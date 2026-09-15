# Ocelot Tracker Deployment Guide

## Table of Contents
1. [Quick Start](#quick-start)
2. [Docker Deployment](#docker-deployment)
3. [Kubernetes Deployment](#kubernetes-deployment)
4. [Production Configuration](#production-configuration)
5. [Monitoring Setup](#monitoring-setup)
6. [Security Hardening](#security-hardening)

---

## Quick Start

### Build from Source

```bash
# Clone repository
git clone https://github.com/mgdavisxvs/Ocelot.git
cd Ocelot

# Install dependencies
go mod download

# Build binary
go build -o ocelot-tracker .

# Run
./ocelot-tracker
```

---

## Docker Deployment

### Single Instance

```bash
# Build image
docker build -t ocelot-tracker:latest .

# Run container
docker run -d \
  -p 34000:34000 \
  -p 9090:9090 \
  -v $(pwd)/data:/data \
  --name ocelot-tracker \
  ocelot-tracker:latest
```

### Docker Compose (Full Stack)

```bash
# Start all services
docker-compose up -d

# View logs
docker-compose logs -f tracker

# Scale trackers
docker-compose up -d --scale tracker=5

# Stop all services
docker-compose down
```

**Services Included:**
- Ocelot Tracker (3 replicas)
- Redis (shared state)
- PostgreSQL (persistent storage)
- Nginx (load balancer)
- Prometheus (metrics)
- Grafana (dashboards)

---

## Kubernetes Deployment

### Prerequisites

- Kubernetes cluster (1.25+)
- kubectl configured
- Helm 3+ (optional)

### Deploy

```bash
# Create namespace
kubectl create namespace ocelot

# Create secrets
kubectl create secret generic postgres-credentials \
  --from-literal=username=tracker \
  --from-literal=password=CHANGE_ME \
  -n ocelot

kubectl create secret generic ocelot-secrets \
  --from-literal=jwt-secret=$(openssl rand -base64 32) \
  --from-literal=site-password=CHANGE_ME \
  -n ocelot

# Deploy PostgreSQL
kubectl apply -f k8s/postgres.yaml -n ocelot

# Deploy Redis
kubectl apply -f k8s/redis.yaml -n ocelot

# Deploy Tracker
kubectl apply -f k8s/deployment.yaml -n ocelot

# Verify deployment
kubectl get pods -n ocelot
kubectl get svc -n ocelot
```

### Access Services

```bash
# Get LoadBalancer IP
kubectl get svc ocelot-tracker -n ocelot

# Port forward for local access
kubectl port-forward svc/ocelot-tracker 34000:80 -n ocelot
```

### Scaling

```bash
# Manual scaling
kubectl scale deployment ocelot-tracker --replicas=10 -n ocelot

# Auto-scaling is configured via HPA (3-20 replicas)
kubectl get hpa ocelot-tracker-hpa -n ocelot
```

---

## Production Configuration

### Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `LOG_LEVEL` | Logging level (debug/info/warn/error) | `info` |
| `REDIS_ADDR` | Redis connection address | `localhost:6379` |
| `POSTGRES_HOST` | PostgreSQL host | `localhost` |
| `POSTGRES_PORT` | PostgreSQL port | `5432` |
| `POSTGRES_USER` | Database username | `tracker` |
| `POSTGRES_PASSWORD` | Database password | - |
| `POSTGRES_DB` | Database name | `ocelot` |
| `JWT_SECRET` | JWT signing secret (32+ bytes) | - |
| `METRICS_PORT` | Prometheus metrics port | `9090` |

### Database Configuration

#### PostgreSQL (Production)

```sql
-- Create database
CREATE DATABASE ocelot;

-- Create user
CREATE USER tracker WITH PASSWORD 'strong_password_here';

-- Grant permissions
GRANT ALL PRIVILEGES ON DATABASE ocelot TO tracker;
```

#### Connection Pooling

```go
// In tracker code
db.SetMaxOpenConns(25)
db.SetMaxIdleConns(5)
db.SetConnMaxLifetime(5 * time.Minute)
```

### TLS/HTTPS Setup

#### Let's Encrypt (Auto)

```bash
# Enable auto TLS
./ocelot-tracker --auto-tls --domain tracker.example.com
```

#### Manual Certificates

```bash
# Generate self-signed cert (development)
openssl req -x509 -newkey rsa:4096 -keyout key.pem -out cert.pem -days 365 -nodes

# Run with TLS
./ocelot-tracker --tls-cert cert.pem --tls-key key.pem
```

---

## Monitoring Setup

### Prometheus

Metrics exposed at `:9090/metrics`

**Key Metrics:**
- `ocelot_announces_total` - Total announces
- `ocelot_announce_duration_seconds` - Announce latency
- `ocelot_active_peers` - Active peer count
- `ocelot_active_torrents` - Active torrent count
- `ocelot_db_query_duration_seconds` - Database query latency
- `ocelot_http_requests_total` - HTTP request count

### Grafana Dashboards

```bash
# Access Grafana
http://localhost:3000

# Default credentials
Username: admin
Password: admin
```

**Pre-configured Dashboards:**
1. Tracker Overview
2. Performance Metrics
3. Database Health
4. Error Rates

### Alerts

Example Prometheus alerting rules:

```yaml
groups:
  - name: ocelot_alerts
    rules:
      - alert: HighErrorRate
        expr: rate(ocelot_http_requests_total{status="error"}[5m]) > 0.05
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "High error rate detected"

      - alert: DatabaseDown
        expr: up{job="postgres"} == 0
        for: 1m
        labels:
          severity: critical
        annotations:
          summary: "Database is down"
```

---

## Security Hardening

### Authentication

#### Generate API Key

```bash
# Via admin panel
curl -X POST http://localhost:34000/changeme/api-keys \
  -H "Content-Type: application/json" \
  -d '{"user_id": 1, "permissions": ["read", "write"]}'
```

#### Use JWT Tokens

```go
// Generate token
token, _ := GenerateToken(userID, "admin", config)

// Use in requests
curl -H "Authorization: Bearer $token" http://localhost:34000/stats
```

### Rate Limiting

Default limits:
- Announce: 100 req/sec, burst 200
- Scrape: 50 req/sec, burst 100

Configure in `nginx.conf` or via code.

### Firewall Rules

```bash
# Allow only tracker ports
ufw allow 34000/tcp  # Tracker
ufw allow 9090/tcp   # Metrics (internal only)
ufw deny 5432/tcp    # PostgreSQL (internal only)
ufw deny 6379/tcp    # Redis (internal only)
```

### Audit Logging

All admin actions are logged to `audit_log` table:

```sql
SELECT timestamp, user_id, action, resource_type, success
FROM audit_log
ORDER BY timestamp DESC
LIMIT 100;
```

---

## Performance Tuning

### OS Limits

```bash
# /etc/sysctl.conf
net.core.somaxconn = 65535
net.ipv4.tcp_max_syn_backlog = 8192
net.ipv4.tcp_fin_timeout = 30
fs.file-max = 100000

# Apply
sysctl -p
```

### Go Runtime

```bash
# Set GOMAXPROCS (default: number of CPUs)
export GOMAXPROCS=8

# Increase GC frequency for high-memory workloads
export GOGC=50
```

### Database Optimization

```sql
-- Create indexes
CREATE INDEX idx_peers_torrent_cover ON peers(
    info_hash, user_id, last_announce, uploaded, downloaded, remaining
) WHERE active = TRUE;

-- Vacuum regularly
VACUUM ANALYZE;
```

---

## Backup & Recovery

### Database Backup

```bash
# PostgreSQL
pg_dump -U tracker ocelot > backup.sql

# Restore
psql -U tracker ocelot < backup.sql
```

### Redis Backup

```bash
# Manual save
redis-cli SAVE

# Copy RDB file
cp /data/dump.rdb /backup/
```

---

## Troubleshooting

### Common Issues

**Issue: High memory usage**
```bash
# Check memory stats
docker stats ocelot-tracker

# Reduce GOGC
export GOGC=50
```

**Issue: Database connection pool exhausted**
```go
// Increase pool size
db.SetMaxOpenConns(50)
```

**Issue: Rate limiting too aggressive**
```nginx
# Adjust nginx limits
limit_req zone=announce_limit burst=500 nodelay;
```

### Logs

```bash
# Docker
docker logs -f ocelot-tracker

# Kubernetes
kubectl logs -f deployment/ocelot-tracker -n ocelot

# JSON logs
./ocelot-tracker 2>&1 | jq .
```

---

## Maintenance

### Rolling Updates

```bash
# Kubernetes
kubectl set image deployment/ocelot-tracker \
  tracker=ocelot-tracker:v2.0 -n ocelot

# Docker Compose
docker-compose pull
docker-compose up -d --no-deps --build tracker
```

### Health Checks

```bash
# HTTP health endpoint
curl http://localhost:34000/health

# Readiness probe
curl http://localhost:34000/ready
```

---

## Support

- GitHub Issues: https://github.com/mgdavisxvs/Ocelot/issues
- Documentation: ENHANCEMENTS.md
- Metrics Guide: See Prometheus/Grafana sections
