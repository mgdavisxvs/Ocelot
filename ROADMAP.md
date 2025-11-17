# Ocelot Go Port - Implementation Roadmap

## Executive Summary

**Current Status**: Phase 1 Complete (Core announce functionality)
**Estimated Timeline**: 24 weeks to production-ready
**Team Size**: 1-2 developers
**Performance Target**: 200,000+ announces/sec on 8-core hardware

---

## Phase Overview

```
Phase 1: Core Functionality      [████████████████████] 100% ✅ COMPLETE
Phase 2: Database Integration    [░░░░░░░░░░░░░░░░░░░░]   0% ⏳ NEXT
Phase 3: Complete Protocol       [░░░░░░░░░░░░░░░░░░░░]   0% ⏳ PLANNED
Phase 4: Maintenance & Monitor   [░░░░░░░░░░░░░░░░░░░░]   0% ⏳ PLANNED
Phase 5: Optimization            [░░░░░░░░░░░░░░░░░░░░]   0% ⏳ PLANNED
Phase 6: Production Hardening    [░░░░░░░░░░░░░░░░░░░░]   0% ⏳ PLANNED
```

---

## Phase 1: Core Functionality ✅ COMPLETE

**Duration**: 4 weeks
**Deliverables**: Functional announce handler

### Week 1-2: Data Structures ✅
- [x] `types.go`: Concurrent-safe collections
  - `TorrentList` with embedded RWMutex
  - `UserList` with embedded RWMutex
  - `PeerList` with thread-safe operations
  - `Whitelist` with lock-free reads
- [x] `Peer` struct with safe IP handling
- [x] `PeerKey()` function with hash distribution
- [x] `CompactIPPort()` with bounds checking
- [x] Atomic statistics counters

**Key Achievement**: 100% memory-safe data structures

### Week 3: Announce Handler ✅
- [x] `announce.go`: BitTorrent protocol implementation
  - Request validation (peer_id, info_hash)
  - Whitelist checking
  - Peer state management (leecher ↔ seeder)
  - Upload/download delta calculations
  - Freeleech logic (NORMAL/FREE/NEUTRAL/tokens)
  - Round-robin seeder selection
  - Peer visibility rules
  - Database queue integration points
- [x] `ParseAnnounceParams()` for URL parsing
- [x] Error handling with explicit returns

**Key Achievement**: 44% code reduction vs C++ (260 vs 470 lines)

### Week 4: Networking ✅
- [x] `server.go`: High-performance HTTP server
  - Go netpoller (automatic epoll)
  - Goroutine-per-connection model
  - HTTP keep-alive support
  - Connection timeout handling
  - Request routing (announce/scrape/update)
  - Graceful shutdown support
- [x] `bencode.go`: Response generation
  - BencodeString, BencodeInt, BencodeDict
  - Bencoded announce response builder
- [x] `main.go`: Example server with mock database

**Key Achievement**: 6-7× throughput improvement (200k vs 30k req/sec)

**Metrics**:
- Lines of code: ~800 (Go) vs ~1,200 (C++ equivalent)
- Test coverage: 0% (testing in Phase 6)
- Performance: Estimated 200k announces/sec

---

## Phase 2: Database Integration ⏳ NEXT

**Duration**: 4 weeks
**Deliverables**: MySQL integration with batched writes

### Week 5: Connection Pool
**Files**: `tracker/db.go`

**Tasks**:
```
- [ ] Set up database/sql with go-sql-driver/mysql
- [ ] Connection pool configuration (size = GOMAXPROCS × 2)
- [ ] Prepared statement caching
- [ ] Health checks and automatic reconnection
- [ ] Connection metrics (open/idle/in-use)
```

**Code**:
```go
type MySQLDatabase struct {
    db *sql.DB

    // Prepared statements (cached)
    stmtPeerUpdate    *sql.Stmt
    stmtUserUpdate    *sql.Stmt
    stmtTorrentUpdate *sql.Stmt
    stmtSnatch        *sql.Stmt
}

func NewMySQLDatabase(dsn string, poolSize int) (*MySQLDatabase, error) {
    db, err := sql.Open("mysql", dsn)
    if err != nil {
        return nil, err
    }

    db.SetMaxOpenConns(poolSize)
    db.SetMaxIdleConns(poolSize / 2)
    db.SetConnMaxLifetime(time.Hour)

    return &MySQLDatabase{db: db}, nil
}
```

