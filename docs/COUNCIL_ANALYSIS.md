
# OCELOT Go App — GUC Council Analysis
## Gödel Unified Council: Knuth · Turing · Church · Gödel

> **Mandate**: Analyze the Ocelot BitTorrent Tracker (Go Edition) and prescribe
> 10 feature-level solutions. Each feature is evaluated through four scholarly
> lenses: algorithmic correctness (Knuth), computational decidability (Turing),
> functional composition (Church), and formal consistency (Gödel).
>
> **Repo**: `mgdavisxvs/Ocelot`  |  **Lang**: Go 1.22+, SQLite WAL, Redis, PHP admin

---

```
╔══════════════════════════════════════════════════════════════════════════════════╗
║             G Ö D E L   U N I F I E D   C O U N C I L   ( G U C )             ║
║                        OCELOT Go App — Full Audit                              ║
╠══════════════════════════════════════════════════════════════════════════════════╣
║  KNUTH   │  Algorithmic Analysis · Data Structures · Literate Programming      ║
║  TURING  │  Computability · State Machines · Protocol Decidability             ║
║  CHURCH  │  Lambda Calculus · Functional Composition · Type Theory             ║
║  GÖDEL   │  Incompleteness · Consistency Bounds · Self-Reference Traps         ║
╚══════════════════════════════════════════════════════════════════════════════════╝
```

---

## FEATURE 01 — SQLite WAL Sharding + BufferedDB + Circuit Breaker

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│  F-01: PERSISTENCE LAYER ARCHITECTURE                                           │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│   Announce/Scrape                                                               │
│        │                                                                        │
│        ▼                                                                        │
│   ┌──────────────┐   write    ┌──────────────────────────────────────────────┐ │
│   │  BufferedDB  │──────────▶ │  channel queue  [cap=BatchBufferCap]         │ │
│   │  (tracker/   │            │  200ms flush ticker                          │ │
│   │   buffered_  │            │  batch INSERT / UPDATE                       │ │
│   │   db.go)     │            └──────────────┬───────────────────────────────┘ │
│   └──────────────┘                           │                                 │
│          │                                   ▼                                  │
│          │ read (bypass)       ┌─────────────────────────────┐                 │
│          └──────────────────▶  │  CircuitBreaker             │                 │
│                                │  CLOSED → OPEN (5 failures) │                 │
│                                │  HALF_OPEN → probe (3 max)  │                 │
│                                └──────────────┬──────────────┘                 │
│                                               │                                │
│                                               ▼                                │
│                          ┌────────────────────────────────────────────┐        │
│                          │  SQLiteShardManager                        │        │
│                          │  shard-0.db  shard-1.db  …  shard-N.db    │        │
│                          │  WAL mode  │  VACUUM INTO every 6h        │        │
│                          └────────────────────────────────────────────┘        │
│                                                                                 │
│  KNUTH  : O(1) amortised write via batch-queue; shard key must be power-of-2  │
│           for bit-mask routing — verify in db_sqlite.go ShardIndex()           │
│  TURING : BufferedDB is a 3-state TM tape head (EMPTY/QUEUED/FLUSHED);        │
│           non-halting risk if flush goroutine exits without drain              │
│  CHURCH : BufferedDB ≅ λq.λf.(q >>= f) — a Reader monad over IO;             │
│           circuit breaker ≅ Maybe functor lifting over DB calls                │
│  GÖDEL  : WAL sharding cannot prove its own consistency across shards;        │
│           cross-shard joins are undecidable without a coordinator lock         │
├─────────────────────────────────────────────────────────────────────────────────┤
│  GO SOLUTION                                                                    │
│                                                                                 │
│  // Shard routing — enforce power-of-2 shard count at init                    │
│  func shardIndex(infoHash [20]byte, n int) int {                               │
│      if n&(n-1) != 0 { panic("shard count must be power of 2") }              │
│      return int(binary.BigEndian.Uint16(infoHash[:2])) & (n - 1)              │
│  }                                                                              │
│  // Drain on shutdown — prevent non-halting flush goroutine                   │
│  func (b *BufferedDB) Drain(ctx context.Context) error {                       │
│      close(b.flushCh)                                                          │
│      select {                                                                  │
│      case <-b.done: return nil                                                 │
│      case <-ctx.Done(): return ctx.Err()                                       │
│      }                                                                          │
│  }                                                                              │
└─────────────────────────────────────────────────────────────────────────────────┘
```

---

## FEATURE 02 — Markov Chain Analytics Sidecar (Freeleech + Anomaly)

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│  F-02: MARKOV ANALYTICS ENGINE                                                  │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│   Tracker Events ──────────▶ Redis PubSub ──────────▶ ocelot-markov sidecar   │
│                                                              │                  │
│                              ┌───────────────────────────────▼──────────────┐  │
│                              │  MarkovChain (markov/internal/chain)          │  │
│                              │                                               │  │
│                              │  States:  SEED → ACTIVE → IDLE → DEAD        │  │
│                              │           ↑_____________________________↓     │  │
│                              │                                               │  │
│                              │  Transition matrix P[s_i][s_j] = count/total  │  │
│                              │  Beta distribution prior (db/beta.go)        │  │
│                              │  Governance rules (db/governance.go)         │  │
│                              └───────────────────────────────────────────────┘  │
│                                          │                                      │
│                          ┌───────────────┴───────────────┐                     │
│                          ▼                               ▼                     │
│              FreeleechPoller                   AnomalyDetector                 │
│              score ≥ threshold → notify        z-score vs population           │
│              Gazelle callback                  adaptive threshold (ML-01)       │
│                                                                                 │
│  KNUTH  : Markov matrix multiply is O(S²) per step; S=4 states so trivial;    │
│           Beta(α,β) posterior update is O(1) — correct amortised design       │
│  TURING : Markov chain ≅ probabilistic TM; steady-state is decidable iff      │
│           chain is ergodic — verify irreducibility in states.go                │
│  CHURCH : P[i][j] ≅ stochastic λ-term; Beta posterior ≅ Bayesian fold        │
│           over event stream: fold (λacc.λe. update acc e) prior events        │
│  GÖDEL  : Freeleech threshold is an axiom (0.75); no formal proof it          │
│           minimises false-positive rate — needs empirical consistency check    │
├─────────────────────────────────────────────────────────────────────────────────┤
│  GO SOLUTION                                                                    │
│                                                                                 │
│  // Ergodicity guard — prevent absorbing states                                │
│  func (c *Chain) IsErgodic() bool {                                            │
│      // BFS from every state; if any state unreachable, chain is absorbing    │
│      reachable := make([][]bool, len(c.States))                                │
│      for i := range c.States {                                                 │
│          reachable[i] = bfsReachable(c.Transitions, i)                        │
│          for j := range c.States {                                             │
│              if !reachable[i][j] { return false }                             │
│          }                                                                     │
│      }                                                                         │
│      return true                                                               │
│  }                                                                              │
│  // Bayesian Beta update (O(1))                                                │
│  func (b *Beta) Update(success bool) {                                         │
│      if success { b.Alpha++ } else { b.Beta++ }                                │
│  }                                                                              │
└─────────────────────────────────────────────────────────────────────────────────┘
```

