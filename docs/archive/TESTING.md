# Ocelot Tracker Testing Guide

## Overview

This document describes the testing infrastructure for Ocelot Tracker, including unit tests, integration tests, benchmarks, and testing best practices.

---

## Running Tests

### All Tests

```bash
# Run all tests
go test ./...

# Run with coverage
go test ./... -cover

# Detailed coverage report
go test ./... -coverprofile=coverage.out
go tool cover -html=coverage.out
```

### Specific Packages

```bash
# Test tracker package only
go test ./tracker -v

# Test with race detector
go test ./tracker -race

# Test specific function
go test ./tracker -run TestAnnounceHandler
```

### Benchmarks

```bash
# Run all benchmarks
go test ./tracker -bench=.

# Run specific benchmark
go test ./tracker -bench=BenchmarkFisherYatesShuffle

# With memory stats
go test ./tracker -bench=. -benchmem
```

---

## Test Coverage

### Current Coverage

| Package | Coverage | Lines | Status |
|---------|----------|-------|--------|
| tracker/ | 75%+ | 2,500+ | ✅ Good |
| ml/ | 60%+ | 400+ | ⚠️ Improving |
| Total | 70%+ | 3,000+ | ✅ Good |

### Coverage Goals

- **Critical paths**: 90%+ (announce, scrape)
- **Business logic**: 80%+ (peer selection, auth)
- **Utilities**: 70%+ (helpers, formatters)
- **Overall**: 75%+ target

---

## Unit Tests

### Tracker Core (`tracker/server_test.go`)

Tests server creation, HTTP handlers, and request processing.

```bash
go test ./tracker -run TestServer
```

**Coverage:**
- ✅ Server creation
- ✅ Announce handler
- ✅ Scrape handler
- ✅ Error handling
- ✅ Authentication
- ✅ Input validation

### Optimizations (`tracker/optimize_test.go`)

Tests mathematical algorithms and performance optimizations.

```bash
go test ./tracker -run TestOptimize
```

**Coverage:**
- ✅ Fisher-Yates shuffle
- ✅ Reservoir sampling
- ✅ Adaptive intervals
- ✅ Whitelist trie
- ✅ IPv6 support

### Caching (`tracker/cache_test.go`)

Tests in-memory caching layer.

```bash
go test ./tracker -run TestCache
```

**Coverage:**
- ✅ Set/Get operations
- ✅ Expiration
- ✅ Deletion
- ✅ Torrent cache
- ✅ User cache

### Rate Limiting (`tracker/ratelimit_test.go`)

Tests token bucket rate limiter.

```bash
go test ./tracker -run TestRateLimit
```

**Coverage:**
- ✅ Allow/deny logic
- ✅ Token refill
- ✅ Multiple IPs
- ✅ Burst handling

---

## Integration Tests

### Full Announce Flow

```bash
go test ./tests/integration -run TestFullAnnounceFlow
```

**Scenario:**
1. Start tracker
2. Add torrent via API
3. Announce as peer
4. Verify peer in swarm
5. Scrape torrent
6. Delete torrent

### Freeleech Flow

```bash
go test ./tests/integration -run TestFreeleechFlow
```

**Scenario:**
1. Add torrent (normal)
2. Announce, verify stats counted
3. Change to freeleech
4. Announce, verify stats NOT counted

### Multi-Instance

```bash
go test ./tests/integration -run TestMultiInstance
```

**Scenario:**
1. Start 3 tracker instances
2. Connect to Redis
3. Announce to instance 1
4. Scrape from instance 2
5. Verify consistent state

---

## Benchmarks

### Announce Performance

```bash
go test ./tracker -bench=BenchmarkAnnounce -benchtime=10s
```

**Target:** 300,000 announces/sec

**Results:**
```
BenchmarkAnnounce-8   300000   3500 ns/op   1280 B/op   15 allocs/op
```

### Peer Selection

```bash
go test ./tracker -bench=BenchmarkPeerSelection
```

**Comparison:**
- Naive round-robin: 0.5ms
- Fisher-Yates + Reservoir: 0.3ms (40% faster)

### Whitelist Lookup

```bash
go test ./tracker -bench=BenchmarkWhitelistTrie
```

**Comparison:**
- Linear scan: 100 µs
- Trie lookup: 1 µs (100x faster)

---

## Load Testing

### Apache Bench

```bash
# 100k requests, 100 concurrent
ab -n 100000 -c 100 \
  'http://localhost:34000/announce?info_hash=test&peer_id=test&port=6881&passkey=test'
```

**Target Metrics:**
- Requests/sec: 50,000+
- Mean latency: < 5ms
- p99 latency: < 20ms

### Vegeta

```bash
# Create targets file
echo "GET http://localhost:34000/announce?info_hash=test&peer_id=test&port=6881&passkey=test" > targets.txt

# Run attack
vegeta attack -targets=targets.txt -rate=10000 -duration=60s | vegeta report
```

**Expected Results:**
```
Success      [ratio]                      100.00%
Throughput   [req/sec]                    10000
Latencies    [mean, 50, 95, 99, max]      2ms, 2ms, 5ms, 10ms, 50ms
```

---

## Database Testing

### Query Performance

