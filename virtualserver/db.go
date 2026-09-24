package virtualserver

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// db wraps a *sql.DB with a mutex for serialised writes (WAL mode still allows
// concurrent reads via separate read transactions).
type db struct {
	mu sync.Mutex
	sq *sql.DB
}

func openDB(path string) (*db, error) {
	sq, err := sql.Open("sqlite", path+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("vs openDB: %w", err)
	}
	sq.SetMaxOpenConns(1) // serialise writes at the driver level too
	d := &db{sq: sq}
	if err := d.migrate(); err != nil {
		sq.Close()
		return nil, err
	}
	return d, nil
}

func (d *db) close() error { return d.sq.Close() }

// ── 17 migrations ─────────────────────────────────────────────────────────────

var migrations = []string{
	// 1 – schema_migrations
	`CREATE TABLE IF NOT EXISTS schema_migrations (
		version   INTEGER PRIMARY KEY,
		applied_at TEXT NOT NULL
	)`,
	// 2 – namespaces
	`CREATE TABLE IF NOT EXISTS namespaces (
		name       TEXT PRIMARY KEY,
		created_at TEXT NOT NULL
	)`,
	// 3 – nodes
	`CREATE TABLE IF NOT EXISTS nodes (
		id             TEXT PRIMARY KEY,
		name           TEXT NOT NULL,
		arch           TEXT NOT NULL,
		state          TEXT NOT NULL DEFAULT 'ready',
		cpu_millicores INTEGER NOT NULL DEFAULT 0,
		ram_bytes      INTEGER NOT NULL DEFAULT 0,
		bearer_token   TEXT NOT NULL DEFAULT '',
		last_heartbeat TEXT NOT NULL,
		created_at     TEXT NOT NULL
	)`,
	// 4 – node_labels
	`CREATE TABLE IF NOT EXISTS node_labels (
		node_id TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
		key     TEXT NOT NULL,
		value   TEXT NOT NULL,
		PRIMARY KEY (node_id, key)
	)`,
	// 5 – node_gpus
	`CREATE TABLE IF NOT EXISTS node_gpus (
		id        TEXT PRIMARY KEY,
		node_id   TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
		device_id TEXT NOT NULL,
		model     TEXT NOT NULL DEFAULT '',
		vram_bytes INTEGER NOT NULL DEFAULT 0
	)`,
	// 6 – services
	`CREATE TABLE IF NOT EXISTS services (
		id               TEXT PRIMARY KEY,
		namespace        TEXT NOT NULL REFERENCES namespaces(name) ON DELETE CASCADE,
		name             TEXT NOT NULL,
		image            TEXT NOT NULL,
		cpu_millicores   INTEGER NOT NULL DEFAULT 0,
		ram_bytes        INTEGER NOT NULL DEFAULT 0,
		restart_policy   TEXT NOT NULL DEFAULT 'OnFailure',
		artifact_hash    TEXT NOT NULL DEFAULT '',
		env_json         TEXT NOT NULL DEFAULT '{}',
		annotations_json TEXT NOT NULL DEFAULT '{}',
		node_exclusions_json TEXT NOT NULL DEFAULT '[]',
		node_selector_json TEXT NOT NULL DEFAULT '{}',
		created_at       TEXT NOT NULL,
		updated_at       TEXT NOT NULL
	)`,
	// 7 – service_gpu_requirements
	`CREATE TABLE IF NOT EXISTS service_gpu_requirements (
		id              TEXT PRIMARY KEY,
		service_id      TEXT NOT NULL REFERENCES services(id) ON DELETE CASCADE,
		count           INTEGER NOT NULL DEFAULT 1,
		vram_per_device INTEGER NOT NULL DEFAULT 0,
		model_filter    TEXT NOT NULL DEFAULT ''
	)`,
	// 8 – service_volume_mounts
	`CREATE TABLE IF NOT EXISTS service_volume_mounts (
		id         TEXT PRIMARY KEY,
		service_id TEXT NOT NULL REFERENCES services(id) ON DELETE CASCADE,
		name       TEXT NOT NULL,
		volume_id  TEXT NOT NULL,
		mount_path TEXT NOT NULL,
		read_only  INTEGER NOT NULL DEFAULT 0
	)`,
	// 9 – instances
	`CREATE TABLE IF NOT EXISTS instances (
		id         TEXT PRIMARY KEY,
		service_id TEXT NOT NULL REFERENCES services(id) ON DELETE CASCADE,
		namespace  TEXT NOT NULL,
		node_id    TEXT NOT NULL DEFAULT '',
		state      TEXT NOT NULL DEFAULT 'scheduled',
		message    TEXT NOT NULL DEFAULT '',
		runtime_id TEXT NOT NULL DEFAULT '',
		restarts   INTEGER NOT NULL DEFAULT 0,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL
	)`,
	// 10 – allocations
	`CREATE TABLE IF NOT EXISTS allocations (
		id              TEXT PRIMARY KEY,
		instance_id     TEXT NOT NULL REFERENCES instances(id) ON DELETE CASCADE,
		node_id         TEXT NOT NULL,
		cpu_millicores  INTEGER NOT NULL DEFAULT 0,
		ram_bytes       INTEGER NOT NULL DEFAULT 0,
		gpu_devices_json TEXT NOT NULL DEFAULT '[]',
		created_at      TEXT NOT NULL
	)`,
	// 11 – operations
	`CREATE TABLE IF NOT EXISTS operations (
		id          TEXT PRIMARY KEY,
		type        TEXT NOT NULL,
		resource_id TEXT NOT NULL,
		state       TEXT NOT NULL DEFAULT 'pending',
		message     TEXT NOT NULL DEFAULT '',
		created_at  TEXT NOT NULL,
		updated_at  TEXT NOT NULL
	)`,
	// 12 – volumes
	`CREATE TABLE IF NOT EXISTS volumes (
		id            TEXT PRIMARY KEY,
		namespace     TEXT NOT NULL REFERENCES namespaces(name) ON DELETE CASCADE,
		name          TEXT NOT NULL,
		driver_name   TEXT NOT NULL,
		node_affinity TEXT NOT NULL DEFAULT '',
		size_bytes    INTEGER NOT NULL DEFAULT 0,
		handle        TEXT NOT NULL DEFAULT '',
		created_at    TEXT NOT NULL
	)`,
	// 13 – volume_bindings
	`CREATE TABLE IF NOT EXISTS volume_bindings (
		id          TEXT PRIMARY KEY,
		instance_id TEXT NOT NULL REFERENCES instances(id) ON DELETE CASCADE,
		volume_id   TEXT NOT NULL REFERENCES volumes(id),
		mount_path  TEXT NOT NULL,
		read_only   INTEGER NOT NULL DEFAULT 0,
		host_path   TEXT NOT NULL DEFAULT '',
		created_at  TEXT NOT NULL
	)`,
	// 14 – snapshots
	`CREATE TABLE IF NOT EXISTS snapshots (
		id              TEXT PRIMARY KEY,
		volume_id       TEXT NOT NULL REFERENCES volumes(id) ON DELETE CASCADE,
		snapshot_handle TEXT NOT NULL,
		size_bytes      INTEGER NOT NULL DEFAULT 0,
		created_at      TEXT NOT NULL
	)`,
	// 15 – idempotency_keys
	`CREATE TABLE IF NOT EXISTS idempotency_keys (
		key         TEXT PRIMARY KEY,
		resource_id TEXT NOT NULL,
		created_at  TEXT NOT NULL
	)`,
	// 16 – audit_log
	`CREATE TABLE IF NOT EXISTS vs_audit_log (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		timestamp   TEXT NOT NULL,
		actor       TEXT NOT NULL DEFAULT '',
		action      TEXT NOT NULL,
		resource_id TEXT NOT NULL DEFAULT '',
		success     INTEGER NOT NULL DEFAULT 1,
		message     TEXT NOT NULL DEFAULT ''
	)`,
	// 17 – placements
	`CREATE TABLE IF NOT EXISTS placements (
		id          TEXT PRIMARY KEY,
		instance_id TEXT NOT NULL,
		node_id     TEXT NOT NULL,
		score       REAL NOT NULL DEFAULT 0,
		reason      TEXT NOT NULL DEFAULT '',
		decided_at  TEXT NOT NULL
	)`,
}