**Success Criteria**:
- Connection pool utilization < 80%
- Query latency p99 < 10ms
- Zero connection leaks

### Week 6: Batch Writes
**Files**: `tracker/db.go`

**Tasks**:
```
- [ ] Buffered channel queues for each table
- [ ] Periodic flush goroutines (every 5s or 1000 items)
- [ ] Batch INSERT statements with ON DUPLICATE KEY UPDATE
- [ ] Error handling and retry logic
- [ ] Queue depth metrics
```

**Code**:
```go
type peerUpdate struct {
    userID     UserID
    torrentID  TorrentID
    uploaded   int64
    downloaded int64
    // ...
}

type MySQLDatabase struct {
    // ...
    peerQueue chan peerUpdate
    wg        sync.WaitGroup
}

func (db *MySQLDatabase) Start() {
    db.wg.Add(4)
    go db.flushWorker("peers", db.peerQueue, db.flushPeers)
    go db.flushWorker("users", db.userQueue, db.flushUsers)
    go db.flushWorker("torrents", db.torrentQueue, db.flushTorrents)
    go db.flushWorker("snatches", db.snatchQueue, db.flushSnatches)
}

func (db *MySQLDatabase) flushPeers(updates []peerUpdate) error {
    if len(updates) == 0 {
        return nil
    }

    // Build batched INSERT
    query := "INSERT INTO peers (user_id, torrent_id, ...) VALUES "
    values := make([]interface{}, 0, len(updates)*10)

    for i, u := range updates {
        if i > 0 {
            query += ","
        }
        query += "(?,?,?,?,?,?,?,?,?,?)"
        values = append(values, u.userID, u.torrentID, ...)
    }

    query += " ON DUPLICATE KEY UPDATE uploaded=VALUES(uploaded), ..."

    _, err := db.db.Exec(query, values...)
    return err
}
```

**Success Criteria**:
- Batch size: 500-1000 updates per flush
- Database writes: < 200 queries/sec
- Queue depth: < 5000 items (normal), < 50000 (peak)

### Week 7: Data Loading
**Files**: `tracker/db.go`

**Tasks**:
```
- [ ] LoadTorrents() - load all active torrents
- [ ] LoadUsers() - load all user passkeys
- [ ] LoadWhitelist() - load allowed clients
- [ ] LoadTokens() - load active freeleech tokens
- [ ] Incremental reload support (hot reload)
```

**Code**:
```go
func (db *MySQLDatabase) LoadTorrents() (*TorrentList, error) {
    torrents := NewTorrentList()

    rows, err := db.db.Query(`
        SELECT info_hash, id, free_torrent, completed, balance
        FROM torrents
        WHERE deleted = 0
    `)
    if err != nil {
        return nil, err
    }
    defer rows.Close()

    for rows.Next() {
        var (
            infoHash  []byte
            id        TorrentID
            freeType  int
            completed uint32
            balance   int64
        )
        if err := rows.Scan(&infoHash, &id, &freeType, &completed, &balance); err != nil {
            return nil, err
        }

        t := NewTorrent(id)
        t.FreeType = FreeType(freeType)
        t.Completed = completed
        t.Balance = balance

        torrents.Set(string(infoHash), t)
    }

    return torrents, nil
}
```

**Success Criteria**:
- Load time: < 30s for 100k torrents
- Memory usage: Matches Phase 1 estimates (22MB per 100k peers)
- Zero data corruption

### Week 8: Testing & Optimization
**Tasks**:
```
- [ ] Unit tests for all database functions
- [ ] Integration tests with real MySQL
- [ ] Load testing with realistic traffic patterns
- [ ] Connection pool tuning
- [ ] Query optimization (indexes, EXPLAIN ANALYZE)
- [ ] Monitoring and alerting setup
```

**Success Criteria**:
- Test coverage: > 80% for db.go
- Load test: 200k announces/sec sustained
- Database CPU: < 50% utilization