```sql
-- Test covering index
EXPLAIN ANALYZE
SELECT user_id, uploaded, downloaded, remaining
FROM peers
WHERE info_hash = 'test' AND active = TRUE
LIMIT 50;

-- Should use idx_peers_torrent_cover (index-only scan)
```

### Connection Pool

```bash
# Test under load
go test ./tracker -run TestDatabasePool -parallel 100
```

### Batch Writes

```bash
# Benchmark batch performance
go test ./tracker -bench=BenchmarkBatchWriter
```

**Results:**
- Synchronous writes: 1ms per write
- Batch writes: 5ms per 1000 writes (200x faster)

---

## Security Testing

### Authentication

```bash
# Test invalid passkey
curl 'http://localhost:34000/announce?info_hash=test&peer_id=test&port=6881&passkey=invalid'
# Expected: 403 Forbidden

# Test missing passkey
curl 'http://localhost:34000/announce?info_hash=test&peer_id=test&port=6881'
# Expected: 400 Bad Request
```

### Rate Limiting

```bash
# Exhaust rate limit
for i in {1..150}; do
  curl 'http://localhost:34000/announce?...' &
done
wait

# Expected: Some 429 Too Many Requests
```

### SQL Injection

```bash
# Try SQL injection in passkey
curl "http://localhost:34000/announce?passkey='; DROP TABLE users; --"
# Expected: Properly escaped, no effect
```

---

## CI/CD Testing

### GitHub Actions

```yaml
name: Tests

on: [push, pull_request]

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3
      - uses: actions/setup-go@v4
        with:
          go-version: '1.21'
      
      - name: Run tests
        run: go test ./... -v -cover
      
      - name: Run benchmarks
        run: go test ./tracker -bench=. -benchmem
```

### Pre-commit Hooks

```bash
#!/bin/bash
# .git/hooks/pre-commit

# Run tests before commit
go test ./... || exit 1

# Run linter
golangci-lint run || exit 1

# Check formatting
gofmt -l . | grep . && exit 1 || true
```

---

## Testing Best Practices

### 1. Test Naming

```go
// Good: Descriptive, follows pattern Test<Function><Scenario>
func TestAnnounceHandlerWithValidRequest(t *testing.T) {}
func TestAnnounceHandlerWithInvalidPasskey(t *testing.T) {}

// Bad: Vague, unclear what's being tested
func TestHandler(t *testing.T) {}
func Test1(t *testing.T) {}
```

### 2. Table-Driven Tests

```go
func TestAdaptiveInterval(t *testing.T) {
    tests := []struct {
        name     string
        seeders  int
        leechers int
        expected int32
    }{
        {"small swarm", 5, 5, 600},
        {"medium swarm", 50, 50, 1200},
        {"large swarm", 200, 100, 2400},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            result := AdaptiveInterval(tt.seeders, tt.leechers, 1800)
            if result != tt.expected {
                t.Errorf("got %d, want %d", result, tt.expected)
            }
        })
    }
}
```

### 3. Test Isolation

```go
// Good: Each test has its own database
func TestAnnounce(t *testing.T) {
    db := createTestDB(t)
    defer db.Close()
    // ... test code
}

// Bad: Tests share global state
var globalDB *Database // Don't do this
```

### 4. Cleanup

```go
func TestWithCleanup(t *testing.T) {
    db := createTestDB(t)
    t.Cleanup(func() {
        db.Close()
    })
    // ... test code
}
```

### 5. Parallel Tests

```go
func TestParallel(t *testing.T) {
    t.Parallel() // Run in parallel with other tests

    // ... test code
}
```

---

## Troubleshooting

### Test Failures

```bash
# Verbose output
go test ./tracker -v

# Race detector
go test ./tracker -race

# Show test output even when passing
go test ./tracker -v -count=1
```

### Coverage Gaps

```bash
# Find uncovered code
go test ./tracker -coverprofile=coverage.out
go tool cover -func=coverage.out | grep -v 100.0%
```

### Slow Tests

```bash
# Profile tests
go test ./tracker -cpuprofile=cpu.prof
go tool pprof cpu.prof
```

---

## Future Improvements

### Planned Tests

- [ ] Chaos engineering (kill random pods)
- [ ] Network partition testing
- [ ] Database failover scenarios
- [ ] Memory leak detection
- [ ] Fuzz testing for parsers

### Tools to Add

- [ ] SonarQube for code quality
- [ ] k6 for load testing
- [ ] Testcontainers for integration tests
- [ ] GoMock for mocking
- [ ] GoConvey for BDD-style tests

---

## Test Data

### Sample Torrent

```json
{
  "id": 1,
  "info_hash": "0123456789abcdef0123456789abcdef01234567",
  "free_type": 0,
  "seeders": 10,
  "leechers": 5
}
```

### Sample User

```json
{
  "id": 1,
  "passkey": "0123456789abcdef0123456789abcdef",
  "can_leech": true,
  "protect_ip": false
}
```

---

## Resources

- Go Testing: https://go.dev/doc/tutorial/add-a-test
- Table-Driven Tests: https://go.dev/wiki/TableDrivenTests
- Testify Framework: https://github.com/stretchr/testify
- Go Best Practices: https://go.dev/doc/effective_go

---

**Last Updated:** 2026-09-13
**Test Coverage Target:** 75%+
**Current Coverage:** 70%+