func (d *db) migrate() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	// Run each DDL statement unconditionally (IF NOT EXISTS guards idempotency).
	for i, stmt := range migrations {
		if _, err := d.sq.Exec(stmt); err != nil {
			return fmt.Errorf("vs migration %d: %w", i+1, err)
		}
	}
	return nil
}

// ── time helpers ──────────────────────────────────────────────────────────────

func timeToStr(t time.Time) string  { return t.UTC().Format(time.RFC3339Nano) }
func strToTime(s string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, s)
	return t
}

// ── namespace CRUD ────────────────────────────────────────────────────────────

func (d *db) createNamespace(ns *Namespace) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, err := d.sq.Exec(
		`INSERT INTO namespaces(name, created_at) VALUES(?,?) ON CONFLICT(name) DO NOTHING`,
		ns.Name, timeToStr(ns.CreatedAt))
	return err
}

func (d *db) listNamespaces() ([]*Namespace, error) {
	rows, err := d.sq.Query(`SELECT name, created_at FROM namespaces ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Namespace
	for rows.Next() {
		n := &Namespace{}
		var cat string
		if err := rows.Scan(&n.Name, &cat); err != nil {
			continue
		}
		n.CreatedAt = strToTime(cat)
		out = append(out, n)
	}
	return out, rows.Err()
}

func (d *db) deleteNamespace(name string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, err := d.sq.Exec(`DELETE FROM namespaces WHERE name=?`, name)
	return err
}

// ── node CRUD ─────────────────────────────────────────────────────────────────

func (d *db) upsertNode(n *Node) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	tx, err := d.sq.Begin()
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO nodes
		(id, name, arch, state, cpu_millicores, ram_bytes, bearer_token, last_heartbeat, created_at)
		VALUES(?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
		  name=excluded.name, arch=excluded.arch, state=excluded.state,
		  cpu_millicores=excluded.cpu_millicores, ram_bytes=excluded.ram_bytes,
		  bearer_token=excluded.bearer_token, last_heartbeat=excluded.last_heartbeat`,
		n.ID, n.Name, n.Arch, string(n.State), n.CPUMillicores, n.RAMBytes,
		n.BearerToken, timeToStr(n.LastHeartbeat), timeToStr(n.CreatedAt))
	if err != nil {
		tx.Rollback()
		return err
	}
	if _, err := tx.Exec(`DELETE FROM node_labels WHERE node_id=?`, n.ID); err != nil {
		tx.Rollback()
		return err
	}
	for k, v := range n.Labels {
		if _, err := tx.Exec(`INSERT INTO node_labels(node_id,key,value) VALUES(?,?,?)`, n.ID, k, v); err != nil {
			tx.Rollback()
			return err
		}
	}
	if _, err := tx.Exec(`DELETE FROM node_gpus WHERE node_id=?`, n.ID); err != nil {
		tx.Rollback()
		return err
	}
	for _, g := range n.GPUs {
		gid := n.ID + ":" + g.DeviceID
		if _, err := tx.Exec(`INSERT INTO node_gpus(id,node_id,device_id,model,vram_bytes) VALUES(?,?,?,?,?)`,
			gid, n.ID, g.DeviceID, g.Model, g.VRAMBytes); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

func (d *db) getNode(id string) (*Node, error) {
	nodes, err := d.listNodes()
	if err != nil {
		return nil, err
	}
	for _, n := range nodes {
		if n.ID == id {
			return n, nil
		}
	}
	return nil, fmt.Errorf("node %q not found", id)
}

func (d *db) listNodes() ([]*Node, error) {
	rows, err := d.sq.Query(
		`SELECT id, name, arch, state, cpu_millicores, ram_bytes, bearer_token, last_heartbeat, created_at
		 FROM nodes ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var nodes []*Node
	for rows.Next() {
		n := &Node{Labels: make(map[string]string)}
		var state, lh, cat string
		if err := rows.Scan(&n.ID, &n.Name, &n.Arch, &state,
			&n.CPUMillicores, &n.RAMBytes, &n.BearerToken, &lh, &cat); err != nil {
			continue
		}
		n.State = NodeState(state)
		n.LastHeartbeat = strToTime(lh)
		n.CreatedAt = strToTime(cat)
		nodes = append(nodes, n)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, n := range nodes {
		lrows, _ := d.sq.Query(`SELECT key, value FROM node_labels WHERE node_id=?`, n.ID)
		for lrows.Next() {
			var k, v string
			lrows.Scan(&k, &v)
			n.Labels[k] = v
		}
		lrows.Close()
		grows, _ := d.sq.Query(`SELECT device_id, model, vram_bytes FROM node_gpus WHERE node_id=?`, n.ID)
		for grows.Next() {
			var g GPU
			grows.Scan(&g.DeviceID, &g.Model, &g.VRAMBytes)
			n.GPUs = append(n.GPUs, g)
		}
		grows.Close()
	}
	return nodes, nil
}

func (d *db) deleteNode(id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, err := d.sq.Exec(`DELETE FROM nodes WHERE id=?`, id)
	return err
}

// ── service CRUD ──────────────────────────────────────────────────────────────

func (d *db) upsertService(s *Service) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	envJ, _ := json.Marshal(s.Env)
	annJ, _ := json.Marshal(s.Annotations)
	exclJ, _ := json.Marshal(s.NodeExclusions)
	selJ, _ := json.Marshal(s.NodeSelector)
	tx, err := d.sq.Begin()
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO services
		(id, namespace, name, image, cpu_millicores, ram_bytes, restart_policy,
		 artifact_hash, env_json, annotations_json, node_exclusions_json, node_selector_json,
		 created_at, updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
		  name=excluded.name, image=excluded.image,
		  cpu_millicores=excluded.cpu_millicores, ram_bytes=excluded.ram_bytes,
		  restart_policy=excluded.restart_policy, artifact_hash=excluded.artifact_hash,
		  env_json=excluded.env_json, annotations_json=excluded.annotations_json,
		  node_exclusions_json=excluded.node_exclusions_json,
		  node_selector_json=excluded.node_selector_json,
		  updated_at=excluded.updated_at`,
		s.ID, s.Namespace, s.Name, s.Image, s.CPUMillicores, s.RAMBytes,
		string(s.RestartPolicy), s.ArtifactHash,
		string(envJ), string(annJ), string(exclJ), string(selJ),
		timeToStr(s.CreatedAt), timeToStr(s.UpdatedAt))
	if err != nil {
		tx.Rollback()
		return err
	}
	tx.Exec(`DELETE FROM service_gpu_requirements WHERE service_id=?`, s.ID)
	for i, g := range s.GPUs {
		tx.Exec(`INSERT INTO service_gpu_requirements(id,service_id,count,vram_per_device,model_filter)
			VALUES(?,?,?,?,?)`,
			fmt.Sprintf("%s-gpu%d", s.ID, i), s.ID, g.Count, g.VRAMPerDevice, g.ModelFilter)
	}
	tx.Exec(`DELETE FROM service_volume_mounts WHERE service_id=?`, s.ID)
	for i, m := range s.VolumeMounts {
		ro := 0
		if m.ReadOnly {
			ro = 1
		}
		tx.Exec(`INSERT INTO service_volume_mounts(id,service_id,name,volume_id,mount_path,read_only)
			VALUES(?,?,?,?,?,?)`,
			fmt.Sprintf("%s-vm%d", s.ID, i), s.ID, m.Name, m.VolumeID, m.MountPath, ro)
	}
	return tx.Commit()
}

func (d *db) getService(id string) (*Service, error) {
	svcs, err := d.listServices("")
	if err != nil {
		return nil, err
	}
	for _, s := range svcs {
		if s.ID == id {
			return s, nil
		}
	}
	return nil, fmt.Errorf("service %q not found", id)
}

func (d *db) listServices(namespace string) ([]*Service, error) {
	q := `SELECT id, namespace, name, image, cpu_millicores, ram_bytes, restart_policy,
		artifact_hash, env_json, annotations_json, node_exclusions_json, node_selector_json,
		created_at, updated_at FROM services`
	args := []interface{}{}
	if namespace != "" {
		q += " WHERE namespace=?"
		args = append(args, namespace)
	}
	rows, err := d.sq.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var svcs []*Service
	for rows.Next() {
		s := &Service{}
		var rp, envJ, annJ, exclJ, selJ, cat, uat string
		if err := rows.Scan(&s.ID, &s.Namespace, &s.Name, &s.Image,
			&s.CPUMillicores, &s.RAMBytes, &rp, &s.ArtifactHash,
			&envJ, &annJ, &exclJ, &selJ, &cat, &uat); err != nil {
			continue
		}
		s.RestartPolicy = RestartPolicy(rp)
		json.Unmarshal([]byte(envJ), &s.Env)
		json.Unmarshal([]byte(annJ), &s.Annotations)
		json.Unmarshal([]byte(exclJ), &s.NodeExclusions)
		json.Unmarshal([]byte(selJ), &s.NodeSelector)
		s.CreatedAt = strToTime(cat)
		s.UpdatedAt = strToTime(uat)
		svcs = append(svcs, s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, s := range svcs {
		grows, _ := d.sq.Query(`SELECT count, vram_per_device, model_filter FROM service_gpu_requirements WHERE service_id=?`, s.ID)
		for grows.Next() {
			var g GPURequirement
			grows.Scan(&g.Count, &g.VRAMPerDevice, &g.ModelFilter)
			s.GPUs = append(s.GPUs, g)
		}
		grows.Close()
		vrows, _ := d.sq.Query(`SELECT name, volume_id, mount_path, read_only FROM service_volume_mounts WHERE service_id=?`, s.ID)
		for vrows.Next() {
			var m VolumeMount
			var ro int
			vrows.Scan(&m.Name, &m.VolumeID, &m.MountPath, &ro)
			m.ReadOnly = ro == 1
			s.VolumeMounts = append(s.VolumeMounts, m)
		}
		vrows.Close()
	}
	return svcs, nil
}

func (d *db) deleteService(id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, err := d.sq.Exec(`DELETE FROM services WHERE id=?`, id)
	return err
}

// ── instance CRUD ─────────────────────────────────────────────────────────────

func (d *db) upsertInstance(inst *Instance) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, err := d.sq.Exec(`INSERT INTO instances
		(id, service_id, namespace, node_id, state, message, runtime_id, restarts, created_at, updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
		  node_id=excluded.node_id, state=excluded.state,
		  message=excluded.message, runtime_id=excluded.runtime_id,
		  restarts=excluded.restarts, updated_at=excluded.updated_at`,
		inst.ID, inst.ServiceID, inst.Namespace, inst.NodeID,
		string(inst.State), inst.Message, inst.RuntimeID, inst.Restarts,
		timeToStr(inst.CreatedAt), timeToStr(inst.UpdatedAt))
	return err
}

func (d *db) getInstancesByState(state InstanceState) ([]*Instance, error) {
	rows, err := d.sq.Query(
		`SELECT id, service_id, namespace, node_id, state, message, runtime_id, restarts, created_at, updated_at
		 FROM instances WHERE state=?`, string(state))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanInstances(rows)
}

func (d *db) listInstances(namespace string) ([]*Instance, error) {
	q := `SELECT id, service_id, namespace, node_id, state, message, runtime_id, restarts, created_at, updated_at
		FROM instances`
	args := []interface{}{}
	if namespace != "" {
		q += " WHERE namespace=?"
		args = append(args, namespace)
	}
	rows, err := d.sq.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanInstances(rows)
}

func (d *db) getInstance(id string) (*Instance, error) {
	rows, err := d.sq.Query(
		`SELECT id, service_id, namespace, node_id, state, message, runtime_id, restarts, created_at, updated_at
		 FROM instances WHERE id=?`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	insts, err := scanInstances(rows)
	if err != nil || len(insts) == 0 {
		return nil, fmt.Errorf("instance %q not found", id)
	}
	return insts[0], nil
}

func scanInstances(rows *sql.Rows) ([]*Instance, error) {
	var out []*Instance
	for rows.Next() {
		inst := &Instance{}
		var state, cat, uat string
		if err := rows.Scan(&inst.ID, &inst.ServiceID, &inst.Namespace, &inst.NodeID,
			&state, &inst.Message, &inst.RuntimeID, &inst.Restarts, &cat, &uat); err != nil {
			continue
		}
		inst.State = InstanceState(state)
		inst.CreatedAt = strToTime(cat)
		inst.UpdatedAt = strToTime(uat)
		out = append(out, inst)
	}
	return out, rows.Err()
}

func (d *db) deleteInstance(id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, err := d.sq.Exec(`DELETE FROM instances WHERE id=?`, id)
	return err
}

// ── volume CRUD ───────────────────────────────────────────────────────────────

func (d *db) upsertVolume(v *Volume) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, err := d.sq.Exec(`INSERT INTO volumes
		(id, namespace, name, driver_name, node_affinity, size_bytes, handle, created_at)
		VALUES(?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
		  node_affinity=excluded.node_affinity,
		  handle=excluded.handle, size_bytes=excluded.size_bytes`,
		v.ID, v.Namespace, v.Name, v.DriverName, v.NodeAffinity, v.SizeBytes, v.Handle, timeToStr(v.CreatedAt))
	return err
}

func (d *db) getVolume(id string) (*Volume, error) {
	vols, err := d.listVolumes("")
	if err != nil {
		return nil, err
	}
	for _, v := range vols {
		if v.ID == id {
			return v, nil
		}
	}
	return nil, fmt.Errorf("volume %q not found", id)
}

func (d *db) listVolumes(namespace string) ([]*Volume, error) {
	q := `SELECT id, namespace, name, driver_name, node_affinity, size_bytes, handle, created_at FROM volumes`
	args := []interface{}{}
	if namespace != "" {
		q += " WHERE namespace=?"
		args = append(args, namespace)
	}
	rows, err := d.sq.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Volume
	for rows.Next() {
		v := &Volume{}
		var cat string
		if err := rows.Scan(&v.ID, &v.Namespace, &v.Name, &v.DriverName,
			&v.NodeAffinity, &v.SizeBytes, &v.Handle, &cat); err != nil {
			continue
		}
		v.CreatedAt = strToTime(cat)
		out = append(out, v)
	}
	return out, rows.Err()
}

func (d *db) deleteVolume(id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, err := d.sq.Exec(`DELETE FROM volumes WHERE id=?`, id)
	return err
}

// ── snapshot CRUD ─────────────────────────────────────────────────────────────

func (d *db) createSnapshot(s *Snapshot) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, err := d.sq.Exec(`INSERT INTO snapshots(id, volume_id, snapshot_handle, size_bytes, created_at)
		VALUES(?,?,?,?,?)`,
		s.ID, s.VolumeID, s.SnapshotHandle, s.SizeBytes, timeToStr(s.CreatedAt))
	return err
}

func (d *db) listSnapshots(volumeID string) ([]*Snapshot, error) {
	rows, err := d.sq.Query(
		`SELECT id, volume_id, snapshot_handle, size_bytes, created_at FROM snapshots WHERE volume_id=? ORDER BY created_at DESC`,
		volumeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Snapshot
	for rows.Next() {
		s := &Snapshot{}
		var cat string
		rows.Scan(&s.ID, &s.VolumeID, &s.SnapshotHandle, &s.SizeBytes, &cat)
		s.CreatedAt = strToTime(cat)
		out = append(out, s)
	}
	return out, rows.Err()
}

// ── operation CRUD ────────────────────────────────────────────────────────────

func (d *db) createOperation(op *Operation) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, err := d.sq.Exec(`INSERT INTO operations(id,type,resource_id,state,message,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?)`,
		op.ID, op.Type, op.ResourceID, string(op.State), op.Message,
		timeToStr(op.CreatedAt), timeToStr(op.UpdatedAt))
	return err
}

func (d *db) updateOperation(id string, state OperationState, msg string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, err := d.sq.Exec(`UPDATE operations SET state=?, message=?, updated_at=? WHERE id=?`,
		string(state), msg, timeToStr(time.Now().UTC()), id)
	return err
}

func (d *db) getOperation(id string) (*Operation, error) {
	row := d.sq.QueryRow(`SELECT id, type, resource_id, state, message, created_at, updated_at FROM operations WHERE id=?`, id)
	op := &Operation{}
	var state, cat, uat string
	if err := row.Scan(&op.ID, &op.Type, &op.ResourceID, &state, &op.Message, &cat, &uat); err != nil {
		return nil, fmt.Errorf("operation %q not found", id)
	}
	op.State = OperationState(state)
	op.CreatedAt = strToTime(cat)
	op.UpdatedAt = strToTime(uat)
	return op, nil
}

// ── allocation helpers ────────────────────────────────────────────────────────

func (d *db) createAllocation(a *Allocation) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	gpuJ, _ := json.Marshal(a.GPUDeviceIDs)
	_, err := d.sq.Exec(`INSERT INTO allocations(id,instance_id,node_id,cpu_millicores,ram_bytes,gpu_devices_json,created_at)
		VALUES(?,?,?,?,?,?,?)`,
		a.ID, a.InstanceID, a.NodeID, a.CPUMillicores, a.RAMBytes, string(gpuJ), timeToStr(a.CreatedAt))
	return err
}

func (d *db) allocationsForNode(nodeID string) ([]*Allocation, error) {
	rows, err := d.sq.Query(
		`SELECT a.id, a.instance_id, a.node_id, a.cpu_millicores, a.ram_bytes, a.gpu_devices_json, a.created_at
		 FROM allocations a JOIN instances i ON a.instance_id=i.id
		 WHERE a.node_id=? AND i.state NOT IN ('stopped','failed')`,
		nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Allocation
	for rows.Next() {
		a := &Allocation{}
		var gpuJ, cat string
		rows.Scan(&a.ID, &a.InstanceID, &a.NodeID, &a.CPUMillicores, &a.RAMBytes, &gpuJ, &cat)
		json.Unmarshal([]byte(gpuJ), &a.GPUDeviceIDs)
		a.CreatedAt = strToTime(cat)
		out = append(out, a)
	}
	return out, rows.Err()
}

// ── placement record ──────────────────────────────────────────────────────────

func (d *db) recordPlacement(p *PlacementDecision) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	id := p.InstanceID + ":" + timeToStr(p.DecidedAt)
	_, err := d.sq.Exec(`INSERT INTO placements(id,instance_id,node_id,score,reason,decided_at)
		VALUES(?,?,?,?,?,?)`,
		id, p.InstanceID, p.NodeID, p.Score, p.Reason, timeToStr(p.DecidedAt))
	return err
}

// ── volume binding ────────────────────────────────────────────────────────────

func (d *db) createBinding(b *VolumeBinding) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	ro := 0
	if b.ReadOnly {
		ro = 1
	}
	_, err := d.sq.Exec(`INSERT INTO volume_bindings(id,instance_id,volume_id,mount_path,read_only,host_path,created_at)
		VALUES(?,?,?,?,?,?,?)`,
		b.ID, b.InstanceID, b.VolumeID, b.MountPath, ro, b.HostPath, timeToStr(b.CreatedAt))
	return err
}

