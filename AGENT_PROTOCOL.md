# Ocelot Node Agent Protocol Specification

**Version:** 1.0.0-draft
**Status:** BINDING (GUC ruling 2026-09-22)
**Scope:** Wire protocol between Ocelot control plane and Ocelot node agents

---

## 1. Overview

Every physical machine in the Ocelot compute fleet runs one `ocelot-agent` binary.
The agent communicates with the central control plane over HTTPS. The protocol
is a pull model: agents initiate all connections. The control plane never opens
a connection to an agent. Agents poll for desired state and push telemetry.

```
Control Plane (central)
    │  HTTPS (agent-initiated)
    │
    ├── POST /v1/nodes/register      agent → CP: one-time registration
    ├── POST /v1/nodes/:id/heartbeat agent → CP: every 30 s
    ├── POST /v1/nodes/:id/inventory agent → CP: every 5 min
    ├── GET  /v1/nodes/:id/desired   agent → CP: poll desired state
    ├── POST /v1/nodes/:id/health    agent → CP: workload status events
    └── POST /v1/nodes/:id/metrics   agent → CP: resource consumption
```

---

## 2. Authentication

### 2.1 HMAC-SHA256 per-node shared secret

Every agent message MUST carry the following HTTP headers:

```
X-Ocelot-Node-ID: <node_id>          (UUID v4, assigned at registration)
X-Ocelot-Timestamp: <unix_epoch_ms>  (milliseconds since epoch, UTC)
X-Ocelot-Signature: <hex_hmac>       (HMAC-SHA256, see §2.2)
```

The control plane MUST reject any request where:
- `X-Ocelot-Node-ID` is absent or not a registered node
- `X-Ocelot-Timestamp` is absent or falls outside the replay window
- `X-Ocelot-Signature` is absent or does not verify

### 2.2 Signature computation

```
message = node_id + ":" + timestamp + ":" + SHA256(request_body_bytes)
signature = HMAC-SHA256(message, node_secret)
X-Ocelot-Signature: hex(signature)
```

Where:
- `node_id` is the UUID from `X-Ocelot-Node-ID`
- `timestamp` is the decimal integer from `X-Ocelot-Timestamp`
- `SHA256(request_body_bytes)` is the hex SHA-256 digest of the raw request body
  (empty string `""` if body is absent or zero-length)
- `node_secret` is the per-node 32-byte random secret generated at registration

### 2.3 Replay window

The control plane MUST reject any request where:

```
| server_time_ms - X-Ocelot-Timestamp | > 60000
```

(i.e., more than 60 seconds difference). Agents MUST use a reliable clock
source (NTP-synchronized) or accept that requests outside this window will
be rejected with HTTP 401.

### 2.4 Bootstrap secret

At initial registration (§4), the agent does not yet have a `node_id` or
`node_secret`. The registration endpoint uses a shared bootstrap token instead:

```
Authorization: Bearer <bootstrap_token>
```

The `bootstrap_token` is a single secret configured on the control plane and
distributed to agents out-of-band (e.g., during node provisioning). It is
distinct from any per-node secret and MUST NOT be used after registration.

---

## 3. Transport

- All endpoints MUST use HTTPS (TLS 1.2+).
- Agents MUST verify the control plane's TLS certificate (system CA bundle or
  pinned certificate, configurable).
- Content-Type for all request and response bodies: `application/json`.
- Compression: agents MAY send `Content-Encoding: gzip` for bodies > 4 KiB.

---

## 4. Endpoints

### 4.1 Registration

**POST /v1/nodes/register**

Called exactly once per agent installation. Uses bootstrap token auth (§2.4).

**Request body:**

```json
{
  "hostname":      "r730-01.home.internal",
  "display_name":  "R730 Node 01",
  "arch":          "amd64",
  "agent_version": "1.0.0",
  "cpu_cores":     24,
  "ram_mb":        196608,
  "storage_gb":    4000,
  "gpus": [
    {
      "device_index": 0,
      "model":        "Tesla V100-SXM2-16GB",
      "vram_mb":      16384,
      "cuda_cap":     "7.0"
    }
  ],
  "labels": {
    "rack":     "homelab-01",
    "tier":     "compute",
    "location": "basement"
  }
}
```

**Field constraints:**

