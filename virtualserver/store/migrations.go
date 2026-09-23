package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// VSStore is the persistence layer for the VirtualServer subsystem.
// It manages a dedicated SQLite database (vs.db), separate from the tracker shards.
type VSStore struct {
	db      *sql.DB
	writeMu sync.Mutex // serializes all write operations
}

// Open creates or opens the VS SQLite database at the given path,
// applies all migrations, and returns a ready-to-use VSStore.
func Open(path string) (*VSStore, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, fmt.Errorf("create vs db dir: %w", err)
	}
	dsn := fmt.Sprintf("file:%s?cache=shared&mode=rwc", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open vs sqlite: %w", err)
	}
	// Single writer, multiple readers
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(time.Hour)

	if err := applyPragmas(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("vs sqlite pragmas: %w", err)
	}
	s := &VSStore{db: db}
	if err := s.runMigrations(); err != nil {
		db.Close()
		return nil, fmt.Errorf("vs migrations: %w", err)
	}
	return s, nil
}

func applyPragmas(db *sql.DB) error {
	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA cache_size=-32000",
		"PRAGMA temp_store=MEMORY",
		"PRAGMA mmap_size=134217728",
		"PRAGMA page_size=4096",
		"PRAGMA wal_autocheckpoint=1000",
		"PRAGMA busy_timeout=5000",
		"PRAGMA foreign_keys=ON",
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
	}
	return nil
}

// Close closes the underlying database connection.
func (s *VSStore) Close() error {
	s.db.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
	return s.db.Close()
}

// CheckpointWAL runs a passive WAL checkpoint.
func (s *VSStore) CheckpointWAL() error {
	_, err := s.db.Exec("PRAGMA wal_checkpoint(PASSIVE)")
	return err
}

// DB returns the underlying *sql.DB for direct queries (read-only use in tests).
func (s *VSStore) DB() *sql.DB { return s.db }

