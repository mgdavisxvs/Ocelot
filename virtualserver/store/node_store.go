package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/mgdavisxvs/Ocelot/virtualserver/domain"
)

// CreateNode inserts a new node. Returns the assigned ID.
func (s *VSStore) CreateNode(ctx context.Context, n domain.Node) (string, error) {
	if n.ID == "" {
		n.ID = uuid.New().String()
	}
	labelsJSON, _ := json.Marshal(n.Labels)
	metaJSON, _ := json.Marshal(n.AgentMeta)
	now := time.Now().Unix()

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	availCPU := n.AvailCPUThreads
	if availCPU == 0 {
		availCPU = n.CPUThreads
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO virtualserver_nodes
		(id,name,backend_type,arch,os,cpu_model,cpu_threads,avail_cpu_threads,ram_mib,avail_ram_mib,
		 storage_mib,avail_storage_mib,labels,location,trust_class,state,
		 agent_metadata,created_at,updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		n.ID, n.Name, n.BackendType, n.Arch, n.OS, n.CPUModel,
		n.CPUThreads, availCPU, n.TotalRAMMiB, n.AvailRAMMiB,
		n.StorageMiB, n.AvailStorageMiB,
		string(labelsJSON), n.Location, n.TrustClass, string(n.State),
		string(metaJSON), now, now,
	)
	if err != nil {
		return "", fmt.Errorf("insert node: %w", err)
	}

	for _, g := range n.GPUDevices {
		_, err = tx.ExecContext(ctx, `
			INSERT INTO virtualserver_node_capabilities
			(node_id,cap_type,vendor,model,vram_mib,device_index,count,allocated)
			VALUES (?,?,?,?,?,?,?,?)`,
			n.ID, "gpu", g.Vendor, g.Model, g.VRAMMiB, g.Index, 1, boolToInt(g.Allocated),
		)
		if err != nil {
			return "", fmt.Errorf("insert gpu capability: %w", err)
		}
	}

	return n.ID, tx.Commit()
}

// GetNode retrieves a node by ID.
func (s *VSStore) GetNode(ctx context.Context, id string) (*domain.Node, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id,name,backend_type,arch,os,cpu_model,cpu_threads,avail_cpu_threads,
		       ram_mib,avail_ram_mib,storage_mib,avail_storage_mib,
		       labels,location,trust_class,state,last_heartbeat,
		       agent_metadata,created_at,updated_at
		FROM virtualserver_nodes WHERE id=?`, id)
	n, err := scanNode(row)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	gpus, err := s.loadGPUDevices(ctx, id)
	if err != nil {
		return nil, err
	}
	n.GPUDevices = gpus
	return n, nil
}

// GetNodeByName retrieves a node by its human-readable name.
func (s *VSStore) GetNodeByName(ctx context.Context, name string) (*domain.Node, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id,name,backend_type,arch,os,cpu_model,cpu_threads,avail_cpu_threads,
		       ram_mib,avail_ram_mib,storage_mib,avail_storage_mib,
		       labels,location,trust_class,state,last_heartbeat,
		       agent_metadata,created_at,updated_at
		FROM virtualserver_nodes WHERE name=?`, name)
	n, err := scanNode(row)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	gpus, err := s.loadGPUDevices(ctx, n.ID)
	if err != nil {
		return nil, err
	}
	n.GPUDevices = gpus
	return n, nil
}