| Field          | Type    | Required | Constraints                        |
|----------------|---------|----------|------------------------------------|
| hostname       | string  | yes      | max 253 chars, valid DNS label     |
| display_name   | string  | no       | max 128 chars                      |
| arch           | string  | yes      | one of: amd64, arm64               |
| agent_version  | string  | yes      | semver format                      |
| cpu_cores      | integer | yes      | 1–65535                            |
| ram_mb         | integer | yes      | 1–67108864 (64 TiB)                |
| storage_gb     | integer | yes      | 0–1048576                          |
| gpus           | array   | no       | 0–16 entries                       |
| gpus[].device_index | integer | yes | 0–15                         |
| gpus[].model   | string  | yes      | max 128 chars                      |
| gpus[].vram_mb | integer | yes      | 1–2097152                          |
| gpus[].cuda_cap| string  | no       | semver-like, e.g. "7.0"           |
| labels         | object  | no       | string keys and values, max 32 pairs|

**Response 201 Created:**

```json
{
  "node_id":     "550e8400-e29b-41d4-a716-446655440000",
  "node_secret": "64-hex-chars-random-secret",
  "heartbeat_interval_sec": 30,
  "inventory_interval_sec": 300,
  "desired_poll_interval_sec": 15
}
```

The agent MUST store `node_id` and `node_secret` in persistent local storage
before responding. Loss of these values requires re-registration.

**Response 409 Conflict:** hostname is already registered. The response body
carries the existing `node_id`; the agent SHOULD use the re-registration
endpoint (§4.2) to refresh its secret.

**Response 401 Unauthorized:** bootstrap token invalid or absent.

---

### 4.2 Re-registration (secret rotation)

**POST /v1/nodes/:id/reregister**

Used when an agent has a valid `node_id` but its `node_secret` has been
rotated or lost. Uses bootstrap token auth (§2.4).

**Request body:**

```json
{ "node_id": "550e8400-e29b-41d4-a716-446655440000" }
```

**Response 200 OK:**

```json
{ "node_secret": "new-64-hex-chars-random-secret" }
```

The old secret is immediately invalidated upon issuance of the new one.

---

### 4.3 Heartbeat

**POST /v1/nodes/:id/heartbeat**

Sent every `heartbeat_interval_sec` seconds. The control plane marks a node
`lost` if no heartbeat is received for `3 × heartbeat_interval_sec`.

**Request body:**

```json
{
  "ts":            1748000000000,
  "status":        "ready",
  "load_avg_1m":   1.42,
  "cpu_used_pct":  12.5,
  "ram_used_mb":   8192,
  "uptime_sec":    86400
}
```

**Status values:**

| Value      | Meaning                                                  |
|------------|----------------------------------------------------------|
| `ready`    | Node can accept new workloads                            |
| `draining` | Node is finishing existing workloads; accept none new    |
| `busy`     | All schedulable resources are allocated                  |
| `degraded` | Node is operating with reduced capacity (e.g., bad GPU)  |
| `stopping` | Node is shutting down gracefully                         |

**Response 200 OK:**

```json
{
  "ok": true,
  "server_time_ms": 1748000000050
}
```

The agent SHOULD use `server_time_ms` to detect clock skew exceeding 30 s
and log a warning.

**Response 410 Gone:** The control plane does not recognise this `node_id`.
The agent MUST re-register (§4.1).

---

### 4.4 Inventory report

**POST /v1/nodes/:id/inventory**

Sent every `inventory_interval_sec` seconds. Carries full resource state.
The control plane uses this to update the schedulable resource pool.

**Request body:**

```json
{
  "ts":        1748000000000,
  "cpu_cores": 24,
  "ram_mb":    196608,
  "storage_gb": 4000,
  "gpus": [
    {
      "device_index":  0,
      "model":         "Tesla V100-SXM2-16GB",
      "vram_mb":       16384,
      "vram_free_mb":  13200,
      "cuda_cap":      "7.0",
      "health":        "ready",
      "workload_id":   "wl-abc123"
    }
  ],
  "cpu_free_pct":  87.5,
  "ram_free_mb":   188416,
  "disk_free_gb":  3800,
  "watts_current": 142.0,
  "agent_version": "1.0.0"
}
```

**GPU health values:**

| Value      | Meaning                                      |
|------------|----------------------------------------------|
| `ready`    | Device is healthy and free or allocated       |
| `degraded` | Device has ECC errors or throttling active   |
| `failed`   | Device is inoperable; workloads must not use it|

**Response 200 OK:**

```json
{ "ok": true }
```

---

### 4.5 Desired state poll

**GET /v1/nodes/:id/desired**

The agent polls this endpoint every `desired_poll_interval_sec` seconds.
The response carries the full desired workload set for this node.
The agent reconciles actual state against desired state and acts accordingly.