---

## FEATURE 03 — VirtualServer Compute Orchestration

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│  F-03: VIRTUALSERVER — CONTAINER ORCHESTRATION LAYER                           │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│   ocelotctl / ocelot-cp                                                         │
│        │  REST (admin key auth)                                                 │
│        ▼                                                                        │
│   ┌────────────────────────────────────────────────────────────────────────┐   │
│   │  VirtualServer API  (:9091)                                            │   │
│   │  POST /v1/services   GET /v1/nodes   POST /v1/workloads                │   │
│   └──────────────────────┬─────────────────────────────────────────────────┘   │
│                          │                                                      │
│          ┌───────────────┼───────────────┐                                      │
│          ▼               ▼               ▼                                      │
│    Scheduler         Reconciler       NodeStore                                 │
│    (scorer.go)       (15s tick)       (SQLite)                                  │
│    Bin-pack          Desired→Actual   Heartbeat                                 │
│    score = cpu*w1    diff engine      TTL eviction                              │
│           + mem*w2                                                               │
│           + local*w3                                                             │
│                                                                                 │
│   Node ──▶ Agent (compute/agent) ──▶ Executor ──▶ Container/Process           │
│                │ heartbeat every 10s                                            │
│                ▼                                                                │
│           HealthProbe → HEALTHY / DEGRADED / DEAD                              │
│                                                                                 │
│  KNUTH  : Bin-packing is NP-hard; current scorer is a greedy first-fit-       │
│           decreasing heuristic — approximation ratio ≤ 11/9·OPT + 6/9        │
│  TURING : Reconciler is a reactive TM: state = (desired, actual);             │
│           termination guaranteed only if desired is reachable from actual      │
│  CHURCH : Scheduler ≅ λnode.score(node) — pure function; reconciler ≅         │
│           fix-point combinator Y applied to diff(desired, actual)              │
│  GÖDEL  : Distributed desired-state is unprovably consistent (CAP);           │
│           idempotency_store.go is the incompleteness mitigation               │
├─────────────────────────────────────────────────────────────────────────────────┤
│  GO SOLUTION                                                                    │
│                                                                                 │
│  // Improved scorer — weighted bin-pack with anti-affinity                    │
│  func (s *Scorer) Score(node *Node, svc *Service) float64 {                   │
│      cpuFit  := 1.0 - float64(node.CPUUsed+svc.CPUReq)/float64(node.CPUCap)  │
│      memFit  := 1.0 - float64(node.MemUsed+svc.MemReq)/float64(node.MemCap)  │
│      locality := localityBonus(node, svc)                                     │
│      spread   := antiAffinityPenalty(node, svc)                                │
│      return 0.4*cpuFit + 0.3*memFit + 0.2*locality - 0.1*spread              │
│  }                                                                              │
└─────────────────────────────────────────────────────────────────────────────────┘
```

---

## FEATURE 04 — Redis EventBus + SSE Real-Time Hub

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│  F-04: EVENT BUS ARCHITECTURE (Redis + SSE)                                    │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  Tracker Worker                                                                 │
│       │  Publish(event)                                                         │
│       ▼                                                                         │
│  ┌─────────────────────────────────────────────────────────────────────────┐   │
│  │  EventBus (in-process)  [tracker/event_bus.go]                          │   │
│  │  topic → []chan Event   (fan-out, non-blocking with overflow drop)       │   │
│  └─────────────────────────────────────────────────────────────────────────┘   │
│       │ Bridge goroutine (eventbus_redis.go)                                   │
│       ▼                                                                         │
│  ┌────────────┐   PUBLISH   ┌──────────────────────────────────────────────┐  │
│  │   Redis    │────────────▶│  SSE Hub  (tracker/sse_hub.go)               │  │
│  │  PubSub    │             │  GET /events  → text/event-stream            │  │
│  └────────────┘             │  fan-out to N admin dashboard clients        │  │
│       │  SUBSCRIBE          └──────────────────────────────────────────────┘  │
│       ▼                                                                         │
│  Markov sidecar receives events → updates chain + emits anomaly/freeleech      │
│                                                                                 │
│  KNUTH  : Fan-out to N subscribers is O(N) per event; ring-buffer per         │
│           subscriber prevents head-of-line blocking — correct design           │
│  TURING : EventBus is a FIFO channel machine; overflow drop ≅ lossy TM        │
│           tape — acceptable for metrics, fatal for audit events                │
│  CHURCH : EventBus ≅ Observable monad; subscriber ≅ λe.handleEvent(e)         │
│           Redis bridge ≅ natural transformation between local/remote functors  │
│  GÖDEL  : No total ordering across Redis shards; event causality is           │
│           unprovable without Lamport timestamps or vector clocks               │
├─────────────────────────────────────────────────────────────────────────────────┤
│  GO SOLUTION                                                                    │
│                                                                                 │
│  // Add Lamport clock to Event for causal ordering                             │
│  type Event struct {                                                            │
│      Topic   string                                                            │
│      Payload []byte                                                            │
│      Clock   uint64  // monotonic Lamport timestamp                           │
│  }                                                                              │
│  // Overflow policy: drop metrics, block on audit events                      │
│  func (b *EventBus) Publish(e Event) {                                         │
│      for _, sub := range b.subs[e.Topic] {                                    │
│          if e.Topic == TopicAudit {                                            │
│              sub <- e  // blocking — audit must not be lost                   │
│          } else {                                                              │
│              select { case sub <- e: default: b.metrics.DroppedInc() }       │
│          }                                                                     │
│      }                                                                         │
│  }                                                                              │
└─────────────────────────────────────────────────────────────────────────────────┘
```