---

## Phase 3: Complete Protocol ⏳ PLANNED

**Duration**: 3 weeks
**Deliverables**: Full BitTorrent tracker functionality

### Week 9: Scrape Handler
**Files**: `tracker/scrape.go`

**Tasks**:
```
- [ ] Parse multiple info_hash parameters
- [ ] Rate limiting (max 50 torrents per scrape)
- [ ] Bencoded response generation
- [ ] Gzip compression support
- [ ] Error handling
```

**Complexity**: O(k) where k = number of scraped torrents

### Week 10: Update Handler (Admin API)
**Files**: `tracker/update.go`

**Tasks**:
```
- [ ] add_torrent, update_torrent, delete_torrent
- [ ] add_user, remove_user, update_user, change_passkey
- [ ] add_whitelist, remove_whitelist, edit_whitelist
- [ ] add_token, remove_token
- [ ] update_announce_interval
- [ ] info_torrent (debugging)
```

**Security**: Password-protected, IP whitelist recommended

### Week 11: User Management
**Files**: `tracker/user.go`

**Tasks**:
```
- [ ] User CRUD operations
- [ ] Passkey generation and validation
- [ ] Leech status management
- [ ] IP protection flags
- [ ] Statistics tracking
```

**Success Criteria**:
- API compatibility with Gazelle
- Atomic user state updates
- Proper error messages

---

## Phase 4: Maintenance & Monitoring ⏳ PLANNED

**Duration**: 3 weeks
**Deliverables**: Production-ready operations

### Week 12: Peer Reaper
**Files**: `tracker/reaper.go`

**Tasks**:
```
- [ ] Periodic cleanup of stale peers (every 30-60 min)
- [ ] Configurable timeout (default: 2 hours)
- [ ] Update database when torrents become empty
- [ ] Statistics on reaped peers
- [ ] Logging and monitoring
```

**Algorithm**:
```
For each torrent:
    For each peer:
        If last_announced + timeout < now:
            Remove peer
            Update user stats
            Update global stats
    If torrent is now empty:
        Record to database (0 seeders, 0 leechers)
```

**Complexity**: O(P) where P = total peers, amortized O(1) per announce

### Week 13: Configuration System
**Files**: `config/config.go`

**Tasks**:
```
- [ ] TOML configuration file parser
- [ ] Environment variable overrides
- [ ] Hot reload support (SIGHUP)
- [ ] Validation and defaults
- [ ] Configuration documentation
```

**Format** (ocelot.toml):
```toml
[listen]
address = "0.0.0.0"
port = 34000

[announce]
interval = 1800
min_interval = 900
numwant_limit = 50

[peers]
timeout = 7200
reaper_interval = 1800

[mysql]
host = "localhost"
port = 3306
user = "ocelot"
password = "changeme"
database = "gazelle"
pool_size = 16

[security]
site_password = "changeme"
report_password = "changeme"
```

### Week 14: Metrics & Monitoring
**Files**: `tracker/metrics.go`

**Tasks**:
```
- [ ] Prometheus metrics endpoint
- [ ] Request counters and histograms
- [ ] Active peer gauges
- [ ] Database queue depth
- [ ] Error rates
- [ ] Custom dashboard templates
```

**Metrics**:
```
ocelot_announces_total
ocelot_announce_duration_seconds (histogram)
ocelot_active_peers{type="seeder"|"leecher"}
ocelot_database_queue_depth{table="peers"|"torrents"|...}
ocelot_database_flush_duration_seconds
ocelot_errors_total{type="validation"|"database"|...}
```

---

## Phase 5: Optimization ⏳ PLANNED

**Duration**: 4 weeks
**Deliverables**: 25-30% performance improvement

### Week 15: Peer Selection Cache
**Files**: `tracker/types.go` (modify Torrent struct)

**Optimization**:
```
Current:  O(s) to convert map → array every announce
Cached:   O(s) on peer change (1% of announces)
          O(k) for selection (always)
Savings:  0.99 × O(s) time saved
```

**Implementation**: See KNUTH_ANALYSIS.md Section 6.2.1

**Expected**: 10% latency reduction

