-- compute.db — Ocelot compute plane schema
-- SQLite WAL mode. Never merge with tracker.db.
-- GUC ruling 2026-09-22: two independent databases, zero cross-domain FKs.

PRAGMA journal_mode = WAL;
PRAGMA foreign_keys = ON;
PRAGMA synchronous = NORMAL;

-- ─── ORGANISATION HIERARCHY ──────────────────────────────────────────────────

CREATE TABLE IF NOT EXISTS orgs (
    id           TEXT    PRIMARY KEY,   -- UUID v4
    name         TEXT    NOT NULL UNIQUE,
    created_at   INTEGER NOT NULL       -- unix epoch ms
);

CREATE TABLE IF NOT EXISTS projects (
    id           TEXT    PRIMARY KEY,
    org_id       TEXT    NOT NULL REFERENCES orgs(id),
    name         TEXT    NOT NULL,
    created_at   INTEGER NOT NULL,
    UNIQUE (org_id, name)
);

CREATE TABLE IF NOT EXISTS accounts (
    id           TEXT    PRIMARY KEY,
    org_id       TEXT    NOT NULL REFERENCES orgs(id),
    project_id   TEXT    REFERENCES projects(id),
    username     TEXT    NOT NULL UNIQUE,
    role         TEXT    NOT NULL CHECK (role IN ('operator','project','user','service','auditor')),
    secret_hash  TEXT    NOT NULL,      -- bcrypt of password or HMAC secret
    created_at   INTEGER NOT NULL,
    deleted_at   INTEGER                -- NULL = active
);

-- ─── NODE REGISTRY ───────────────────────────────────────────────────────────

CREATE TABLE IF NOT EXISTS nodes (
    id               TEXT    PRIMARY KEY,   -- UUID v4, assigned at registration
    hostname         TEXT    NOT NULL UNIQUE,
    display_name     TEXT,
    arch             TEXT    NOT NULL CHECK (arch IN ('amd64','arm64')),
    cpu_cores        INTEGER NOT NULL CHECK (cpu_cores > 0),
    ram_mb           INTEGER NOT NULL CHECK (ram_mb > 0),
    storage_gb       INTEGER NOT NULL DEFAULT 0,
    labels           TEXT    NOT NULL DEFAULT '{}',  -- JSON key-value map
    status           TEXT    NOT NULL DEFAULT 'joining'
                     CHECK (status IN ('joining','ready','draining','busy','degraded','lost','dead')),
    last_heartbeat   INTEGER,           -- unix epoch ms
    agent_version    TEXT,
    node_secret_hash TEXT    NOT NULL,  -- SHA-256 hex of the HMAC secret
    created_at       INTEGER NOT NULL,
    drain_at         INTEGER            -- NULL = not draining
);

CREATE TABLE IF NOT EXISTS node_gpus (
    id             TEXT    PRIMARY KEY,  -- UUID v4
    node_id        TEXT    NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    device_index   INTEGER NOT NULL CHECK (device_index >= 0),
    model          TEXT    NOT NULL,
    vram_mb        INTEGER NOT NULL CHECK (vram_mb > 0),
    cuda_cap       TEXT,
    health         TEXT    NOT NULL DEFAULT 'ready'
                   CHECK (health IN ('ready','degraded','failed')),
    vram_reserved  INTEGER NOT NULL DEFAULT 0 CHECK (vram_reserved >= 0),
    workload_id    TEXT,                 -- NULL if free; FK enforced at app layer
    UNIQUE (node_id, device_index)
);

CREATE TABLE IF NOT EXISTS node_economics (
    node_id             TEXT    PRIMARY KEY REFERENCES nodes(id) ON DELETE CASCADE,
    acquisition_cost    REAL    NOT NULL DEFAULT 0.0,   -- USD
    monthly_power_est   REAL    NOT NULL DEFAULT 0.0,   -- USD/month
    depreciation_yrs    INTEGER NOT NULL DEFAULT 5,
    useful_hours        INTEGER NOT NULL DEFAULT 0,     -- cumulative
    compute_value_usd   REAL    NOT NULL DEFAULT 0.0    -- cumulative credited
);

-- ─── ARTIFACT REGISTRY ───────────────────────────────────────────────────────