---

## FEATURE 05 — Adaptive ML Anomaly Detection Stack

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│  F-05: ADAPTIVE ANOMALY DETECTION (ML-01 / ML-04)                             │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│   Per-announce peer metrics                                                     │
│       uploaded / downloaded / left / speed / port / UA                         │
│             │                                                                   │
│   ┌─────────▼──────────────────────────────────────────────────────────────┐   │
│   │  AnomalyDetector  [tracker/anomaly.go]                                 │   │
│   │                                                                        │   │
│   │  z-score = (x - μ) / σ  for {upload_ratio, speed, interval}          │   │
│   │                                                                        │   │
│   │  Threshold τ is ADAPTIVE:                                             │   │
│   │    AdaptiveThresholdPoller polls Markov API every FreeleechPollSec    │   │
│   │    τ_new = τ_base × (1 + population_variance_factor)                 │   │
│   └────────────────────────────────────────────────────────────────────────┘   │
│             │ anomaly score > τ                                                 │
│             ▼                                                                   │
│   ┌─────────────────────────────────────────────────────────────────────────┐  │
│   │  ClientDetector  [ML-04]   ─── UA fingerprint → known-bad list        │  │
│   │  FraudEnforcer             ─── ratio check + IP reputation            │  │
│   │  AdmissionPolicy           ─── final ALLOW / DENY / THROTTLE         │  │
│   └─────────────────────────────────────────────────────────────────────────┘  │
│                                                                                 │
│  KNUTH  : Welford online algorithm for μ/σ is O(1) per update with O(1)       │
│           space — preferred over batch recompute; verify in anomaly.go         │
│  TURING : Anomaly detection is a classifier TM; halts iff τ is finite;        │
│           adaptive τ introduces non-termination risk if API is unavailable     │
│  CHURCH : Detector ≅ λx.(z-score x) >>= (λz. if z>τ then Anomaly else OK)   │
│           ≅ continuation-passing style over the announce pipeline             │
│  GÖDEL  : No threshold τ can be proved optimal by the system itself;          │
│           self-referential calibration (Markov → τ → Markov) is a            │
│           potential fixed-point divergence — add convergence bound            │
├─────────────────────────────────────────────────────────────────────────────────┤
│  GO SOLUTION                                                                    │
│                                                                                 │
│  // Welford online variance (O(1) space, numerically stable)                  │
│  type WelfordAccum struct { n int64; mean, M2 float64 }                        │
│  func (w *WelfordAccum) Update(x float64) {                                    │
│      w.n++                                                                     │
│      delta := x - w.mean                                                      │
│      w.mean += delta / float64(w.n)                                           │
│      w.M2 += delta * (x - w.mean)                                             │
│  }                                                                              │
│  func (w *WelfordAccum) Variance() float64 {                                  │
│      if w.n < 2 { return 0 }                                                  │
│      return w.M2 / float64(w.n-1)                                             │
│  }                                                                              │
│  // Convergence bound for adaptive threshold                                   │
│  const MaxThresholdMultiplier = 3.0  // τ ≤ 3×τ_base                         │
└─────────────────────────────────────────────────────────────────────────────────┘
```

---

## FEATURE 06 — Peer Scoring + Swarm Health Prediction

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│  F-06: PEER SCORING + SWARM HEALTH (ML-02 / ML-03)                            │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│   Swarm state snapshot                                                          │
│   { seeders, leechers, snatch_velocity, peer_churn_rate }                       │
│             │                                                                   │
│   ┌─────────▼──────────────────────────────────────────────────────────────┐   │
│   │  SwarmHealthPredictor  [ML-02]  (tracker/types.go vicinity)            │   │
│   │                                                                        │   │
│   │  health = f(seed_ratio, snatch_v, churn)                               │   │
│   │         = w1·(S/L) + w2·snatch_v - w3·churn                          │   │
│   │  Categories: HEALTHY / DEGRADED / CRITICAL / DEAD                     │   │
│   └────────────────────────────────────────────────────────────────────────┘   │
│             │                                                                   │
│   ┌─────────▼──────────────────────────────────────────────────────────────┐   │
│   │  PeerScorer  [ML-03]                                                   │   │
│   │                                                                        │   │
│   │  score(peer) = upload_rate × reliability × freshness                  │   │
│   │  PeerSort: O(k log k) partial sort → top-k by score for peer list     │   │
│   │  Favours: high-upload, stable port, short announce gap                │   │
│   └────────────────────────────────────────────────────────────────────────┘   │
│                                                                                 │
│   ┌────────────────────────────────────────────────────────────────────────┐   │
│   │  DemandHeatmap  [tracker/demand_heatmap.go]                            │   │
│   │  2D time×torrent matrix → hot/cold tier classification                │   │
│   └────────────────────────────────────────────────────────────────────────┘   │
│                                                                                 │
│  KNUTH  : Top-k partial sort with heap is O(n log k); for n=200 peers,        │
│           k=50 num_want, this is ~350 comparisons vs 1400 for full sort        │
│  TURING : SwarmHealth FSM has 4 states; transitions are deterministic;        │
│           prediction horizon must be bounded (no unbounded lookahead)          │
│  CHURCH : score ≅ λp. product of component λ-terms; composable by            │
│           function composition; PeerSort ≅ sortBy (comparing score)           │
│  GÖDEL  : Swarm health self-reports via announce; a dishonest peer can        │
│           report false progress — health is consistent only under              │
│           honest-majority assumption (provable only externally)               │
├─────────────────────────────────────────────────────────────────────────────────┤
│  GO SOLUTION                                                                    │
│                                                                                 │
│  // O(n log k) top-k heap sort for peer list                                  │
│  func TopKPeers(peers []Peer, k int) []Peer {                                  │
│      h := &peerMinHeap{}                                                       │
│      for _, p := range peers {                                                 │
│          heap.Push(h, p)                                                       │
│          if h.Len() > k { heap.Pop(h) }                                       │
│      }                                                                         │
│      result := make([]Peer, h.Len())                                           │
│      for i := len(result)-1; i >= 0; i-- {                                    │
│          result[i] = heap.Pop(h).(Peer)                                        │
│      }                                                                         │
│      return result                                                             │
│  }                                                                              │
└─────────────────────────────────────────────────────────────────────────────────┘
```

