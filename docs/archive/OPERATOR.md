# Ocelot Go Tracker — Operator Manual

## Table of Contents

1. [Architecture Overview](#1-architecture-overview)
2. [System Requirements](#2-system-requirements)
3. [Building](#3-building)
4. [Configuration Reference](#4-configuration-reference)
5. [Running the Tracker](#5-running-the-tracker)
6. [Database Layout](#6-database-layout)
7. [Signal Handling](#7-signal-handling)
8. [Admin HTTP API](#8-admin-http-api)
9. [Freeleech and Token System](#9-freeleech-and-token-system)
10. [Gazelle Integration](#10-gazelle-integration)
11. [Operational Procedures](#11-operational-procedures)
12. [Monitoring and Stats](#12-monitoring-and-stats)
13. [Troubleshooting](#13-troubleshooting)
14. [Security Considerations](#14-security-considerations)

---

## 1. Architecture Overview

Ocelot is a high-performance private BitTorrent tracker designed for use with the
Gazelle web application framework. This Go port replaces the original C++
implementation (libev + MySQL) with Go's runtime netpoller and SQLite.

### Component Map

```
                 ┌─────────────────────────────────────────┐
  BitTorrent     │             Ocelot Tracker               │
  Clients  ────► │  /:passkey/announce   (BEP-3)            │
                 │  /:passkey/scrape     (BEP-48)           │
                 └────────────┬────────────────────────────┘
                              │ HTTP/1.1 raw TCP
                 ┌────────────▼────────────────────────────┐
  Gazelle  ────► │  /:sitepass/update    (admin mutations)  │
  Web App        │  /:sitepass/stats     (JSON metrics)     │
                 │  /:sitepass/torrents  (JSON list)        │
                 │  /:sitepass/peers     (JSON list)        │
                 │  /:sitepass/whitelist (JSON list)        │
                 └────────────┬────────────────────────────┘
                              │
              ┌───────────────┼───────────────────┐
              │               │                   │
    ┌─────────▼──────┐  ┌─────▼──────┐  ┌────────▼──────┐
    │  In-Memory     │  │  SQLite    │  │  Gazelle HTTP │
    │  State         │  │  Shards    │  │  Callbacks    │
    │                │  │  (84 GB)   │  │  (optional)   │
    │  TorrentList   │  │            │  │               │
    │  UserList      │  │  WAL mode  │  │  ExpireToken  │
    │  PeerList(s)   │  │  sharded   │  │               │
    │  Whitelist     │  │  by month  │  └───────────────┘
    └────────────────┘  └────────────┘
              │
    ┌─────────┴─────────────────────┐
    │  Background Goroutines        │
    │  Reaper   — evict stale peers │
    │  Scheduler— WAL checkpoint    │
    └───────────────────────────────┘
```

### Key Design Decisions

| Concern | C++ original | Go port |
|---|---|---|
| I/O model | libev (manual epoll) | Go netpoller (automatic epoll/kqueue) |
| Database | MySQL, persistent connection | SQLite WAL, 84 GB shard rotation |
| Concurrency | pthreads + manual mutexes | goroutines + sync.RWMutex + atomics |
| Peer state | RAM only, lost on restart | RAM primary; SQLite for restart recovery |
| Config | ocelot.conf (same format) | ocelot.conf (same format, same keys) |
| Site comms | HTTP POST to Gazelle | HTTP POST to Gazelle (GazelleSiteComm) |

All peer data (who is seeding what, upload/download deltas, peer IPs) lives
exclusively in RAM for maximum announce throughput. SQLite stores cumulative
statistics and the metadata required to rebuild in-memory state after a restart.

---

## 2. System Requirements

### Minimum

- Linux x86-64 (epoll required for production throughput)
- Go 1.21 or later
- 512 MB RAM (RAM grows with active peer count: ~200 bytes per peer)
- 1 GB disk for database directory (grows with announce volume)

### Recommended Production

- Linux x86-64, kernel 5.10+
- Go 1.21+
- 4 GB RAM for large swarms (100 k+ peers)
- SSD storage for the database directory
- Dedicated non-root user account

### Ports

| Port | Protocol | Purpose |
|---|---|---|
| 34000 (default) | TCP | BitTorrent client announces and admin API |

The tracker serves both client traffic and admin traffic on the same port,
distinguished by passkey vs. site password in the URL path.

---

## 3. Building

### From source

```sh
git clone https://github.com/mgdavisxvs/Ocelot.git
cd Ocelot
go build -o ocelot ./cmd/ocelot/
```

The binary is fully self-contained. No shared libraries, no CGO, no external
runtime dependencies beyond the OS.

### Verify the build

```sh
./ocelot -h
# Usage of ./ocelot:
#   -c string
#         path to config file (default "ocelot.conf")
```

---

## 4. Configuration Reference

The tracker reads a plain-text key=value config file (default: `ocelot.conf`
in the working directory). Comments begin with `#`. Unknown keys are silently
ignored. All values have safe defaults so a missing or empty file starts the
tracker in a functional, conservative state.

### Full annotated example

```ini
# ── Network ───────────────────────────────────────────────────────────────────

# TCP port to listen on. Clients must point to this port.
listen_port = 34000

# Hard limit on concurrent TCP connections accepted. Connections beyond this
# limit are immediately closed. Set to at least max_middlemen.
max_connections = 128

# Soft limit on simultaneous in-flight requests (goroutines). New connections
# are rejected when this is reached. Size to your peak concurrent announce rate.
max_middlemen = 20000

# Read buffer size per connection in bytes. 4096 covers all normal announces.
max_read_buffer = 4096

# Maximum allowed HTTP request size in bytes. Protects against oversized payloads.
max_request_size = 4096

# Seconds to wait for a client to send a complete request before closing.
connection_timeout = 10

# Seconds of HTTP keep-alive idle time. 0 disables keep-alive (close after
# each request). Enable only if your reverse proxy supports it.
keepalive_timeout = 0

# ── Tracker behaviour ─────────────────────────────────────────────────────────

# Seconds between announces that clients should respect. Clients that announce
# faster than this are still accepted, but they waste resources.
# Standard: 1800 (30 min). Aggressive trackers use 3600 (1 hr).
announce_interval = 1800

# Maximum number of peers the tracker returns in a single announce response.
# Clients may request fewer via numwant. Hard capped at this value.
numwant_limit = 50

# Seconds of silence before a peer is considered dead and removed from the
# swarm. Must be > announce_interval to avoid evicting healthy peers.
# Rule of thumb: peers_timeout = announce_interval * 2 + 600.
peers_timeout = 7200

# How often (seconds) the background reaper checks for dead peers.
# Lower values free memory faster; higher values reduce CPU overhead.
reap_peers_interval = 1800

# ── Maintenance ───────────────────────────────────────────────────────────────

# How often (seconds) the scheduler runs WAL checkpoints and checks whether
# the current SQLite shard has exceeded 84 GB and needs rotation.
schedule_interval = 3

# Lifetime (seconds) of deletion reason records. Not actively used in the Go
# port but kept for config compatibility with Gazelle.
del_reason_lifetime = 86400

# Size of the internal request log ring buffer. Not surfaced externally.
request_log_size = 500

# ── Authentication ────────────────────────────────────────────────────────────

# 32-character password for admin API routes (/update, /stats, /torrents, etc.)
# Change this before exposing the tracker to any network.
site_password = 00000000000000000000000000000000

# 32-character password for report endpoints. Currently reserved.
report_password = 00000000000000000000000000000000

# ── Storage ───────────────────────────────────────────────────────────────────

# Directory where SQLite shard files are stored. Created automatically.
# Use an absolute path in production.
db_dir = ./data/db

# If true, the tracker accepts announces but does not write to the database.
# Useful for testing announce logic without polluting stats.
readonly = false

# ── Gazelle integration (optional) ────────────────────────────────────────────

# Full URL of the Gazelle tracker callback endpoint. When set, the tracker
# sends HTTP POST requests to Gazelle when freeleech tokens are consumed.
# Leave blank to disable (token expiry will only be logged locally).
# Example: http://gazelle.example.com/tracker/callback.php
gazelle_url =
```

### Config key quick reference

| Key | Type | Default | Hot-reload (SIGHUP) |
|---|---|---|---|
| `listen_port` | int | 34000 | No (requires restart) |
| `max_connections` | int | 128 | No |
| `max_middlemen` | int | 20000 | No |
| `max_read_buffer` | int | 4096 | No |
| `max_request_size` | int | 4096 | No |
| `connection_timeout` | int (sec) | 10 | No |
| `keepalive_timeout` | int (sec) | 0 | No |
| `announce_interval` | int (sec) | 1800 | **Yes** |
| `numwant_limit` | int | 50 | **Yes** |
| `peers_timeout` | int (sec) | 7200 | **Yes** |
| `reap_peers_interval` | int (sec) | 1800 | No |
| `schedule_interval` | int (sec) | 3 | No |
| `del_reason_lifetime` | int (sec) | 86400 | No |
| `request_log_size` | int | 500 | No |
| `site_password` | string | 00…0 | **Yes** |
| `report_password` | string | 00…0 | **Yes** |
| `db_dir` | path | ./data/db | No |
| `readonly` | bool | false | No |
| `gazelle_url` | URL | (empty) | No |

Fields marked **Yes** in Hot-reload are updated in-memory on SIGHUP without
restarting. All others require a full process restart.

---

## 5. Running the Tracker

### Minimal startup

```sh
# Uses ocelot.conf in the current directory (or built-in defaults if absent)
./ocelot

# Explicit config file path
./ocelot -c /etc/ocelot/ocelot.conf
```

### Startup sequence (internal)

```
1. Parse -c flag → locate config file
2. ParseConfigFile()  → load all key=value pairs; fall back to defaults
3. NewSQLiteShardManager(db_dir)
      → create db_dir if absent
      → open all existing monthly shard .db files (read-only, for history queries)
      → open or create current month's shard: ocelot-YYYY-MM.db
      → apply WAL pragmas
      → run CREATE TABLE IF NOT EXISTS for all 8 tables
      → prepare announce-path statements
4. Loader.LoadAll()
      → load torrents from DB into TorrentList
      → load users from DB into UserList
      → load whitelist from DB into Whitelist
      → load tokens from DB into Torrent.TokenedUsers
      (empty DB = empty state; add via admin API)
5. Select SiteComm: GazelleSiteComm if gazelle_url set, else NoOpSiteComm
6. Reaper.Start()    → background goroutine, runs every reap_peers_interval sec
7. Scheduler.Start() → background goroutine, runs every schedule_interval sec
8. Server.ListenAndServe() → TCP accept loop (goroutine)
9. Block on SIGINT/SIGTERM
```

### Systemd unit (recommended)

```ini
[Unit]
Description=Ocelot BitTorrent Tracker
After=network.target

[Service]
Type=simple
User=ocelot
Group=ocelot
WorkingDirectory=/opt/ocelot
ExecStart=/opt/ocelot/ocelot -c /etc/ocelot/ocelot.conf
Restart=on-failure
RestartSec=5
LimitNOFILE=65536

# Signal shortcuts
ExecReload=/bin/kill -HUP $MAINPID

[Install]
WantedBy=multi-user.target
```

```sh
systemctl daemon-reload
systemctl enable ocelot
systemctl start ocelot

# Reload config after editing ocelot.conf
systemctl reload ocelot        # sends SIGHUP

# Reload torrent/user data from DB after Gazelle sync
kill -USR1 $(systemctl show -p MainPID ocelot | cut -d= -f2)
```

### Open file limit

Each TCP connection consumes one file descriptor. For `max_middlemen = 20000`
you need at least 22000 open files (connections + DB + misc). The systemd unit
above sets `LimitNOFILE=65536`. If running without systemd:

```sh
ulimit -n 65536
./ocelot -c /etc/ocelot/ocelot.conf
```

---

## 6. Database Layout

### Shard naming

SQLite shard files are named `ocelot-YYYY-MM.db` and stored in `db_dir`.
A new shard is created when the current one reaches 84 GB. All historical
shards remain open for aggregate reads (e.g. user total upload/download).

```
data/db/
├── ocelot-2025-01.db        ← historical (read-only queries)
├── ocelot-2025-06.db        ← historical
└── ocelot-2026-09.db        ← current (all writes go here)
```

### Table reference

#### `peers`
One row per (user, torrent) pair. Updated on every announce.

| Column | Type | Description |
|---|---|---|
| user_id | INTEGER PK | User ID |
| torrent_id | INTEGER PK | Torrent ID |
| active | INTEGER | 1 = active, 0 = stopped |
| uploaded | INTEGER | Total bytes uploaded this session |
| downloaded | INTEGER | Total bytes downloaded this session |
| upspeed | INTEGER | Instantaneous upload speed (bytes/sec) |
| downspeed | INTEGER | Instantaneous download speed (bytes/sec) |
| remaining | INTEGER | Bytes left to download |
| corrupt | INTEGER | Corrupt bytes reported |
| timespent | INTEGER | Seconds since first announce |
| announces | INTEGER | Total announce count |
| ip | TEXT | Peer IP address (empty if ProtectIP) |
| peer_id | BLOB | 20-byte BitTorrent peer ID |
| useragent | TEXT | HTTP User-Agent header |
| last_announce | INTEGER | Unix timestamp of last announce |

#### `torrents`
Aggregate stats per torrent ID.

| Column | Type | Description |
|---|---|---|
| id | INTEGER PK | Torrent ID |
| seeders | INTEGER | Current seeder count |
| leechers | INTEGER | Current leecher count |
| snatched | INTEGER | Cumulative snatch count |
| balance | INTEGER | Upload − download delta |
| free_type | INTEGER | 0=Normal 1=Free 2=Neutral |
| last_action | INTEGER | Unix timestamp of last update |

#### `users`
Cumulative transfer totals per user.

| Column | Type | Description |
|---|---|---|
| id | INTEGER PK | User ID |
| uploaded | INTEGER | Cumulative bytes uploaded |
| downloaded | INTEGER | Cumulative bytes downloaded |

#### `snatches`
One row per (user, torrent, time) completion event.

| Column | Type | Description |
|---|---|---|
| user_id | INTEGER PK | User ID |
| torrent_id | INTEGER PK | Torrent ID |
| snatched_time | INTEGER PK | Unix timestamp |
| ip | TEXT | IP at time of snatch |

#### `tokens`
Active per-user freeleech tokens.

| Column | Type | Description |
|---|---|---|
| user_id | INTEGER PK | User ID |
| torrent_id | INTEGER PK | Torrent ID |
| downloaded | INTEGER | Bytes downloaded under this token |

#### `torrent_hashes` *(restart recovery)*
Maps torrent IDs to info_hashes so the in-memory TorrentList can be
rebuilt after a restart.

| Column | Type | Description |
|---|---|---|
| torrent_id | INTEGER PK | Torrent ID |
| info_hash | TEXT UNIQUE | 20-byte info_hash (raw bytes as string) |

#### `user_passkeys` *(restart recovery)*
Stores passkeys and permissions so the UserList survives a restart.

| Column | Type | Description |
|---|---|---|
| user_id | INTEGER PK | User ID |
| passkey | TEXT UNIQUE | 32-character passkey |
| can_leech | INTEGER | 1 = allowed to download |
| protect_ip | INTEGER | 1 = IP not stored in DB |

#### `whitelist` *(peer_id prefix list)*

| Column | Type | Description |
|---|---|---|
| id | INTEGER PK | Auto-increment row ID |
| prefix | TEXT UNIQUE | Peer ID prefix (e.g. `-qB4`) |

### WAL and checkpointing

The tracker uses `PRAGMA journal_mode=WAL` and `PRAGMA synchronous=NORMAL`.
WAL mode allows one writer and multiple concurrent readers. The Scheduler
goroutine runs `PRAGMA wal_checkpoint(TRUNCATE)` every `schedule_interval`
seconds to prevent the WAL file from growing unbounded.

If the WAL file is very large (e.g. after a crash), you can truncate it
manually while the tracker is stopped:

```sh
sqlite3 data/db/ocelot-2026-09.db "PRAGMA wal_checkpoint(TRUNCATE);"
```

### Manual database inspection

```sh
# Connect to the current shard
sqlite3 data/db/ocelot-$(date +%Y-%m).db

# Top 10 torrents by seeder count
SELECT id, seeders, leechers, snatched FROM torrents ORDER BY seeders DESC LIMIT 10;

# Active peers for a torrent
SELECT user_id, ip, remaining, announces, datetime(last_announce,'unixepoch')
FROM peers WHERE torrent_id = 42 AND active = 1;

# User totals across all shards (run per shard and sum externally)
SELECT id, uploaded, downloaded FROM users WHERE id = 1234;
```

---

## 7. Signal Handling

| Signal | Effect |
|---|---|
| `SIGINT` | Graceful shutdown: stop accepting, drain active connections (30 s timeout), close DB |
| `SIGTERM` | Same as SIGINT |
| `SIGHUP` | Hot-reload config: re-parse ocelot.conf, apply mutable fields in-place |
| `SIGUSR1` | Merge-reload state: update torrent metadata, user permissions, whitelist from DB |

### SIGHUP — config hot-reload

Fields updated without restart: `site_password`, `report_password`,
`numwant_limit`, `announce_interval`, `peers_timeout`.

Fields that require restart: `listen_port`, `max_middlemen`, `db_dir`,
`gazelle_url`, `keepalive_timeout`, `reap_peers_interval`.

```sh
# Edit config, then reload
vim /etc/ocelot/ocelot.conf
kill -HUP $(pgrep ocelot)
```

### SIGUSR1 — state merge-reload

Reloads torrent metadata, user permissions, and the whitelist from the
current SQLite shard. **Does not wipe peer lists.** Active peers (seeders
and leechers) remain in memory untouched. Use this after Gazelle has
updated the database to reflect new torrents, banned users, or whitelist
changes.

```sh
kill -USR1 $(pgrep ocelot)
```

**What changes on SIGUSR1:**

- Existing torrent → `Completed`, `Balance`, `FreeType` updated
- New torrent in DB → inserted into TorrentList (empty peer lists)
- Existing user → `CanLeech`, `ProtectIP` atomics updated
- New user in DB → inserted into UserList
- Whitelist → fully replaced from DB
- Tokens → merged into `Torrent.TokenedUsers`

**What does NOT change on SIGUSR1:**

- Peer lists (Seeders/Leechers) — never wiped
- Torrent IDs / info hashes of existing entries
- User passkeys of existing entries (passkey is the map key)

---

## 8. Admin HTTP API

All admin routes use the site password as the passkey segment:

```
http://tracker.example.com:{port}/{site_password}/{action}?{params}
```

All responses are JSON. Error responses have HTTP 200 with a bencoded
`failure reason` body (BitTorrent tracker convention) when called from an
announce context, or JSON `{"status":"error","message":"..."}` from admin
routes.

### POST /update — mutation dispatcher

Dispatches on the `action` query parameter.

#### add_torrent

Registers a new torrent. The torrent is inserted into the in-memory
TorrentList and the `torrent_hashes` table for restart persistence.

```
GET /{sitepass}/update?action=add_torrent&id=42&info_hash=<raw_hash>&free_type=0
```

| Parameter | Required | Description |
|---|---|---|
| `id` | Yes | Torrent ID (unsigned integer) |
| `info_hash` | Yes | 20-byte raw info hash |
| `free_type` | No | 0=Normal (default), 1=Free, 2=Neutral |

#### delete_torrent

Removes a torrent from the in-memory TorrentList. Peers in the swarm stop
being tracked immediately. Does not remove the torrent from the DB.

```
GET /{sitepass}/update?action=delete_torrent&info_hash=<raw_hash>
```

#### update_torrent

Updates a torrent's freeleech status in-place (peers are not affected).

```
GET /{sitepass}/update?action=update_torrent&info_hash=<raw_hash>&free_type=1
```

#### add_user

Registers a new user. Inserts into UserList and `user_passkeys`.

```
GET /{sitepass}/update?action=add_user&id=1234&passkey=<32chars>&can_leech=1&protect_ip=0
```

| Parameter | Required | Description |
|---|---|---|
| `id` | Yes | User ID (unsigned integer) |
| `passkey` | Yes | 32-character alphanumeric passkey |
| `can_leech` | No | 1=allowed (default), 0=seed-only |
| `protect_ip` | No | 1=do not store IP in DB, 0=store (default) |

#### remove_user

Removes a user from UserList. Their active peers are not evicted immediately;
they will fail their next announce when the passkey lookup returns not-found.

```
GET /{sitepass}/update?action=remove_user&passkey=<32chars>
```

#### change_passkey

Re-keys a user entry under a new passkey. Old passkey becomes invalid
immediately. Updates `user_passkeys` in the DB.

```
GET /{sitepass}/update?action=change_passkey&old_passkey=<32chars>&new_passkey=<32chars>
```

#### add_whitelist

Adds a peer_id prefix to the client whitelist. Takes effect immediately.
Persisted to the `whitelist` table.

```
GET /{sitepass}/update?action=add_whitelist&prefix=-qB4
```

BitTorrent client peer_id prefixes are typically 4–8 characters, e.g.:
- `-qB4` — qBittorrent 4.x
- `-DE13` — Deluge 1.3
- `-lt` — libtorrent-based clients

#### remove_whitelist

Removes a peer_id prefix. Peers already in the swarm with that prefix are
not evicted; they fail their next announce if not on any remaining prefix.

```
GET /{sitepass}/update?action=remove_whitelist&prefix=-qB4
```

#### info

Returns current tracker statistics (alias for GET /stats).

```
GET /{sitepass}/update?action=info
```

---

### GET /stats — runtime statistics

```
GET /{sitepass}/stats
```

Response:

```json
{
  "uptime_seconds": 86400,
  "open_connections": 142,
  "opened_connections": 4829103,
  "seeders": 18432,
  "leechers": 3021,
  "requests": 9843201,
  "announcements": 9201044,
  "succ_announcements": 9198777,
  "scrapes": 641157,
  "bytes_read": 1048576000,
  "bytes_written": 2097152000,
  "torrent_count": 50234,
  "user_count": 12891,
  "whitelist_count": 18
}
```

---

### GET /torrents — torrent list

```
GET /{sitepass}/torrents?limit=100
```

| Parameter | Default | Description |
|---|---|---|
| `limit` | 100 | Maximum rows to return |

Response: JSON array of torrent objects. Order is non-deterministic (map
iteration). Use the `id` field to correlate with Gazelle's DB.

```json
[
  {
    "info_hash": "...",
    "id": 42,
    "seeders": 14,
    "leechers": 3,
    "completed": 891,
    "free_type": 0
  }
]
```

---

### GET /peers — peer list for one torrent

```
GET /{sitepass}/peers?info_hash=<raw_hash>&limit=100
```

Response: JSON array of peer objects, seeders listed before leechers.

```json
[
  {
    "user_id": 1234,
    "ip": "203.0.113.44",
    "port": 51413,
    "uploaded": 1073741824,
    "downloaded": 536870912,
    "left": 0,
    "seeder": true
  }
]
```

Note: `ip` is empty string when the user has `protect_ip` set.

---

### GET /whitelist — current whitelist

```
GET /{sitepass}/whitelist
```

Response: JSON array of prefix strings.

```json
["-qB4", "-DE13", "-TR3", "-lt"]
```

---

## 9. Freeleech and Token System

### FreeType values

| Value | Name | Effect |
|---|---|---|
| 0 | Normal | Upload and download both counted toward user ratio |
| 1 | Free | Download not counted; upload still counted |
| 2 | Neutral | Neither upload nor download counted |

FreeType is set per torrent via `add_torrent` or `update_torrent`. It takes
effect on the next announce from any peer on that torrent.

### Per-user freeleech tokens

A token grants one user freeleech on one torrent, overriding the torrent's
FreeType for that user alone. Tokens are stored in the `tokens` table and
loaded into `Torrent.TokenedUsers` on startup and SIGUSR1.

**Token lifecycle:**

```
Gazelle grants token → inserts into tokens table
                     → operator sends SIGUSR1
                     → tracker loads token into Torrent.TokenedUsers

User announces with Left > 0 and a download delta:
    tracker sees user in TokenedUsers
    → sets downloadedChange = 0 (free)
    → calls DB.RecordToken(userID, torrentID, downloadedChange)
    → sets expireToken = true

On completion or next announce with expireToken:
    → SiteComm.ExpireToken(torrentID, userID)
    → deletes token from Torrent.TokenedUsers

Gazelle receives ExpireToken callback:
    → marks token consumed in its own DB
```

If `gazelle_url` is not configured, `ExpireToken` is a no-op (logged only).
Gazelle will not know the token was consumed until the next full sync.

---

## 10. Gazelle Integration

### Workflow

1. Gazelle adds a torrent:
   ```
   GET /tracker/{sitepass}/update?action=add_torrent&id=42&info_hash=...
   ```

2. Gazelle adds a user:
   ```
   GET /tracker/{sitepass}/update?action=add_user&id=1234&passkey=...&can_leech=1
   ```

3. Gazelle changes a user's passkey (e.g. user regenerates it):
   ```
   GET /tracker/{sitepass}/update?action=change_passkey&old_passkey=...&new_passkey=...
   ```

4. Gazelle grants freeleech to a user:
   - Inserts row into `tokens` table in the shared SQLite DB
   - Sends `kill -USR1 $(pgrep ocelot)` (or calls a reload endpoint)

5. Ocelot calls back to Gazelle when a token is consumed:
   ```
   POST gazelle_url
   action=expire_token&torrentid=42&userid=1234&password={site_password}
   ```

### Passkey URL format

Clients should be configured to use:
```
http://tracker.example.com:34000/{32-char-passkey}/announce
```

Passkeys must be exactly 32 characters. Longer or shorter passkeys return
`Malformed announce`.

---

## 11. Operational Procedures

### Adding a torrent at runtime

```sh
SITE_PASS="your_site_password"
TRACKER="http://localhost:34000"
INFO_HASH="$(printf '...' | xxd -p)"  # raw info hash

curl "${TRACKER}/${SITE_PASS}/update?action=add_torrent&id=42&info_hash=${INFO_HASH}&free_type=0"
# {"status":"ok","message":"torrent added"}
```

### Banning a user

```sh
curl "${TRACKER}/${SITE_PASS}/update?action=remove_user&passkey=abc123...abc123"
# {"status":"ok","message":"user removed"}
```

The user's next announce will return `Passkey not found`. Their peers
remain in swarms until reaped by the Reaper goroutine.

### Rotating config after security incident

If `site_password` is compromised:

1. Edit `ocelot.conf`: change `site_password`
2. `kill -HUP $(pgrep ocelot)` — takes effect immediately
3. Update the password in Gazelle's tracker settings
4. Also update Gazelle's site password for callback auth

### Graceful restart (zero-peer-loss)

Because all peer state is in RAM, a full process restart loses all active
peer data. To minimise impact:

1. Wait for a low-traffic window (e.g. 03:00 local)
2. `systemctl stop ocelot` — drains connections (30 s timeout)
3. Replace the binary
4. `systemctl start ocelot` — Loader.LoadAll() restores torrent/user data
5. Peers will re-announce within one announce interval (default 30 min)
   and rebuild the swarm naturally

### Adding a new client to the whitelist

```sh
# Allow qBittorrent 5.x
curl "${TRACKER}/${SITE_PASS}/update?action=add_whitelist&prefix=-qB5"

# Verify
curl "${TRACKER}/${SITE_PASS}/whitelist"
```

### Disabling the whitelist (allow all clients)

The whitelist is allow-all when empty. To disable it:

```sh
# Remove all entries one by one, or truncate the table and reload
sqlite3 data/db/ocelot-$(date +%Y-%m).db "DELETE FROM whitelist;"
kill -USR1 $(pgrep ocelot)
```

---

## 12. Monitoring and Stats

### Key metrics to watch

| Metric | Warning threshold | Action |
|---|---|---|
| `open_connections` | > 80% of `max_middlemen` | Increase `max_middlemen`, raise `LimitNOFILE` |
| `succ_announcements` / `announcements` ratio | < 0.95 | Check error logs for common failures |
| `seeders` + `leechers` | Sudden drop | Check for reaper misconfiguration or restart |
| DB shard file size | > 70 GB | Verify `schedule_interval` and `CheckRotation` |
| WAL file size (`ocelot-YYYY-MM.db-wal`) | > 1 GB | Force checkpoint: `kill -USR1 $(pgrep ocelot)` or manually |

### Polling the stats endpoint

```sh
watch -n 30 "curl -s http://localhost:34000/${SITE_PASS}/stats | python3 -m json.tool"
```

### Prometheus integration (manual)

The `/stats` endpoint returns flat JSON suitable for scraping. A minimal
Prometheus exporter shell script:

```sh
#!/bin/sh
STATS=$(curl -s "http://localhost:34000/${SITE_PASS}/stats")
echo "ocelot_seeders $(echo $STATS | jq .seeders)"
echo "ocelot_leechers $(echo $STATS | jq .leechers)"
echo "ocelot_announcements_total $(echo $STATS | jq .announcements)"
echo "ocelot_open_connections $(echo $STATS | jq .open_connections)"
```

---

## 13. Troubleshooting

### Tracker returns "unregistered torrent"

The info_hash is not in the TorrentList. Either:
- The torrent was never added via `add_torrent`
- The tracker was restarted and `torrent_hashes` table is empty
- The info_hash encoding differs (URL-encoded vs raw bytes)

Fix: `add_torrent` via admin API or send SIGUSR1 after populating the DB.

### Tracker returns "your client is not on the whitelist"

The client's peer_id prefix is not in the whitelist, or the whitelist
is non-empty and the client is not listed.

Fix: `add_whitelist` for the client's prefix, or empty the whitelist to
allow all clients.

### Tracker returns "access denied, leeching forbidden"

The user's `can_leech` flag is false. Fix via:

```sh
# Re-add the user with can_leech=1
curl "${TRACKER}/${SITE_PASS}/update?action=add_user&id=1234&passkey=...&can_leech=1"
```

Or update the `user_passkeys` table directly and send SIGUSR1.

### High announce failure rate

Check for these causes in order:
1. Whitelist too restrictive — peers using unapproved clients
2. Torrents not registered — Gazelle/tracker sync lag
3. Users not registered — passkey mismatch
4. Compact announce rejected — client does not support BEP-23 (rare)

### Database "no space left on device"

The `db_dir` filesystem is full. The 84 GB shard rotation prevents any single
file from exceeding that limit, but the total dataset can still fill the disk.

Fix:
1. Free disk space or expand the filesystem
2. If old shards are no longer needed for reporting, archive or delete them:
   ```sh
   # Stop tracker, archive, restart
   systemctl stop ocelot
   gzip -9 data/db/ocelot-2025-01.db
   systemctl start ocelot
   ```
   Note: the tracker will not open compressed files; historical queries for
   that month's shard will return zero.

### WAL file growing without bound

If `schedule_interval` is large or the Scheduler goroutine panicked, the WAL
can grow to several gigabytes. Fix while the tracker is stopped:

```sh
sqlite3 data/db/ocelot-$(date +%Y-%m).db "PRAGMA wal_checkpoint(TRUNCATE);"
```

### Peers not appearing in swarm after restart

Peer state lives only in RAM and is not persisted. After a restart, peers
must re-announce before they appear. With `announce_interval = 1800`, the
swarm fully rebuilds within 30 minutes under normal load.

---

## 14. Security Considerations

### Change default passwords immediately

The default `site_password` and `report_password` are 32 zeros. Any host
with network access to the tracker port can call admin routes with the
default. Change both before exposing the port to any network:

```ini
site_password  = <32 random hex characters>
report_password = <32 random hex characters>
```

### Firewall the tracker port

The tracker serves both client traffic and admin traffic on the same port.
If Gazelle and Ocelot run on the same host, firewall the tracker port so
only localhost and Gazelle's IP can reach admin routes.

```sh
# Allow BitTorrent clients from anywhere
ufw allow 34000/tcp

# Restrict admin routes at the application level via site_password
# (no port-level distinction is possible without a reverse proxy)
```

### Reverse proxy considerations

If running behind nginx or another reverse proxy, ensure:
- `X-Forwarded-For` is set correctly — the tracker uses it for peer IP
- The proxy passes raw binary info_hash values without re-encoding
- `proxy_pass` is configured for the raw TCP port (not HTTP rewrite)

### IP protection

Users with `protect_ip = 1` have their IP address omitted from the `peers`
table in the DB. Their IP is still used in-memory for compact peer list
construction but is never logged to disk.

### Peer ID whitelist

Run with a non-empty whitelist to restrict the tracker to known, well-behaved
BitTorrent clients. This prevents automated scrapers and custom clients from
joining swarms. Common prefixes for private tracker use:

```
-qB4  qBittorrent 4.x
-qB5  qBittorrent 5.x
-DE13 Deluge 1.3
-DE20 Deluge 2.0
-TR3  Transmission 3.x
-TR4  Transmission 4.x
-lt   libtorrent (various)
```
