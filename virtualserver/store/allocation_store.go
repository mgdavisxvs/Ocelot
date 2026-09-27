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

	now := time.Now().Unix()

	// VS-D-K2: atomically verify and deduct available resources in a single
	// UPDATE WHERE clause — eliminates the TOCTOU window between the prior
	// SELECT and UPDATE. RowsAffected == 0 means the node does not exist or
	// cannot satisfy the request.
	cpuCond := cpuThreads
	if cpuThreads == 0 {
		cpuCond = 0 // zero CPU request always satisfies
	}
	res, err := tx.ExecContext(ctx, `
		UPDATE virtualserver_nodes
		SET avail_ram_mib = avail_ram_mib - ?,
		    avail_cpu_threads = avail_cpu_threads - ?,
		    updated_at = ?
		WHERE id = ?
		  AND avail_ram_mib >= ?
		  AND (? = 0 OR avail_cpu_threads >= ?)`,
		ramMiB, cpuThreads, now,
		nodeID,
		ramMiB,
		cpuCond, cpuThreads,
	)
	if err != nil {
		return fmt.Errorf("atomic allocate node resources: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("check alloc rows affected: %w", err)
	}
	if affected == 0 {
		// Distinguish not-found from insufficient-resources by checking existence.
		var exists int
		tx.QueryRowContext(ctx, `SELECT 1 FROM virtualserver_nodes WHERE id=?`, nodeID).Scan(&exists)
		if exists == 0 {
			return ErrNotFound
		}
		return fmt.Errorf("%w: node %s cannot satisfy ram=%d cpu=%d", ErrAllocationConflict, nodeID, ramMiB, cpuThreads)
	}

	// Write allocation record
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
		UPDATE virtualserver_nodes
		SET avail_ram_mib=avail_ram_mib+?, avail_cpu_threads=avail_cpu_threads+?, updated_at=?
		WHERE id=?`,
		alloc.RAMMiB, alloc.CPUThreads, now, alloc.NodeID,
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

// ListActiveAllocations returns all unreleased allocations for a node,
// ordered by ram_mib ascending (mirrors the AllocationHeap ordering).
func (s *VSStore) ListActiveAllocations(ctx context.Context, nodeID string) ([]Allocation, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id,instance_id,node_id,cpu_threads,ram_mib,gpu_device_index,allocated_at
		FROM virtualserver_allocations
		WHERE node_id=? AND released_at IS NULL
		ORDER BY ram_mib ASC`, nodeID)
	if err != nil {
		return nil, fmt.Errorf("list active allocations: %w", err)
	}
	defer rows.Close()
	var allocs []Allocation
	for rows.Next() {
		var a Allocation
		var gpuIdx sql.NullInt64
		var allocatedAt int64
		if err := rows.Scan(&a.ID, &a.InstanceID, &a.NodeID, &a.CPUThreads, &a.RAMMiB, &gpuIdx, &allocatedAt); err != nil {
			return nil, err
		}
		if gpuIdx.Valid {
			idx := int(gpuIdx.Int64)
			a.GPUDeviceIndex = &idx
		}
		a.AllocatedAt = time.Unix(allocatedAt, 0)
		allocs = append(allocs, a)
	}
	return allocs, rows.Err()
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