---

## FEATURE 07 — Domain Adapter Vocabulary System (Pluggable Protocols)

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│  F-07: DOMAIN ADAPTER — PLUGGABLE PROTOCOL VOCABULARY                          │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│   domains/                                                                      │
│   ├── myapp.yaml    { domain, actions{event,query}, wire_format{format} }      │
│   └── game.yaml     { domain, actions{event,query}, wire_format{format} }      │
│             │                                                                   │
│             ▼  LoadAllVocabConfigs("domains")                                   │
│   ┌─────────────────────────────────────────────────────────────────────────┐  │
│   │  ConfiguredAdapter  [tracker/configured_adapter.go]                     │  │
│   │                                                                         │  │
│   │  Each YAML → one HTTP route on the tracker                             │  │
│   │  wire_format: json | bencode | msgpack                                 │  │
│   │  actions.event → mapped to tracker announce pipeline                   │  │
│   │  actions.query → mapped to tracker scrape pipeline                     │  │
│   │                                                                         │  │
│   │  Collision guard: btBuiltins set prevents overriding                   │  │
│   │  announce / scrape / update / stats / torrents / peers /               │  │
│   │  whitelist / report                                                    │  │
│   └─────────────────────────────────────────────────────────────────────────┘  │
│                                                                                 │
│  KNUTH  : Runtime route registration via map[string]http.Handler is O(1)      │
│           lookup; YAML parse at startup is O(F) where F = file count          │
│  TURING : Each adapter is a transducer TM over the wire format alphabet;      │
│           composition of adapters is decidable iff formats are unambiguous    │
│  CHURCH : Adapter ≅ λreq.decode(req) >>= process >>= encode — a Kleisli      │
│           arrow in the IO monad; YAML config ≅ Church encoding of the arrow   │
│  GÖDEL  : The vocabulary system is extensible but cannot verify its own       │
│           semantic correctness; a YAML with wrong action keys silently        │
│           produces a no-op route — needs schema validation at load time       │
├─────────────────────────────────────────────────────────────────────────────────┤
│  GO SOLUTION                                                                    │
│                                                                                 │
│  // JSON Schema validation for vocab YAML at load time                        │
│  func ValidateVocabConfig(vc VocabConfig) error {                              │
│      if vc.Domain == ""          { return errors.New("domain required") }     │
│      if vc.Actions.Event == ""   { return errors.New("actions.event required")}│
│      if vc.WireFormat.Format == "" {                                           │
│          return errors.New("wire_format.format required")                     │
│      }                                                                         │
│      validFormats := map[string]bool{"json":true,"bencode":true,"msgpack":true}│
│      if !validFormats[vc.WireFormat.Format] {                                  │
│          return fmt.Errorf("unknown wire format: %s", vc.WireFormat.Format)   │
│      }                                                                         │
│      return nil                                                                │
│  }                                                                              │
└─────────────────────────────────────────────────────────────────────────────────┘
```

---

## FEATURE 08 — Distributed Tracing with OpenTelemetry

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│  F-08: DISTRIBUTED TRACING — OpenTelemetry / OTLP                             │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│   HTTP Request                                                                  │
│        │  traceparent header (W3C)                                             │
│        ▼                                                                        │
│   ┌────────────────────────────────────────────────────────────────────────┐   │
│   │  TracerProvider (tracker/tracing_init.go)                              │   │
│   │  OTLP HTTP exporter → Jaeger / Tempo / OTLP collector                 │   │
│   │  Sample rate: OTEL_SAMPLE_RATE (default 0.1 = 10%)                   │   │
│   └─────────────────────────┬──────────────────────────────────────────────┘   │
│                             │ ctx propagation                                  │
│             ┌───────────────┴─────────────────────┐                            │
│             ▼                                     ▼                            │
│   Span: http.announce                   Span: db.batch_write                  │
│     ├── attr: info_hash                   ├── attr: shard_id                  │
│     ├── attr: peer_id                     ├── attr: batch_size                │
│     └── child: swarm.update               └── attr: latency_ms                │
│                                                                                 │
│   Trace graph:                                                                  │
│   announce ──▶ db.read ──▶ swarm.update ──▶ db.write ──▶ response             │
│            └──▶ anomaly.check ──▶ ratelimit.check                              │
│                                                                                 │
│  KNUTH  : Sampling at 10% means 90% of traces are lost; for high-QPS         │
│           trackers, tail-based sampling is algorithmically superior:          │
│           sample 100% of ERROR traces, 1% of OK traces                        │
│  TURING : Trace propagation is a context-passing TM; lost context (missing   │
│           traceparent) creates disconnected spans — non-fatal but opaque      │
│  CHURCH : Span ≅ λctx.(start span, run body ctx, end span) — a bracket       │
│           combinator; TracerProvider ≅ Reader monad threading ctx implicitly  │
│  GÖDEL  : Distributed traces cannot prove causal completeness; a missing     │
│           span between services is indistinguishable from a fast service      │
├─────────────────────────────────────────────────────────────────────────────────┤
│  GO SOLUTION                                                                    │
│                                                                                 │
│  // Tail-based sampler: always sample errors, rate-limit OK traces            │
│  type TailSampler struct{ errorRate, okRate float64; rng *rand.Rand }          │
│  func (s *TailSampler) ShouldSample(attrs []attribute.KeyValue) bool {        │
│      for _, a := range attrs {                                                 │
│          if a.Key == "error" && a.Value.AsBool() {                            │
│              return s.rng.Float64() < s.errorRate  // 100% for errors        │
│          }                                                                     │
│      }                                                                         │
│      return s.rng.Float64() < s.okRate  // 1% for ok                         │
│  }                                                                              │
└─────────────────────────────────────────────────────────────────────────────────┘
```