CREATE TABLE IF NOT EXISTS artifacts (
    infohash     TEXT    PRIMARY KEY,   -- SHA-256 hex of content
    type         TEXT    NOT NULL CHECK (type IN ('binary','container','model','dataset','archive','vm_image','script')),
    name         TEXT    NOT NULL,
    version      TEXT    NOT NULL DEFAULT 'unversioned',
    arch         TEXT    NOT NULL DEFAULT 'any' CHECK (arch IN ('amd64','arm64','any')),
    size_bytes   INTEGER NOT NULL DEFAULT 0,
    entrypoint   TEXT,                  -- NULL for dataset/archive types
    dependencies TEXT    NOT NULL DEFAULT '[]',  -- JSON array of infohash strings
    signature    TEXT,                  -- Ed25519 hex signature (nullable until Phase 3)
    publisher    TEXT,
    manifest     TEXT    NOT NULL DEFAULT '{}',  -- JSON type-specific extras
    created_at   INTEGER NOT NULL
);

-- ─── VOLUMES ─────────────────────────────────────────────────────────────────

CREATE TABLE IF NOT EXISTS volumes (
    id           TEXT    PRIMARY KEY,   -- UUID v4
    org_id       TEXT    NOT NULL REFERENCES orgs(id),
    project_id   TEXT    REFERENCES projects(id),
    name         TEXT    NOT NULL,
    driver       TEXT    NOT NULL CHECK (driver IN ('local','artifact','nfs')),
    source_path  TEXT,                  -- driver-specific; NULL for artifact volumes
    artifact_ref TEXT,                  -- infohash; NULL for non-artifact volumes
    read_only    INTEGER NOT NULL DEFAULT 0,
    size_gb      INTEGER NOT NULL DEFAULT 0,
    created_at   INTEGER NOT NULL,
    deleted_at   INTEGER
);

-- ─── WORKLOAD QUEUE ──────────────────────────────────────────────────────────