func (d *db) bindingsForInstance(instanceID string) ([]*VolumeBinding, error) {
	rows, err := d.sq.Query(
		`SELECT id, instance_id, volume_id, mount_path, read_only, host_path, created_at
		 FROM volume_bindings WHERE instance_id=?`, instanceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*VolumeBinding
	for rows.Next() {
		b := &VolumeBinding{}
		var ro int
		var cat string
		rows.Scan(&b.ID, &b.InstanceID, &b.VolumeID, &b.MountPath, &ro, &b.HostPath, &cat)
		b.ReadOnly = ro == 1
		b.CreatedAt = strToTime(cat)
		out = append(out, b)
	}
	return out, rows.Err()
}

func (d *db) deleteBindingsForInstance(instanceID string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, err := d.sq.Exec(`DELETE FROM volume_bindings WHERE instance_id=?`, instanceID)
	return err
}

// ── idempotency ───────────────────────────────────────────────────────────────

// checkOrSetIdempotency returns the existing resource ID if key was already
// processed, or stores the new mapping and returns "".
func (d *db) checkOrSetIdempotency(key, resourceID string) (existing string, err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	row := d.sq.QueryRow(`SELECT resource_id FROM idempotency_keys WHERE key=?`, key)
	var id string
	if err := row.Scan(&id); err == nil {
		return id, nil
	}
	_, err = d.sq.Exec(`INSERT INTO idempotency_keys(key, resource_id, created_at) VALUES(?,?,?)`,
		key, resourceID, timeToStr(time.Now().UTC()))
	return "", err
}

// ── audit ─────────────────────────────────────────────────────────────────────

func (d *db) audit(actor, action, resourceID string, success bool, msg string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	succ := 1
	if !success {
		succ = 0
	}
	d.sq.Exec(`INSERT INTO vs_audit_log(timestamp, actor, action, resource_id, success, message)
		VALUES(?,?,?,?,?,?)`,
		timeToStr(time.Now().UTC()), actor, action, resourceID, succ, msg)
}

// ── helper: node headroom ─────────────────────────────────────────────────────

// nodeHeadroom returns the remaining CPU millicores and RAM bytes on nodeID
// by subtracting active allocations from the node's total capacity.
func (d *db) nodeHeadroom(nodeID string, totalCPU int, totalRAM int64) (freeCPU int, freeRAM int64, err error) {
	allocs, err := d.allocationsForNode(nodeID)
	if err != nil {
		return 0, 0, err
	}
	usedCPU := 0
	var usedRAM int64
	for _, a := range allocs {
		usedCPU += a.CPUMillicores
		usedRAM += a.RAMBytes
	}
	return totalCPU - usedCPU, totalRAM - usedRAM, nil
}

// nodeAllocatedGPUs returns the set of GPU device IDs currently allocated on nodeID.
func (d *db) nodeAllocatedGPUs(nodeID string) (map[string]struct{}, error) {
	allocs, err := d.allocationsForNode(nodeID)
	if err != nil {
		return nil, err
	}
	used := make(map[string]struct{})
	for _, a := range allocs {
		for _, gid := range a.GPUDeviceIDs {
			used[gid] = struct{}{}
		}
	}
	return used, nil
}

// ── helper: volume affinity node ──────────────────────────────────────────────

func (d *db) volumeAffinityNode(volumeID string) (string, error) {
	row := d.sq.QueryRow(`SELECT node_affinity FROM volumes WHERE id=?`, volumeID)
	var aff string
	if err := row.Scan(&aff); err != nil {
		return "", nil
	}
	return aff, nil
}

// setVolumeAffinity persists the node affinity for a volume.
func (d *db) setVolumeAffinity(volumeID, nodeID string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, err := d.sq.Exec(`UPDATE volumes SET node_affinity=? WHERE id=?`, nodeID, volumeID)
	return err
}

// ── recent failures ───────────────────────────────────────────────────────────

// nodeRecentFailures counts instances on nodeID that reached 'failed' state
// in the last window duration (used by scorer).
func (d *db) nodeRecentFailures(nodeID string, since time.Time) (int, error) {
	row := d.sq.QueryRow(
		`SELECT COUNT(*) FROM instances WHERE node_id=? AND state='failed' AND updated_at >= ?`,
		nodeID, timeToStr(since))
	var count int
	err := row.Scan(&count)
	return count, err
}

// ── idempotency key cleanup (unused but available) ───────────────────────────

func (d *db) pruneIdempotencyKeys(before time.Time) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	ts := timeToStr(before)
	_, err := d.sq.Exec(`DELETE FROM idempotency_keys WHERE created_at < ?`, ts)
	return err
}

// jsonStrings marshals a []string to its JSON representation.
func jsonStrings(ss []string) string {
	b, _ := json.Marshal(ss)
	return string(b)
}

// commaSep joins strings with a comma.
func commaSep(ss []string) string { return strings.Join(ss, ",") }