---

## FEATURE 09 — Rate Limiter + Admission Policy + Fraud Enforcer

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│  F-09: REQUEST ADMISSION PIPELINE                                               │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│   Inbound announce/scrape                                                       │
│        │                                                                        │
│        ▼                                                                        │
│   ┌──────────────────────────────────────────────────────────────────────────┐ │
│   │  RateLimiter  [token bucket, per-IP]  (tracker/ratelimit.go)            │ │
│   │  cap = 100,000 IP slots (sync.Map)                                      │ │
│   │  Rate = RateLimitRPS tokens/s  |  Burst = RateLimitBurst               │ │
│   └──────────────────────────────────┬───────────────────────────────────────┘ │
│                                      │ ALLOW                                   │
│        ┌─────────────────────────────▼────────────────────────────────────┐    │
│        │  AdmissionPolicy  (tracker/admission_policy.go)                  │    │
│        │  passkey present? → user exists? → user enabled? → not banned?   │    │
│        └──────────────────────────────┬───────────────────────────────────┘    │
│                                       │ PASS                                   │
│        ┌──────────────────────────────▼────────────────────────────────────┐   │
│        │  FraudEnforcer  (tracker/fraud_enforcer.go)                       │   │
│        │  upload_ratio sanity check                                        │   │
│        │  port in valid range (1024–65535)                                 │   │
│        │  IP reputation (optional)                                         │   │
│        └──────────────────────────────┬───────────────────────────────────┘   │
│                                       │ CLEAN                                  │
│                                       ▼                                        │
│                             Worker.handleAnnounce()                             │
│                                                                                 │
│  KNUTH  : Token bucket is O(1) per request; sync.Map under high concurrency   │
│           degrades — a sharded map (N=256 shards by IP hash) is O(1) and      │
│           contention-free                                                       │
│  TURING : Admission pipeline ≅ deterministic finite automaton over            │
│           {RATE_OK, ADMITTED, FRAUD_FREE}; each gate is a state transition    │
│  CHURCH : Pipeline ≅ (rateLimitCheck >>> admissionCheck >>> fraudCheck)        │
│           in arrow notation; short-circuit on first DENY — lazy evaluation    │
│  GÖDEL  : Upload ratio check cannot prove honest reporting; only external     │
│           cross-peer verification achieves consistency (unprovable in-system)  │
├─────────────────────────────────────────────────────────────────────────────────┤
│  GO SOLUTION                                                                    │
│                                                                                 │
│  // Sharded rate limiter map — 256 shards, O(1) lock-free hot path            │
│  const nShards = 256                                                           │
│  type ShardedLimiter struct {                                                  │
│      shards [nShards]struct {                                                  │
│          sync.Mutex                                                            │
│          m map[string]*rate.Limiter                                            │
│      }                                                                         │
│  }                                                                              │
│  func (s *ShardedLimiter) shard(ip string) int {                               │
│      h := fnv.New32a(); h.Write([]byte(ip))                                   │
│      return int(h.Sum32()) % nShards                                          │
│  }                                                                              │
│  func (s *ShardedLimiter) Allow(ip string) bool {                              │
│      idx := s.shard(ip)                                                        │
│      s.shards[idx].Lock(); defer s.shards[idx].Unlock()                       │
│      l, ok := s.shards[idx].m[ip]                                             │
│      if !ok { l = rate.NewLimiter(rps, burst); s.shards[idx].m[ip] = l }     │
│      return l.Allow()                                                          │
│  }                                                                              │
└─────────────────────────────────────────────────────────────────────────────────┘
```

---

## FEATURE 10 — Swarm Snapshot Persistence (Peer State Across Restarts)

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│  F-10: SWARM SNAPSHOT — PEER STATE PERSISTENCE                                 │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│   Graceful shutdown (SIGINT/SIGTERM)                                            │
│        │                                                                        │
│        ▼                                                                        │
│   ┌────────────────────────────────────────────────────────────────────────┐   │
│   │  SaveSnapshot(path, torrents)  [tracker/snapshot.go]                   │   │
│   │                                                                        │   │
│   │  Serialises:  map[info_hash] → []PeerEntry                            │   │
│   │  Format:     gob-encoded, compressed with gzip                        │   │
│   │  Atomicity:  write to .tmp, then os.Rename (atomic on POSIX)          │   │
│   │  Path:       $DB_DIR/swarm.snap                                        │   │
│   └─────────────────────────────────────────────────────────────────────────┘  │
│             │  On next startup:                                                 │
│             ▼                                                                   │
│   ┌────────────────────────────────────────────────────────────────────────┐   │
│   │  LoadSnapshot(path, torrents)                                          │   │
│   │                                                                        │   │
│   │  Deserialise → inject peers into in-memory TorrentList                │   │
│   │  Skip stale peers: age > PeersTimeout                                 │   │
│   │  Skip unknown info_hashes (not in DB)                                 │   │
│   └────────────────────────────────────────────────────────────────────────┘   │
│                                                                                 │
│   Timeline:                                                                     │
│   ─────────────────────────────────────────────────────────────────────────    │
│   run N :   …peers active…  →  SIGTERM → SaveSnapshot → exit                  │
│   run N+1:  startup → LoadSnapshot → peers restored → Reaper evicts stale     │
│                                                                                 │
│  KNUTH  : gob+gzip is O(P) encode where P = total peers; for 10^6 peers,     │
│           checkpoint every 15 min to bound recovery window; verify that       │
│           the atomicity rename lands on same filesystem as .tmp file           │
│  TURING : Snapshot ≅ TM tape dump; restore ≅ tape load; decidable only if    │
│           the deserialization alphabet matches the serialization alphabet —    │
│           version-tag the snapshot format                                      │
│  CHURCH : Snapshot ≅ Church numeral encoding of swarm state; restore ≅        │
│           Church decoding — both must be inverse functions (bijective)         │
│  GÖDEL  : A snapshot cannot prove it is consistent with the DB;               │
│           a peer in snapshot may have been banned in DB between runs —        │
│           post-load reaper pass is the necessary consistency check            │
├─────────────────────────────────────────────────────────────────────────────────┤
│  GO SOLUTION                                                                    │
│                                                                                 │
│  // Version-tagged snapshot format (Turing format invariant)                  │
│  const snapshotVersion uint32 = 2                                              │
│  type SnapshotHeader struct {                                                  │
│      Magic   [4]byte  // {'O','C','L','T'}                                    │
│      Version uint32                                                            │
│      SavedAt int64    // unix timestamp                                        │
│  }                                                                              │
│  // Post-load consistency sweep (Gödel mitigation)                            │
│  func ReconcileSnapshotWithDB(torrents *TorrentList, db *SQLiteShardManager) { │
│      torrents.Range(func(ih [20]byte, t *Torrent) bool {                      │
│          for ip, peer := range t.Peers {                                      │
│              if isBanned(db, peer.UserID) { delete(t.Peers, ip) }            │
│          }                                                                     │
│          return true                                                           │
│      })                                                                        │
│  }                                                                              │
│  // Periodic snapshot (not only on shutdown)                                  │
│  func StartPeriodicSnapshot(ctx context.Context, path string,                 │
│      torrents *TorrentList, interval time.Duration) {                         │
│      go func() {                                                               │
│          t := time.NewTicker(interval)                                        │
│          for { select {                                                        │
│              case <-t.C: SaveSnapshot(path, torrents)                         │
│              case <-ctx.Done(): return                                         │
│          } }                                                                   │
│      }()                                                                       │
│  }                                                                              │
└─────────────────────────────────────────────────────────────────────────────────┘
```