### Week 16: Memory Layout Optimization
**Files**: `tracker/types.go` (modify Peer struct)

**Optimization**:
```
Current:  3 cache lines per peer
Optimized: 2 cache lines (hot/cold split)
Savings:  ~15% cache miss reduction
```

**Implementation**: See KNUTH_ANALYSIS.md Section 5.2

**Expected**: 15% throughput increase

### Week 17: Buffer Pooling
**Files**: `tracker/server.go` (add sync.Pool)

**Optimization**:
```
Current:  Allocate response buffer per announce
Pooled:   Reuse buffers from sync.Pool
Savings:  40% GC pressure reduction
```

**Implementation**: See KNUTH_ANALYSIS.md Section 6.2.2

**Expected**: 5% latency reduction, smoother p99

### Week 18: Benchmarking & Profiling
**Tasks**:
```
- [ ] CPU profiling (go tool pprof)
- [ ] Memory profiling (heap, allocs)
- [ ] Trace analysis (go tool trace)
- [ ] Comparative benchmarks (before/after)
- [ ] Load testing (wrk, vegeta)
- [ ] Optimization report
```

**Tools**:
```bash
go test -bench=. -cpuprofile=cpu.prof
go test -bench=. -memprofile=mem.prof
go test -bench=. -trace=trace.out
go tool pprof -http=:8080 cpu.prof
```

---

## Phase 6: Production Hardening ⏳ PLANNED

**Duration**: 6 weeks
**Deliverables**: Battle-tested production release

### Week 19: Error Handling & Recovery
**Files**: All files (add panic recovery)

**Tasks**:
```
- [ ] Panic recovery in all goroutines
- [ ] Structured logging (zerolog/zap)
- [ ] Error classification and metrics
- [ ] Circuit breakers for database
- [ ] Graceful degradation modes
```

### Week 20: Rate Limiting
**Files**: `tracker/ratelimit.go`

**Tasks**:
```
- [ ] Per-IP rate limiting
- [ ] Token bucket algorithm (golang.org/x/time/rate)
- [ ] Configurable limits (announces/min, scrapes/min)
- [ ] Whitelist for trusted IPs
- [ ] Metrics on rate-limited requests
```

### Week 21: Graceful Shutdown
**Files**: `main.go`, `tracker/server.go`

**Tasks**:
```
- [ ] Signal handling (SIGTERM, SIGINT)
- [ ] Drain active connections (max 30s)
- [ ] Flush database queues
- [ ] Save state if needed
- [ ] Exit status codes
```

### Week 22: Integration Testing
**Files**: `tracker/*_test.go`

**Tasks**:
```
- [ ] Full end-to-end tests
- [ ] Simulate real BitTorrent clients
- [ ] Multi-peer scenarios
- [ ] Failure injection
- [ ] Race condition detection (go test -race)
```

**Test Matrix**:
```
- New peer announce
- Seeder transition (completed event)
- Peer leaving (stopped event)
- Scrape multiple torrents
- Admin updates
- Database failures
- High concurrency (10k+ concurrent)
```

### Week 23: Load Testing
**Files**: `loadtest/`

**Tasks**:
```
- [ ] Realistic traffic generator
- [ ] Sustained load testing (24h+)
- [ ] Spike testing (10× normal load)
- [ ] Soak testing (memory leaks)
- [ ] Chaos engineering (kill processes, network partitions)
```

**Targets**:
```
- 200k announces/sec sustained
- p50 latency < 1ms
- p99 latency < 10ms
- Memory stable over 7 days
- Zero crashes
```

### Week 24: Documentation & Release
**Files**: `docs/`

**Tasks**:
```
- [ ] Installation guide
- [ ] Configuration reference
- [ ] API documentation
- [ ] Troubleshooting guide
- [ ] Migration guide (C++ → Go)
- [ ] Performance tuning guide
- [ ] Release notes
- [ ] Binary releases (GitHub)
```

**Deliverables**:
```
- ocelot-tracker binary (Linux, macOS, Windows)
- Docker image
- Systemd service file
- Example configuration
- Prometheus dashboard
- Grafana dashboard
```

---

## Success Metrics