**Response 200 OK:**

```json
{
  "schema_version": 1,
  "workloads": [
    {
      "id":          "wl-abc123",
      "action":      "run",
      "manifest": {
        "name":      "embedding-service",
        "type":      "container",
        "image_ref": "infohash:sha256:abcdef...",
        "entrypoint": null,
        "args":      [],
        "env": {
          "MODEL_PATH": "/mnt/models/bge-m3"
        },
        "resources": {
          "cpu_millicores": 2000,
          "ram_mb":         4096,
          "gpu_count":      1,
          "gpu_vram_mb":    8192
        },
        "volumes": [
          {
            "volume_id":   "vol-models-001",
            "mount_point": "/mnt/models",
            "driver":      "local",
            "read_only":   true
          }
        ],
        "checkpoint_interval_sec": 3600,
        "timeout_sec":             0
      }
    }
  ],
  "prefetch": [
    {
      "infohash": "sha256:abcdef1234...",
      "priority": 80
    }
  ],
  "volumes": [
    {
      "volume_id":   "vol-models-001",
      "driver":      "local",
      "source_path": "/data/ocelot/volumes/vol-models-001"
    }
  ]
}
```

**Workload action values:**

| Value       | Meaning                                              |
|-------------|------------------------------------------------------|
| `run`       | Ensure this workload is running; start if not        |
| `stop`      | Terminate this workload (grace period from manifest) |
| `checkpoint`| Trigger a checkpoint snapshot now                    |

**Reconciliation rule:** Any workload that is running on the agent but absent
from the `workloads` array MUST be stopped with a 30-second grace period.

**Response 304 Not Modified:** May be returned if desired state has not changed
since the agent's last poll. Agents MUST handle 304 by taking no action.

---

### 4.6 Health report (workload events)

**POST /v1/nodes/:id/health**

Sent when a workload transitions state. The agent MUST NOT buffer more than
100 health events; if the buffer is full, the oldest events are dropped and
a `buffer_overflow: true` flag is set in the next health report.

**Request body:**

```json
{
  "ts": 1748000000000,
  "events": [
    {
      "workload_id":  "wl-abc123",
      "kind":         "started",
      "ts":           1748000000000,
      "pid":          12345,
      "exit_code":    null,
      "message":      ""
    },
    {
      "workload_id":  "wl-def456",
      "kind":         "failed",
      "ts":           1747999990000,
      "pid":          12300,
      "exit_code":    1,
      "message":      "OOM kill: exceeded 4096 MiB RAM limit"
    }
  ],
  "buffer_overflow": false
}
```

**Event kind values:**

| Kind          | Trigger                                                |
|---------------|--------------------------------------------------------|
| `started`     | Process/container confirmed running                    |
| `checkpoint`  | Checkpoint snapshot completed                          |
| `failed`      | Workload exited with non-zero code or signal           |
| `completed`   | Workload exited with code 0                            |
| `oom_killed`  | Workload terminated by OOM killer                      |
| `timed_out`   | Workload exceeded its `timeout_sec` limit              |
| `stopped`     | Workload was stopped by a `stop` desired-state action  |

**Response 200 OK:**

```json
{ "ok": true }
```

---

### 4.7 Metrics push

**POST /v1/nodes/:id/metrics**

Sent every 60 seconds. Carries per-workload resource consumption for billing
and scheduling.

**Request body:**

```json
{
  "ts":         1748000000000,
  "interval_sec": 60,
  "workloads": [
    {
      "workload_id":      "wl-abc123",
      "cpu_millicore_sec": 120000,
      "ram_mb_sec":        245760,
      "gpu_vram_mb_sec":   491520,
      "bytes_read":        104857600,
      "bytes_written":     52428800,
      "net_bytes_in":      1048576,
      "net_bytes_out":     2097152
    }
  ],
  "node": {
    "cpu_millicore_sec": 1440000,
    "ram_mb_sec":        11796480,
    "watts_sec":         8520.0
  }
}
```

**Response 200 OK:**

```json
{ "ok": true }
```

---

## 5. Error responses

All error responses use a consistent JSON envelope:

```json
{
  "error": {
    "code":    "NODE_NOT_FOUND",
    "message": "Node 550e8400-... is not registered",
    "retry":   false
  }
}
```

**Standard error codes:**