// ListNodes returns all nodes, optionally filtered by state.
// Uses a single LEFT JOIN to fetch GPU devices, eliminating the prior N+1 query pattern.
func (s *VSStore) ListNodes(ctx context.Context, stateFilter string) ([]domain.Node, error) {
	q := `
		SELECT n.id, n.name, n.backend_type, n.arch, n.os, n.cpu_model,
		       n.cpu_threads, n.avail_cpu_threads,
		       n.ram_mib, n.avail_ram_mib, n.storage_mib, n.avail_storage_mib,
		       n.labels, n.location, n.trust_class, n.state, n.last_heartbeat,
		       n.agent_metadata, n.created_at, n.updated_at,
		       c.device_index, c.vendor, c.model, c.vram_mib, c.allocated
		FROM virtualserver_nodes n
		LEFT JOIN virtualserver_node_capabilities c ON c.node_id = n.id AND c.cap_type = 'gpu'`
	var args []interface{}
	if stateFilter != "" {
		q += " WHERE n.state = ?"
		args = append(args, stateFilter)
	}
	q += " ORDER BY n.name, c.device_index"

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}
	defer rows.Close()

	var nodeOrder []string
	nodeMap := make(map[string]*domain.Node)

	for rows.Next() {
		var n domain.Node
		var state, labelsJSON, metaJSON string
		var lastHB sql.NullInt64
		var createdAt, updatedAt int64
		var gpuIdx, gpuVRAM, gpuAlloc sql.NullInt64
		var gpuVendor, gpuModel sql.NullString

		if err := rows.Scan(
			&n.ID, &n.Name, &n.BackendType, &n.Arch, &n.OS, &n.CPUModel,
			&n.CPUThreads, &n.AvailCPUThreads,
			&n.TotalRAMMiB, &n.AvailRAMMiB, &n.StorageMiB, &n.AvailStorageMiB,
			&labelsJSON, &n.Location, &n.TrustClass, &state, &lastHB,
			&metaJSON, &createdAt, &updatedAt,
			&gpuIdx, &gpuVendor, &gpuModel, &gpuVRAM, &gpuAlloc,
		); err != nil {
			return nil, err
		}

		existing, seen := nodeMap[n.ID]
		if !seen {
			n.State = domain.NodeState(state)
			if lastHB.Valid {
				t := time.Unix(lastHB.Int64, 0)
				n.LastHeartbeat = &t
			}
			n.Labels = make(map[string]string)
			json.Unmarshal([]byte(labelsJSON), &n.Labels) //nolint:errcheck
			n.AgentMeta = make(map[string]string)
			json.Unmarshal([]byte(metaJSON), &n.AgentMeta) //nolint:errcheck
			n.CreatedAt = time.Unix(createdAt, 0)
			n.UpdatedAt = time.Unix(updatedAt, 0)
			nodeMap[n.ID] = &n
			nodeOrder = append(nodeOrder, n.ID)
			existing = nodeMap[n.ID]
		}

		if gpuIdx.Valid {
			existing.GPUDevices = append(existing.GPUDevices, domain.GPUDevice{
				Index:     int(gpuIdx.Int64),
				Vendor:    gpuVendor.String,
				Model:     gpuModel.String,
				VRAMMiB:   gpuVRAM.Int64,
				Allocated: gpuAlloc.Int64 != 0,
			})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	nodes := make([]domain.Node, 0, len(nodeOrder))
	for _, id := range nodeOrder {
		nodes = append(nodes, *nodeMap[id])
	}
	return nodes, nil
}

// UpdateNodeState transitions a node to a new state with validation.
// Protected states (quarantined/retired/draining/maintenance) cannot be
// promoted to ready by heartbeat; use this only for explicit state changes.
func (s *VSStore) UpdateNodeState(ctx context.Context, id string, to domain.NodeState) error {
	n, err := s.GetNode(ctx, id)
	if err != nil {
		return err
	}
	if err := domain.ValidateNodeTransition(n.State, to); err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	res, err := s.db.ExecContext(ctx,
		"UPDATE virtualserver_nodes SET state=?, updated_at=? WHERE id=? AND state=?",
		string(to), time.Now().Unix(), id, string(n.State),
	)
	if err != nil {
		return err
	}
	if rows, _ := res.RowsAffected(); rows == 0 {
		return fmt.Errorf("concurrent state change on node %s", id)
	}
	return nil
}

// Heartbeat updates last_heartbeat and available resources.
// Protected nodes have their state preserved — they are NOT promoted to ready.
func (s *VSStore) Heartbeat(ctx context.Context, id string, availRAMMiB int64, availCPU int) error {
	n, err := s.GetNode(ctx, id)
	if err != nil {
		return err
	}
	now := time.Now().Unix()
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	newState := string(n.State)
	if n.State == domain.NodeDiscovered {
		newState = string(domain.NodeReady)
	}
	// Protected states are preserved
	if domain.IsProtectedNodeState(n.State) {
		newState = string(n.State)
	}

	_, err = s.db.ExecContext(ctx, `
		UPDATE virtualserver_nodes
		SET last_heartbeat=?, avail_ram_mib=?, avail_cpu_threads=?, state=?, updated_at=?
		WHERE id=?`,
		now, availRAMMiB, availCPU, newState, now, id,
	)
	return err
}

func (s *VSStore) loadGPUDevices(ctx context.Context, nodeID string) ([]domain.GPUDevice, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT device_index, vendor, model, vram_mib, allocated
		FROM virtualserver_node_capabilities
		WHERE node_id=? AND cap_type='gpu'
		ORDER BY device_index`, nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var gpus []domain.GPUDevice
	for rows.Next() {
		var g domain.GPUDevice
		var allocated int
		if err := rows.Scan(&g.Index, &g.Vendor, &g.Model, &g.VRAMMiB, &allocated); err != nil {
			return nil, err
		}
		g.Allocated = allocated != 0
		gpus = append(gpus, g)
	}
	return gpus, rows.Err()
}

// UpdateGPUAllocation sets the allocated flag for a specific device on a node.
func (s *VSStore) UpdateGPUAllocation(ctx context.Context, nodeID string, deviceIndex int, allocated bool) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.db.ExecContext(ctx, `
		UPDATE virtualserver_node_capabilities
		SET allocated=?
		WHERE node_id=? AND device_index=? AND cap_type='gpu'`,
		boolToInt(allocated), nodeID, deviceIndex,
	)
	return err
}

// ── scanner helpers ───────────────────────────────────────────────────────────

type nodeScanner interface {
	Scan(dest ...interface{}) error
}

func scanNode(r nodeScanner) (*domain.Node, error) {
	return scanNodeFields(r)
}

func scanNodeRow(rows *sql.Rows) (*domain.Node, error) {
	return scanNodeFields(rows)
}

func scanNodeFields(r nodeScanner) (*domain.Node, error) {
	var n domain.Node
	var state string
	var labelsJSON, metaJSON string
	var lastHB sql.NullInt64
	var createdAt, updatedAt int64
	err := r.Scan(
		&n.ID, &n.Name, &n.BackendType, &n.Arch, &n.OS, &n.CPUModel,
		&n.CPUThreads, &n.AvailCPUThreads, &n.TotalRAMMiB, &n.AvailRAMMiB,
		&n.StorageMiB, &n.AvailStorageMiB,
		&labelsJSON, &n.Location, &n.TrustClass, &state,
		&lastHB, &metaJSON, &createdAt, &updatedAt,
	)
	if err != nil {
		return nil, err
	}
	n.State = domain.NodeState(state)
	if lastHB.Valid {
		t := time.Unix(lastHB.Int64, 0)
		n.LastHeartbeat = &t
	}
	n.Labels = make(map[string]string)
	json.Unmarshal([]byte(labelsJSON), &n.Labels)
	n.AgentMeta = make(map[string]string)
	json.Unmarshal([]byte(metaJSON), &n.AgentMeta)
	n.CreatedAt = time.Unix(createdAt, 0)
	n.UpdatedAt = time.Unix(updatedAt, 0)
	return &n, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