// runMigrations applies all pending migrations in order.
func (s *VSStore) runMigrations() error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	// Bootstrap migration tracking table
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version     INTEGER PRIMARY KEY,
		applied_at  INTEGER NOT NULL,
		description TEXT    NOT NULL
	)`)
	if err != nil {
		return fmt.Errorf("bootstrap schema_migrations: %w", err)
	}

	for _, m := range migrations {
		var count int
		row := s.db.QueryRow("SELECT COUNT(*) FROM schema_migrations WHERE version=?", m.version)
		if err := row.Scan(&count); err != nil {
			return fmt.Errorf("check migration %d: %w", m.version, err)
		}
		if count > 0 {
			continue
		}
		tx, err := s.db.Begin()
		if err != nil {
			return fmt.Errorf("begin migration %d tx: %w", m.version, err)
		}
		if _, err := tx.Exec(m.up); err != nil {
			tx.Rollback() //nolint:errcheck
			return fmt.Errorf("apply migration %d (%s): %w", m.version, m.description, err)
		}
		if _, err := tx.Exec(
			"INSERT INTO schema_migrations(version,applied_at,description) VALUES(?,?,?)",
			m.version, time.Now().Unix(), m.description,
		); err != nil {
			tx.Rollback() //nolint:errcheck
			return fmt.Errorf("record migration %d: %w", m.version, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %d: %w", m.version, err)
		}
	}
	return nil
}

type migration struct {
	version     int
	description string
	up          string
}

var migrations = []migration{
	{
		version:     1,
		description: "virtualserver_namespaces",
		up: `CREATE TABLE IF NOT EXISTS virtualserver_namespaces (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			name        TEXT    NOT NULL UNIQUE,
			description TEXT    NOT NULL DEFAULT '',
			created_at  INTEGER NOT NULL,
			updated_at  INTEGER NOT NULL
		)`,
	},
	{
		version:     2,
		description: "virtualserver_nodes",
		up: `CREATE TABLE IF NOT EXISTS virtualserver_nodes (
			id               TEXT    PRIMARY KEY,
			name             TEXT    NOT NULL UNIQUE,
			backend_type     TEXT    NOT NULL DEFAULT '',
			arch             TEXT    NOT NULL DEFAULT '',
			os               TEXT    NOT NULL DEFAULT '',
			cpu_model        TEXT    NOT NULL DEFAULT '',
			cpu_threads      INTEGER NOT NULL DEFAULT 0,
			ram_mib          INTEGER NOT NULL DEFAULT 0,
			avail_ram_mib    INTEGER NOT NULL DEFAULT 0,
			storage_mib      INTEGER NOT NULL DEFAULT 0,
			avail_storage_mib INTEGER NOT NULL DEFAULT 0,
			labels           TEXT    NOT NULL DEFAULT '{}',
			location         TEXT    NOT NULL DEFAULT '',
			trust_class      TEXT    NOT NULL DEFAULT 'standard',
			state            TEXT    NOT NULL DEFAULT 'discovered',
			last_heartbeat   INTEGER,
			agent_metadata   TEXT    NOT NULL DEFAULT '{}',
			created_at       INTEGER NOT NULL,
			updated_at       INTEGER NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_nodes_state ON virtualserver_nodes(state);
		CREATE INDEX IF NOT EXISTS idx_nodes_arch  ON virtualserver_nodes(arch)`,
	},
	{
		version:     3,
		description: "virtualserver_node_capabilities",
		up: `CREATE TABLE IF NOT EXISTS virtualserver_node_capabilities (
			id        INTEGER PRIMARY KEY AUTOINCREMENT,
			node_id   TEXT    NOT NULL REFERENCES virtualserver_nodes(id) ON DELETE CASCADE,
			cap_type  TEXT    NOT NULL DEFAULT 'gpu',
			vendor    TEXT    NOT NULL DEFAULT '',
			model     TEXT    NOT NULL DEFAULT '',
			vram_mib  INTEGER NOT NULL DEFAULT 0,
			device_index INTEGER NOT NULL DEFAULT 0,
			count     INTEGER NOT NULL DEFAULT 1,
			allocated INTEGER NOT NULL DEFAULT 0,
			props     TEXT    NOT NULL DEFAULT '{}'
		);
		CREATE INDEX IF NOT EXISTS idx_nodecap_node ON virtualserver_node_capabilities(node_id)`,
	},
	{
		version:     4,
		description: "virtualserver_services",
		up: `CREATE TABLE IF NOT EXISTS virtualserver_services (
			id            INTEGER PRIMARY KEY AUTOINCREMENT,
			namespace     TEXT    NOT NULL,
			name          TEXT    NOT NULL,
			manifest_json TEXT    NOT NULL,
			desired_count INTEGER NOT NULL DEFAULT 1,
			state         TEXT    NOT NULL DEFAULT 'declared',
			created_at    INTEGER NOT NULL,
			updated_at    INTEGER NOT NULL,
			UNIQUE(namespace, name)
		);
		CREATE INDEX IF NOT EXISTS idx_svc_ns_state ON virtualserver_services(namespace, state)`,
	},
	{
		version:     5,
		description: "virtualserver_service_versions",
		up: `CREATE TABLE IF NOT EXISTS virtualserver_service_versions (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			service_id  INTEGER NOT NULL REFERENCES virtualserver_services(id),
			version     INTEGER NOT NULL,
			manifest_json TEXT  NOT NULL,
			created_at  INTEGER NOT NULL,
			UNIQUE(service_id, version)
		)`,
	},
	{
		version:     6,
		description: "virtualserver_instances",
		up: `CREATE TABLE IF NOT EXISTS virtualserver_instances (
			id             TEXT    PRIMARY KEY,
			service_id     INTEGER NOT NULL REFERENCES virtualserver_services(id),
			node_id        TEXT    REFERENCES virtualserver_nodes(id),
			vs_path        TEXT    NOT NULL DEFAULT '',
			state          TEXT    NOT NULL DEFAULT 'declared',
			retry_count    INTEGER NOT NULL DEFAULT 0,
			runtime_handle TEXT    NOT NULL DEFAULT '{}',
			created_at     INTEGER NOT NULL,
			updated_at     INTEGER NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_inst_svc   ON virtualserver_instances(service_id);
		CREATE INDEX IF NOT EXISTS idx_inst_node  ON virtualserver_instances(node_id);
		CREATE INDEX IF NOT EXISTS idx_inst_state ON virtualserver_instances(state)`,
	},
	{
		version:     7,
		description: "virtualserver_allocations",
		up: `CREATE TABLE IF NOT EXISTS virtualserver_allocations (
			id               INTEGER PRIMARY KEY AUTOINCREMENT,
			instance_id      TEXT    NOT NULL REFERENCES virtualserver_instances(id) ON DELETE CASCADE,
			node_id          TEXT    NOT NULL REFERENCES virtualserver_nodes(id),
			cpu_threads      INTEGER NOT NULL DEFAULT 0,
			ram_mib          INTEGER NOT NULL DEFAULT 0,
			gpu_device_index INTEGER,
			allocated_at     INTEGER NOT NULL,
			released_at      INTEGER,
			UNIQUE(instance_id)
		);
		CREATE INDEX IF NOT EXISTS idx_alloc_node ON virtualserver_allocations(node_id) WHERE released_at IS NULL;
		CREATE INDEX IF NOT EXISTS idx_alloc_inst ON virtualserver_allocations(instance_id)`,
	},
	{
		version:     8,
		description: "virtualserver_operations",
		up: `CREATE TABLE IF NOT EXISTS virtualserver_operations (
			id           TEXT    PRIMARY KEY,
			instance_id  TEXT    NOT NULL REFERENCES virtualserver_instances(id),
			op_type      TEXT    NOT NULL,
			state        TEXT    NOT NULL DEFAULT 'pending',
			adapter      TEXT    NOT NULL DEFAULT '',
			created_at   INTEGER NOT NULL,
			updated_at   INTEGER NOT NULL,
			completed_at INTEGER
		);
		CREATE INDEX IF NOT EXISTS idx_op_inst  ON virtualserver_operations(instance_id);
		CREATE INDEX IF NOT EXISTS idx_op_state ON virtualserver_operations(state)`,
	},
	{
		version:     9,
		description: "virtualserver_operation_events",
		up: `CREATE TABLE IF NOT EXISTS virtualserver_operation_events (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			operation_id TEXT    NOT NULL REFERENCES virtualserver_operations(id),
			event_type   TEXT    NOT NULL,
			message      TEXT    NOT NULL DEFAULT '',
			payload      TEXT    NOT NULL DEFAULT '{}',
			created_at   INTEGER NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_opev_op ON virtualserver_operation_events(operation_id)`,
	},
	{
		version:     10,
		description: "virtualserver_health_observations",
		up: `CREATE TABLE IF NOT EXISTS virtualserver_health_observations (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			instance_id  TEXT    NOT NULL REFERENCES virtualserver_instances(id),
			node_id      TEXT    NOT NULL DEFAULT '',
			status       TEXT    NOT NULL,
			message      TEXT    NOT NULL DEFAULT '',
			observed_at  INTEGER NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_health_inst ON virtualserver_health_observations(instance_id, observed_at)`,
	},
	{
		version:     11,
		description: "virtualserver_artifact_bindings",
		up: `CREATE TABLE IF NOT EXISTS virtualserver_artifact_bindings (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			service_id   INTEGER NOT NULL REFERENCES virtualserver_services(id),
			info_hash    TEXT    NOT NULL,
			art_type     TEXT    NOT NULL DEFAULT 'ocelot',
			status       TEXT    NOT NULL DEFAULT 'unknown',
			seeder_count INTEGER NOT NULL DEFAULT 0,
			checked_at   INTEGER NOT NULL,
			UNIQUE(service_id)
		);
		CREATE INDEX IF NOT EXISTS idx_artbind_hash ON virtualserver_artifact_bindings(info_hash)`,
	},
	{
		version:     12,
		description: "virtualserver_idempotency_keys",
		up: `CREATE TABLE IF NOT EXISTS virtualserver_idempotency_keys (
			key_hash    TEXT    PRIMARY KEY,
			response    TEXT    NOT NULL,
			status_code INTEGER NOT NULL,
			created_at  INTEGER NOT NULL,
			expires_at  INTEGER NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_idem_expires ON virtualserver_idempotency_keys(expires_at)`,
	},
	{
		version:     13,
		description: "audit_log",
		up: `CREATE TABLE IF NOT EXISTS audit_log (
			id            INTEGER PRIMARY KEY AUTOINCREMENT,
			timestamp     INTEGER NOT NULL,
			action        TEXT    NOT NULL,
			resource_type TEXT    NOT NULL DEFAULT '',
			resource_id   TEXT    NOT NULL DEFAULT '',
			ip_address    TEXT    NOT NULL DEFAULT '',
			success       INTEGER NOT NULL DEFAULT 1,
			error_message TEXT    NOT NULL DEFAULT '',
			metadata      TEXT    NOT NULL DEFAULT '{}'
		);
		CREATE INDEX IF NOT EXISTS idx_audit_ts ON audit_log(timestamp)`,
	},
	{
		version:     14,
		description: "virtualserver_volumes",
		up: `CREATE TABLE IF NOT EXISTS virtualserver_volumes (
			id             TEXT    PRIMARY KEY,
			namespace      TEXT    NOT NULL,
			name           TEXT    NOT NULL,
			manifest_json  TEXT    NOT NULL,
			state          TEXT    NOT NULL DEFAULT 'declared',
			bound_node_id  TEXT    NOT NULL DEFAULT '',
			driver_handle  TEXT    NOT NULL DEFAULT '{}',
			failure_reason TEXT    NOT NULL DEFAULT '',
			created_at     INTEGER NOT NULL,
			updated_at     INTEGER NOT NULL,
			UNIQUE(namespace, name)
		);
		CREATE INDEX IF NOT EXISTS idx_vol_ns_state ON virtualserver_volumes(namespace, state);
		CREATE INDEX IF NOT EXISTS idx_vol_state    ON virtualserver_volumes(state)`,
	},
	{
		version:     15,
		description: "virtualserver_volume_mounts",
		up: `CREATE TABLE IF NOT EXISTS virtualserver_volume_mounts (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			volume_id    TEXT    NOT NULL REFERENCES virtualserver_volumes(id),
			instance_id  TEXT    NOT NULL REFERENCES virtualserver_instances(id),
			target_path  TEXT    NOT NULL,
			read_only    INTEGER NOT NULL DEFAULT 0,
			state        TEXT    NOT NULL DEFAULT 'pending',
			mounted_at   INTEGER,
			unmounted_at INTEGER
		);
		CREATE INDEX IF NOT EXISTS idx_vmount_vol  ON virtualserver_volume_mounts(volume_id);
		CREATE INDEX IF NOT EXISTS idx_vmount_inst ON virtualserver_volume_mounts(instance_id)`,
	},
	{
		version:     16,
		description: "virtualserver_volume_snapshots",
		up: `CREATE TABLE IF NOT EXISTS virtualserver_volume_snapshots (
			id           TEXT    PRIMARY KEY,
			volume_id    TEXT    NOT NULL REFERENCES virtualserver_volumes(id),
			label        TEXT    NOT NULL DEFAULT '',
			state        TEXT    NOT NULL DEFAULT 'pending',
			driver_ref   TEXT    NOT NULL DEFAULT '',
			size_mib     INTEGER NOT NULL DEFAULT 0,
			created_at   INTEGER NOT NULL,
			completed_at INTEGER
		);
		CREATE INDEX IF NOT EXISTS idx_vsnap_vol ON virtualserver_volume_snapshots(volume_id)`,
	},
	{
		version:     17,
		description: "nodes_avail_cpu_threads",
		up:          `ALTER TABLE virtualserver_nodes ADD COLUMN avail_cpu_threads INTEGER NOT NULL DEFAULT 0`,
	},
}
