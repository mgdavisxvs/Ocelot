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

	_, err = tx.ExecContext(ctx, `
		INSERT INTO virtualserver_nodes
		(id,name,backend_type,arch,os,cpu_model,cpu_threads,ram_mib,avail_ram_mib,
		 storage_mib,avail_storage_mib,labels,location,trust_class,state,
		 agent_metadata,created_at,updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		n.ID, n.Name, n.BackendType, n.Arch, n.OS, n.CPUModel,
		n.CPUThreads, n.TotalRAMMiB, n.AvailRAMMiB,
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
		SELECT id,name,backend_type,arch,os,cpu_model,cpu_threads,
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
		SELECT id,name,backend_type,arch,os,cpu_model,cpu_threads,
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
func (s *VSStore) ListNodes(ctx context.Context, stateFilter string) ([]domain.Node, error) {
	var rows *sql.Rows
	var err error
	if stateFilter != "" {
		rows, err = s.db.QueryContext(ctx, `
			SELECT id,name,backend_type,arch,os,cpu_model,cpu_threads,
			       ram_mib,avail_ram_mib,storage_mib,avail_storage_mib,
			       labels,location,trust_class,state,last_heartbeat,
			       agent_metadata,created_at,updated_at
			FROM virtualserver_nodes WHERE state=? ORDER BY name`, stateFilter)
	} else {
		rows, err = s.db.QueryContext(ctx, `
			SELECT id,name,backend_type,arch,os,cpu_model,cpu_threads,
			       ram_mib,avail_ram_mib,storage_mib,avail_storage_mib,
			       labels,location,trust_class,state,last_heartbeat,
			       agent_metadata,created_at,updated_at
			FROM virtualserver_nodes ORDER BY name`)
	}
	if err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}
	defer rows.Close()

	var nodes []domain.Node
	for rows.Next() {
		n, err := scanNodeRow(rows)
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, *n)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close() // release connection before issuing per-node GPU queries

	for i := range nodes {
		gpus, err := s.loadGPUDevices(ctx, nodes[i].ID)
		if err != nil {
			return nil, err
		}
		nodes[i].GPUDevices = gpus
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
	_, err = s.db.ExecContext(ctx,
		"UPDATE virtualserver_nodes SET state=?, updated_at=? WHERE id=?",
		string(to), time.Now().Unix(), id,
	)
	return err
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
		SET last_heartbeat=?, avail_ram_mib=?, state=?, updated_at=?
		WHERE id=?`,
		now, availRAMMiB, newState, now, id,
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
		&n.CPUThreads, &n.TotalRAMMiB, &n.AvailRAMMiB,
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