CREATE TABLE IF NOT EXISTS workloads (
    id            TEXT    PRIMARY KEY,   -- UUID v4
    org_id        TEXT    NOT NULL REFERENCES orgs(id),
    project_id    TEXT    REFERENCES projects(id),
    account_id    TEXT    REFERENCES accounts(id),
    name          TEXT    NOT NULL,
    status        TEXT    NOT NULL DEFAULT 'submitted'
                  CHECK (status IN ('submitted','queued','scheduling','reserved','dispatched',
                                    'running','checkpointing','stopping',
                                    'completed','failed','timed_out','cancelled')),
    priority      INTEGER NOT NULL DEFAULT 50 CHECK (priority BETWEEN 0 AND 100),
    manifest      TEXT    NOT NULL,      -- JSON WorkloadManifest
    node_id       TEXT    REFERENCES nodes(id),  -- NULL until scheduled
    submitted_at  INTEGER NOT NULL,
    deadline      INTEGER,               -- NULL = no deadline
    queued_at     INTEGER,
    scheduled_at  INTEGER,
    started_at    INTEGER,
    finished_at   INTEGER,
    exit_code     INTEGER,
    failure_msg   TEXT,
    retry_count   INTEGER NOT NULL DEFAULT 0,
    max_retries   INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_workloads_status_priority
    ON workloads (status, priority DESC, queued_at ASC)
    WHERE status IN ('queued','submitted');

CREATE INDEX IF NOT EXISTS idx_workloads_node
    ON workloads (node_id, status)
    WHERE node_id IS NOT NULL;

-- ─── RESOURCE RESERVATIONS ───────────────────────────────────────────────────
-- Atomically tracks reserved resources per node. All writes MUST use
-- BEGIN IMMEDIATE transactions to prevent double-booking.

CREATE TABLE IF NOT EXISTS reservations (
    id           TEXT    PRIMARY KEY,   -- UUID v4
    node_id      TEXT    NOT NULL REFERENCES nodes(id),
    workload_id  TEXT    NOT NULL REFERENCES workloads(id),
    cpu_mcores   INTEGER NOT NULL DEFAULT 0,  -- millicores
    ram_mb       INTEGER NOT NULL DEFAULT 0,
    gpu_id       TEXT    REFERENCES node_gpus(id),  -- NULL if no GPU
    gpu_vram_mb  INTEGER NOT NULL DEFAULT 0,
    state        TEXT    NOT NULL DEFAULT 'reserved'
                 CHECK (state IN ('reserved','allocated','running','released')),
    created_at   INTEGER NOT NULL,
    released_at  INTEGER
);

CREATE INDEX IF NOT EXISTS idx_reservations_node_active
    ON reservations (node_id, state)
    WHERE state IN ('reserved','allocated','running');

-- ─── SERVICE REGISTRY ────────────────────────────────────────────────────────

CREATE TABLE IF NOT EXISTS services (
    workload_id   TEXT    NOT NULL REFERENCES workloads(id) ON DELETE CASCADE,
    service_name  TEXT    NOT NULL,
    node_id       TEXT    NOT NULL REFERENCES nodes(id),
    ip            TEXT    NOT NULL,
    port          INTEGER NOT NULL CHECK (port BETWEEN 1 AND 65535),
    health        TEXT    NOT NULL DEFAULT 'unknown'
                  CHECK (health IN ('unknown','healthy','unhealthy')),
    updated_at    INTEGER NOT NULL,
    PRIMARY KEY (workload_id, service_name)
);

-- ─── EVENT JOURNAL ───────────────────────────────────────────────────────────

CREATE TABLE IF NOT EXISTS events (
    id           TEXT    PRIMARY KEY,   -- UUID v4
    ts           INTEGER NOT NULL,      -- unix epoch ms
    org_id       TEXT    NOT NULL,      -- no FK: events outlive orgs
    actor        TEXT    NOT NULL,      -- node_id | account_id | "system"
    kind         TEXT    NOT NULL,
    subject_type TEXT    NOT NULL CHECK (subject_type IN ('node','workload','artifact','volume','reservation')),
    subject_id   TEXT    NOT NULL,
    payload      TEXT    NOT NULL DEFAULT '{}'  -- JSON
);

CREATE INDEX IF NOT EXISTS idx_events_ts      ON events (ts DESC);
CREATE INDEX IF NOT EXISTS idx_events_subject ON events (subject_type, subject_id, ts DESC);
CREATE INDEX IF NOT EXISTS idx_events_org     ON events (org_id, ts DESC);

-- Valid kind values (application-enforced, not a CHECK constraint to allow
-- future extension without schema migration):
--   node.joined, node.lost, node.drained, node.recovered
--   workload.queued, workload.scheduled, workload.started,
--   workload.checkpoint, workload.failed, workload.completed,
--   workload.cancelled, workload.timed_out, workload.requeued
--   artifact.fetched, artifact.verified, artifact.evicted
--   reservation.created, reservation.released, reservation.expired
--   volume.mounted, volume.unmounted
--   credits.charged, credits.refunded

-- ─── TELEMETRY (hot tier — last 30 days) ─────────────────────────────────────

CREATE TABLE IF NOT EXISTS workload_metrics (
    workload_id        TEXT    NOT NULL REFERENCES workloads(id) ON DELETE CASCADE,
    ts                 INTEGER NOT NULL,        -- unix epoch ms, interval start
    interval_sec       INTEGER NOT NULL,
    cpu_millicore_sec  INTEGER NOT NULL DEFAULT 0,
    ram_mb_sec         INTEGER NOT NULL DEFAULT 0,
    gpu_vram_mb_sec    INTEGER NOT NULL DEFAULT 0,
    bytes_read         INTEGER NOT NULL DEFAULT 0,
    bytes_written      INTEGER NOT NULL DEFAULT 0,
    net_bytes_in       INTEGER NOT NULL DEFAULT 0,
    net_bytes_out      INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (workload_id, ts)
);

CREATE INDEX IF NOT EXISTS idx_metrics_ts ON workload_metrics (ts DESC);

CREATE TABLE IF NOT EXISTS node_metrics (
    node_id            TEXT    NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    ts                 INTEGER NOT NULL,
    interval_sec       INTEGER NOT NULL,
    cpu_millicore_sec  INTEGER NOT NULL DEFAULT 0,
    ram_mb_sec         INTEGER NOT NULL DEFAULT 0,
    watts_sec          REAL    NOT NULL DEFAULT 0.0,
    PRIMARY KEY (node_id, ts)
);

-- ─── SCHEMA VERSION ──────────────────────────────────────────────────────────

CREATE TABLE IF NOT EXISTS schema_version (
    version    INTEGER PRIMARY KEY,
    applied_at INTEGER NOT NULL
);

INSERT OR IGNORE INTO schema_version (version, applied_at) VALUES (1, strftime('%s','now') * 1000);