---

## GUC SUMMARY MATRIX

```
╔═══╦══════════════════════════════════════╦══════════╦══════════╦═══════════╦════════════╗
║ # ║ Feature                              ║ Knuth    ║ Turing   ║ Church    ║ Gödel      ║
╠═══╬══════════════════════════════════════╬══════════╬══════════╬═══════════╬════════════╣
║ 1 ║ SQLite WAL Sharding + BufferedDB     ║ ✓ O(1)   ║ ⚠ drain  ║ ✓ monad  ║ ⚠ x-shard ║
║ 2 ║ Markov Chain Analytics Sidecar       ║ ✓ O(S²)  ║ ✓ ergod. ║ ✓ Bayes  ║ ⚠ τ axiom ║
║ 3 ║ VirtualServer Orchestration          ║ ⚠ NP-h.  ║ ✓ FSM    ║ ✓ fix-Y  ║ ⚠ CAP     ║
║ 4 ║ Redis EventBus + SSE Hub             ║ ✓ O(N)   ║ ⚠ lossy  ║ ✓ monad  ║ ⚠ order   ║
║ 5 ║ Adaptive Anomaly Detection           ║ ✓ Welf.  ║ ⚠ τ-div  ║ ✓ CPS    ║ ⚠ self-ref║
║ 6 ║ Peer Scoring + Swarm Health          ║ ✓ O(nlogk║ ✓ bound. ║ ✓ compose║ ⚠ dishon. ║
║ 7 ║ Domain Adapter Vocabulary            ║ ✓ O(1)   ║ ✓ transd.║ ✓ Kleisli║ ⚠ schema  ║
║ 8 ║ Distributed Tracing (OTel)           ║ ⚠ 10%s.  ║ ⚠ ctx-los║ ✓ bracket║ ⚠ causal  ║
║ 9 ║ Rate Limiter + Admission + Fraud     ║ ⚠ sync.Map║ ✓ DFA   ║ ✓ arrow  ║ ⚠ ratio   ║
║10 ║ Swarm Snapshot Persistence           ║ ✓ O(P)   ║ ⚠ versn. ║ ✓ bijct. ║ ⚠ DB-sync ║
╠═══╩══════════════════════════════════════╩══════════╩══════════╩═══════════╩════════════╣
║ ✓ = sound    ⚠ = defect identified (binding ruling — must address)                     ║
╚═════════════════════════════════════════════════════════════════════════════════════════╝
```

