package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Allocation holds a resource reservation for one instance on one node.
type Allocation struct {
	ID             int64
	InstanceID     string
	NodeID         string
	CPUThreads     int
	RAMMiB         int64
	GPUDeviceIndex *int
	AllocatedAt    time.Time
	ReleasedAt     *time.Time
}

// AllocateResources atomically reserves CPU, RAM, and optionally a GPU device
// for the given instance. It re-checks availability inside the write lock to
// prevent TOCTOU double-allocation.
func (s *VSStore) AllocateResources(ctx context.Context, instanceID, nodeID string, cpuThreads int, ramMiB int64, gpuDeviceIndex *int) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin alloc tx: %w", err)
	}
	defer tx.Rollback()

	// Re-verify available resources inside the transaction
	var availRAM int64
	var availCPU int
	row := tx.QueryRowContext(ctx,
		"SELECT avail_ram_mib, cpu_threads FROM virtualserver_nodes WHERE id=?", nodeID)
	if err := row.Scan(&availRAM, &availCPU); err == sql.ErrNoRows {
		return ErrNotFound
	} else if err != nil {
		return fmt.Errorf("read node resources: %w", err)
	}

	if availRAM < ramMiB {
		return fmt.Errorf("%w: node %s has %d MiB RAM available, need %d", ErrAllocationConflict, nodeID, availRAM, ramMiB)
	}
	// Note: we track avail_ram but not avail_cpu separately in DB yet; use
	// cpu_threads as total. For a full implementation avail_cpu would be tracked.
	_ = availCPU

	// Check GPU device availability if required
	if gpuDeviceIndex != nil {
		var allocated int
		tx.QueryRowContext(ctx, `
			SELECT allocated FROM virtualserver_node_capabilities
			WHERE node_id=? AND device_index=? AND cap_type='gpu'`,
			nodeID, *gpuDeviceIndex,
		).Scan(&allocated)
		if allocated != 0 {
			return fmt.Errorf("%w: GPU device %d on node %s is already allocated", ErrAllocationConflict, *gpuDeviceIndex, nodeID)
		}
	}

	// Write allocation record
	now := time.Now().Unix()
	var gpuIdx interface{}
	if gpuDeviceIndex != nil {
		gpuIdx = *gpuDeviceIndex
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO virtualserver_allocations
		(instance_id,node_id,cpu_threads,ram_mib,gpu_device_index,allocated_at)
		VALUES (?,?,?,?,?,?)`,
		instanceID, nodeID, cpuThreads, ramMiB, gpuIdx, now,
	)
	if err != nil {
		return fmt.Errorf("insert allocation: %w", err)
	}

	// Update node available RAM
	_, err = tx.ExecContext(ctx, `
		UPDATE virtualserver_nodes SET avail_ram_mib=avail_ram_mib-?, updated_at=? WHERE id=?`,
		ramMiB, now, nodeID,
	)
	if err != nil {
		return fmt.Errorf("update node ram: %w", err)
	}

	// Mark GPU device allocated
	if gpuDeviceIndex != nil {
		_, err = tx.ExecContext(ctx, `
			UPDATE virtualserver_node_capabilities
			SET allocated=1
			WHERE node_id=? AND device_index=? AND cap_type='gpu'`,
			nodeID, *gpuDeviceIndex,
		)
		if err != nil {
			return fmt.Errorf("mark gpu allocated: %w", err)
		}
	}

	return tx.Commit()
}

// ReleaseAllocation marks an allocation as released and restores node resources.
func (s *VSStore) ReleaseAllocation(ctx context.Context, instanceID string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var alloc Allocation
	var gpuIdx sql.NullInt64
	row := tx.QueryRowContext(ctx, `
		SELECT id,node_id,cpu_threads,ram_mib,gpu_device_index
		FROM virtualserver_allocations
		WHERE instance_id=? AND released_at IS NULL`, instanceID)
	if err := row.Scan(&alloc.ID, &alloc.NodeID, &alloc.CPUThreads, &alloc.RAMMiB, &gpuIdx); err == sql.ErrNoRows {
		return nil // already released
	} else if err != nil {
		return err
	}

	now := time.Now().Unix()
	_, err = tx.ExecContext(ctx, `
		UPDATE virtualserver_allocations SET released_at=? WHERE id=?`, now, alloc.ID)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		UPDATE virtualserver_nodes SET avail_ram_mib=avail_ram_mib+?, updated_at=? WHERE id=?`,
		alloc.RAMMiB, now, alloc.NodeID,
	)
	if err != nil {
		return err
	}
	if gpuIdx.Valid {
		tx.ExecContext(ctx, `
			UPDATE virtualserver_node_capabilities
			SET allocated=0
			WHERE node_id=? AND device_index=? AND cap_type='gpu'`,
			alloc.NodeID, gpuIdx.Int64,
		)
	}
	return tx.Commit()
}

// GetActiveAllocation returns the active allocation for an instance.
func (s *VSStore) GetActiveAllocation(ctx context.Context, instanceID string) (*Allocation, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id,instance_id,node_id,cpu_threads,ram_mib,gpu_device_index,allocated_at
		FROM virtualserver_allocations
		WHERE instance_id=? AND released_at IS NULL`, instanceID)
	var a Allocation
	var gpuIdx sql.NullInt64
	var allocatedAt int64
	if err := row.Scan(&a.ID, &a.InstanceID, &a.NodeID, &a.CPUThreads, &a.RAMMiB, &gpuIdx, &allocatedAt); err == sql.ErrNoRows {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, err
	}
	if gpuIdx.Valid {
		idx := int(gpuIdx.Int64)
		a.GPUDeviceIndex = &idx
	}
	a.AllocatedAt = time.Unix(allocatedAt, 0)
	return &a, nil
}