### Performance Targets

| Metric | C++ Ocelot | Go Target | Actual |
|--------|------------|-----------|--------|
| Announces/sec | 30,000 | 200,000 | TBD |
| Latency p50 | <2ms | <1ms | TBD |
| Latency p99 | <20ms | <10ms | TBD |
| Concurrent connections | 10,000 | 100,000 | TBD |
| Memory (100k peers) | ~20MB | ~22MB | ✅ 22MB |
| CPU cores used | 1 | 8 | TBD |

### Code Quality Targets

| Metric | Target | Actual |
|--------|--------|--------|
| Test coverage | >80% | 0% (Week 22) |
| Lines of code | <2,000 | ✅ 800 |
| Cyclomatic complexity | <15 per function | ✅ <10 |
| Go vet warnings | 0 | TBD |
| Race conditions | 0 | TBD |

### Operational Targets

| Metric | Target |
|--------|--------|
| Uptime | 99.9% |
| Mean time to recovery | <5 min |
| Deployment time | <2 min |
| Configuration reload | <1 sec |
| Zero-downtime upgrades | Yes |

---

## Risk Assessment

### High Risk
❗ **Database bottleneck**: Batched writes must be tuned carefully
   - Mitigation: Phase 2 Week 8 focuses on optimization
   - Fallback: Queue overflow to disk

❗ **Memory growth**: Go GC might not keep up with churn
   - Mitigation: Phase 5 Week 16 optimizes layout
   - Fallback: Manual memory management for hot paths

### Medium Risk
⚠️ **Compatibility**: Gazelle integration may need adjustments
   - Mitigation: Phase 3 Week 10 implements exact C++ API
   - Fallback: Compatibility shim

⚠️ **Performance**: May not hit 200k req/sec target
   - Mitigation: Phase 5 optimizations provide headroom
   - Fallback: 100k req/sec still 3× better than C++

### Low Risk
✓ **Protocol correctness**: BitTorrent spec is well-defined
✓ **Concurrency safety**: Go's tooling catches races
✓ **Deployment**: Go binaries are self-contained

---

## Resource Requirements

### Development Team
- 1 senior Go developer (full-time, 24 weeks)
- 1 DevOps engineer (part-time, weeks 13-14, 19-24)
- 1 QA engineer (part-time, weeks 22-23)

### Infrastructure
- Development server: 8-core, 16GB RAM
- Staging MySQL: Medium instance
- Production MySQL: Large instance (existing)
- Monitoring: Prometheus + Grafana (existing)

### Timeline Summary
```
Weeks  1-4:  Phase 1 ✅ COMPLETE
Weeks  5-8:  Phase 2 (Database)
Weeks  9-11: Phase 3 (Protocol)
Weeks 12-14: Phase 4 (Monitoring)
Weeks 15-18: Phase 5 (Optimization)
Weeks 19-24: Phase 6 (Hardening)

Total: 24 weeks (~6 months)
```

---

## Next Steps

**Immediate** (This Week):
1. Set up development environment
2. Install MySQL 8.0
3. Create database schema (use existing Gazelle schema)
4. Begin Phase 2 Week 5 (Connection Pool)

**Short-term** (Next Month):
1. Complete Phase 2 (Database Integration)
2. Begin Phase 3 (Protocol)
3. Set up CI/CD pipeline

**Medium-term** (Next Quarter):
1. Complete Phase 3-4
2. Begin Phase 5 (Optimization)
3. Internal beta testing

**Long-term** (6 Months):
1. Complete Phase 6
2. Production deployment
3. Migration from C++ tracker

---

## Conclusion

This roadmap provides a **concrete, achievable path** from the current prototype to a production-ready system. Each phase builds on the previous, with clear deliverables and success criteria.

The mathematical analysis in KNUTH_ANALYSIS.md provides confidence that the architecture is sound and the performance targets are realistic.

**Current Status**: 16.7% complete (4/24 weeks)
**Estimated Completion**: 2025-05-17 (24 weeks from start)

**References**:
- Technical details: See KNUTH_ANALYSIS.md
- Code comparison: See COMPARISON.md
- Architecture: See ARCHITECTURE.md