## GUC BINDING RULINGS (Severity: CRITICAL / HIGH / MEDIUM)

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│  RULING-01 [CRITICAL] F-01 BufferedDB must implement Drain(ctx) on shutdown    │
│  RULING-02 [CRITICAL] F-01 Shard count must be validated as power-of-2 at init │
│  RULING-03 [HIGH]     F-02 Markov chain must pass ergodicity check at startup  │
│  RULING-04 [HIGH]     F-05 Adaptive threshold must have convergence bound 3×   │
│  RULING-05 [HIGH]     F-04 Audit events must never be dropped (blocking chan)  │
│  RULING-06 [HIGH]     F-07 VocabConfig must be schema-validated at load time  │
│  RULING-07 [MEDIUM]   F-08 Replace head-based 10% sampling with tail-sampler  │
│  RULING-08 [MEDIUM]   F-09 Replace sync.Map rate limiter with sharded map     │
│  RULING-09 [MEDIUM]   F-10 Version-tag snapshot format (magic + version field) │
│  RULING-10 [MEDIUM]   F-10 Post-load snapshot/DB reconciliation sweep required │
└─────────────────────────────────────────────────────────────────────────────────┘
```

---

*Gödel Unified Council — Session closed.*
*Generated by GUC analysis of mgdavisxvs/Ocelot at commit HEAD.*
