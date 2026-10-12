# Ocelot (Go Edition)

High-performance BitTorrent tracker rewritten in Go with SQLite WAL sharding, Markov-chain analytics, an in-process EventBus, and a PHP admin panel.

## Quick start

```
go build ./cmd/ocelot          # tracker
go build ./markov/cmd/ocelot-markov   # analytics sidecar
```

## Required environment variables

| Variable | Description |
|----------|-------------|
| `SITE_PASSWORD` | Tracker site password (must not be `changeme`) |
| `REPORT_PASSWORD` | Report endpoint password |
| `DB_DIR` | Path to SQLite shard directory (e.g. `/data/db`) |

## Optional environment variables

### Tracker (`cmd/ocelot`)

| Variable | Default | Description |
|----------|---------|-------------|
| `GAZELLE_URL` | _(disabled)_ | Gazelle callback base URL; enables token-expiry callbacks |
| `GAZELLE_PASSWORD` | `SITE_PASSWORD` | Override password for Gazelle API calls |
| `REDIS_URL` | _(disabled)_ | Redis address in `host:port` form; enables EventBus Redis bridge and SSE |
| `REDIS_PASSWORD` | _(none)_ | Redis AUTH password |
| `SSE_ADDR` | _(disabled)_ | Bind address for SSE hub (e.g. `:8080`); serves `/events` to admin dashboard |
| `METRICS_ADDR` | _(disabled)_ | Bind address for Prometheus metrics endpoint (e.g. `:9090`) |
| `DB_BACKUP_DIR` | _(disabled)_ | Directory for scheduled SQLite `VACUUM INTO` backups (every 6 h) |
| `TLS_CERT_FILE` | _(disabled)_ | PEM certificate file; starts TLS listener on `:34443` |
| `TLS_KEY_FILE` | _(disabled)_ | PEM key file (required with `TLS_CERT_FILE`) |
| `TLS_DOMAIN` | _(disabled)_ | Domain for automatic ACME/Let's Encrypt TLS |
| `OTEL_ENDPOINT` | _(disabled)_ | OTLP HTTP endpoint for distributed tracing (e.g. `http://jaeger:4318`) |
| `OTEL_SAMPLE_RATE` | `0.1` | Trace sampling rate (0.0–1.0) |
| `FREELEECH_THRESHOLD` | `0.75` | Minimum Markov priority score (0–1) to grant freeleech |

### Markov sidecar (`markov/cmd/ocelot-markov`)

| Variable | Default | Description |
|----------|---------|-------------|
| `REDIS_URL` | _(disabled)_ | Redis address; enables EventPublisher to push anomaly/freeleech/interval events to tracker |
| `REDIS_PASSWORD` | _(none)_ | Redis AUTH password |

### Admin panel (`admin/`)

| Variable | Description |
|----------|-------------|
| `ADMIN_USER` | Admin username (default: `admin`) |
| `ADMIN_PASS_HASH` | bcrypt hash of admin password (`php -r "echo password_hash('pass', PASSWORD_BCRYPT);"`) |
| `SITE_PASSWORD` | Tracker API password (must match tracker's `SITE_PASSWORD`) |
| `TRACKER_URL` | Tracker base URL (default: `http://localhost:34000`) |
| `MARKOV_URL` | Markov sidecar base URL (default: `http://localhost:9090`) |
| `REDIS_URL` | Redis URL for SSE streaming to admin dashboard (optional) |
| `REDIS_PASSWORD` | Redis AUTH password (optional) |

## Signals

| Signal | Action |
|--------|--------|
| `SIGHUP` | Reload `ocelot.conf` (hot-reload passwords, limits, intervals) |
| `SIGUSR1` | Reload torrent list, user list, and client whitelist from DB |

---

# Ocelot (original C++ version)

Ocelot is a BitTorrent tracker written in C++ for the [Gazelle](http://whatcd.github.io/Gazelle/) project. It supports requests over TCP and can only track IPv4 peers.

## Ocelot Compile-time Dependencies

* [GCC/G++](http://gcc.gnu.org/) (4.7+ required; 4.8.1+ recommended)
* [Boost](http://www.boost.org/) (1.55.0+ required)
* [libev](http://software.schmorp.de/pkg/libev.html) (required)
* [MySQL++](http://tangentsoft.net/mysql++/) (3.2.0+ required)
* [TCMalloc](http://goog-perftools.sourceforge.net/doc/tcmalloc.html) (optional, but strongly recommended)

## Installation

The [Gazelle installation guides](https://github.com/WhatCD/Gazelle/wiki/Gazelle-installation) include instructions for installing Ocelot as a part of the Gazelle project.

### Standalone Installation

* Create the following tables according to the [Gazelle database schema](https://raw.githubusercontent.com/WhatCD/Gazelle/master/gazelle.sql):
 - `torrents`
 - `users_freeleeches`
 - `users_main`
 - `xbt_client_whitelist`
 - `xbt_files_users`
 - `xbt_snatched`

* Edit `ocelot.conf` to your liking.

* Build Ocelot:

        ./configure
        make
        make install

## Running Ocelot

### Run-time options:

* `-c <path/to/ocelot.conf>` - Path to config file. If unspecified, the current working directory is used.
* `-v` - Print queue status every time a flush is initiated.

### Signals

* `SIGHUP` - Reload config
* `SIGUSR1` - Reload torrent list, user list and client whitelist