| HTTP | Code                    | Agent behaviour                            |
|------|-------------------------|--------------------------------------------|
| 400  | `BAD_REQUEST`           | Log error, skip this send cycle            |
| 401  | `UNAUTHORIZED`          | Re-register (§4.1) then retry              |
| 404  | `NODE_NOT_FOUND`        | Re-register (§4.1)                         |
| 409  | `HOSTNAME_CONFLICT`     | Use re-registration endpoint (§4.2)        |
| 410  | `NODE_GONE`             | Re-register (§4.1)                         |
| 429  | `RATE_LIMITED`          | Back off using `Retry-After` header        |
| 500  | `INTERNAL_ERROR`        | Retry with exponential backoff (see §6)    |
| 503  | `UNAVAILABLE`           | Retry with exponential backoff (see §6)    |

---

## 6. Retry policy

Agents MUST implement exponential backoff with jitter for all retryable errors:

```
attempt   delay (base)   jitter
1         2 s            ±0.5 s
2         4 s            ±1.0 s
3         8 s            ±2.0 s
4         16 s           ±4.0 s
5+        30 s           ±8.0 s  (capped)
```

After 10 consecutive failures on any endpoint, the agent MUST:
1. Log a structured error event with endpoint, attempt count, and last error code.
2. Continue retrying at the capped interval.
3. NOT exit; the process must remain running to recover when connectivity returns.

---

## 7. Agent startup sequence

```
1. Load stored node_id and node_secret from local config.
   If absent: POST /v1/nodes/register (bootstrap token).
   Store returned node_id and node_secret.

2. POST /v1/nodes/:id/inventory  (initial full inventory report)

3. GET  /v1/nodes/:id/desired    (initial desired state poll)
   Reconcile: start/stop workloads per desired state.

4. Enter main loop:
   ├── Every heartbeat_interval_sec:   POST /v1/nodes/:id/heartbeat
   ├── Every inventory_interval_sec:   POST /v1/nodes/:id/inventory
   ├── Every desired_poll_interval_sec: GET /v1/nodes/:id/desired
   ├── Every 60 s:                      POST /v1/nodes/:id/metrics
   └── On any workload event:           POST /v1/nodes/:id/health
```

---

## 8. Workload reconciliation algorithm

On every desired state poll response, the agent runs:

```
desired_ids  = set of workload IDs in desired.workloads where action == "run"
running_ids  = set of workload IDs currently running on this node

to_start = desired_ids - running_ids
to_stop  = running_ids - desired_ids

for wl_id in to_stop:
    stop(wl_id, grace_period=30s)
    emit health event: kind=stopped

for wl_id in to_start:
    fetch_artifact_if_needed(manifest.image_ref)
    resolve_volume_mounts(manifest.volumes)
    start(manifest)
    emit health event: kind=started | kind=failed
```

Fetch and start operations are idempotent. The agent MUST tolerate being
told to start a workload that is already starting (dedup by workload_id).

---

## 9. Prefetch behaviour

When the desired state response includes a `prefetch` array, the agent MUST
initiate artifact acquisition in the background (non-blocking), ordered by
descending priority. An artifact already present on disk requires no action.

---

## 10. Volume mount behaviour

Before starting any workload, the agent MUST mount all volumes listed in
`manifest.volumes`. If any volume mount fails, the workload MUST NOT start
and a `failed` health event MUST be emitted with the mount error in `message`.

---

## 11. Schema versions

The `schema_version` field in desired state responses allows the protocol
to evolve. Agents that receive a `schema_version` they do not recognise MUST:
1. Log a warning with the received version.
2. Skip reconciliation for that response.
3. Continue polling; a newer agent version should be deployed.

Current version: **1**.

---

## 12. Security considerations

- `node_secret` is equivalent to a private key. It MUST be stored with
  permissions `0600` and MUST NOT appear in logs or metrics.
- The replay window (§2.3) prevents captured requests from being replayed.
- Agents MUST validate TLS certificates. `InsecureSkipVerify` is prohibited.
- The bootstrap token MUST be rotated if any agent binary or config is
  compromised.
- Future: mTLS per-node certificates will replace the HMAC scheme in a
  backward-compatible upgrade. The endpoints and body schemas remain identical;
  only the auth layer changes.

---

## 13. Reference implementation notes

The reference agent implementation lives in `compute/agent/`.

Minimum Go dependencies required:
- `crypto/hmac`, `crypto/sha256` — signature computation (stdlib)
- `encoding/json` — message serialisation (stdlib)
- `net/http` — HTTP client (stdlib)
- No external dependencies for the protocol layer.

The reference control-plane handler lives in `compute/api/agent_handler.go`.
