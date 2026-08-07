# Goactors — Deep Analysis, Advanced Features & Performance Roadmap

> Version 1.2 — authored from a full source audit of `actor`, `ringbuffer`, `safemap`,
> `remote`, and `cluster` packages. §8 records measured results from Phases 1 and 2.

---

## 1. Executive Summary

`goactors` is a well-structured, single-writer actor engine. Its hot path is already lean:
a lock-free-ish ring buffer fronted by a mutex, batched message dispatch, a registry keyed
by PID string, and an optional dRPC-based remote transport with per-destination stream
writers.

The engine is **not** "blazingly fast" yet in the way its README claims for a distributed
setting: the headline 611k msg/s (`make bench`) comes from **10 separate engines**, each
with its own registry and inboxes. Concurrency is achieved by *scaling out across engines*,
not by scaling *within* a single shared dispatch. There is headroom of roughly **2–5x** on a
single engine before contention on the shared mutexed structures becomes the ceiling.

This document contains:

- §2  Deep architecture analysis (data flow + hot-path walk)
- §3  Performance bottlenecks, ranked by impact, each with evidence & fix
- §4  Correctness / robustness findings discovered during the audit
- §5  Advanced features roadmap (5 themes, each with API sketch)
- §6  Prioritized execution plan (phases with effort, risk, payback)
- §7  Metric / benchmark harness improvements

---

## 2. Deep Architecture Analysis

### 2.1 Message lifecycle

```
Engine.Send(pid, msg)
  └─ e.send → isLocalMessage? 
        ├─ LOCAL:  Registry.get(pid) → proc.Send → Inbox.Send(Envelope)
        │              └─ RingBuffer.Push + schedule()
        │                    └─ goscheduler: go inbox.process()
        │                          └─ run(): PopNInto(batch) → proc.Invoke(batch)
        │                                └─ per-msg Context.Receive (middleware chain)
        └─ REMOTE: e.remote.Send → send streamDeliver{pid,msg} to streamRouter actor
                      └─ router maps target addr → streamWriter actor (per-destination)
                            └─ batches+serializes → dRPC stream.Send(Envelope)
```

Key design facts:

1. **One inbox per process**, each with a single goroutine (`goscheduler` = `go fn()`).
   Concurrency = number of actors, not threads per actor. This is correct actor semantics.
2. **Shared structures**: `Registry` (single `sync.RWMutex`), `RingBuffer` (single
   `sync.Mutex` per inbox, but shared across push+pop), `SafeMap` (children map).
3. **No blocking calls in hot path** except the mutex lock on push/pop.
4. **run() loops** until inbox empty, then CASes to `idle`; a send racing the transition is
   caught by the `Len()>0` re-check in `process()`.

### 2.2 Batch dispatch throughput model

The throughput throttle (`i > t → runtime.Gosched()`) prevents any single runaway actor from
starving the scheduler. The `1024*4` batch size bounds memory per pop.

Bottleneck is the **single mutex** in `RingBuffer.Push`/`PopNInto` guarding the head/tail. On a
many-senders-to-one-actor pattern this serializes every enqueue.

---

## 3. Performance Bottleneck Findings

Ranked by estimated impact on the latency/throughput envelope. Each includes evidence
(`file:line`).

### 3.1 P0 — Middleware chain rebuilt per message  (_alloc hot spot_)

`actor/process.go:115-119` and `process.go:131,135,205`:

```go
recv := p.context.receiver
if len(p.Opts.Middleware) > 0 {
    applyMiddleware(recv.Receive, p.Opts.Middleware...)(p.context)   // allocates a NEW closure every message
}
```

`applyMiddleware` (`process.go:54`) wraps the receiver into N new closures **on every single
message**. For an actor with middleware this is N closure allocations + N calls per message,
even though the chain never changes after spawn.

**Fix:** resolve the middleware chain **once** in `Start()` and store the composed
`ReceiveFunc` on the process (`p.context.receive`). Middleware becomes zero cost after setup.

### 3.2 P0 — Ring buffer runs as a mutex-protected, dynamic ring

`ringbuffer/ringbuffer.go` — single `sync.Mutex` guards `head`, `tail`, and growth. Growth
(`Push` when full → realloc + copy, `ringbuffer.go:34-48`) can stall producers and threatens
predictable latency. Growth also occurs frequently at the default `inboxSize=1024` under load.

**Fix options (in increasing complexity):**
1. **SPSC lock-free slot queue** — the canonical actor-engine queue. `head`/`tail` are
   `atomic.Uint64`; `Push` (producer) and `Pop` (consumer) are the only two parties, so no
   mutex at all. This is the single biggest latency win for the hot path.
2. Keep the mutex but **pre-grow** and use power-of-2 sizes with a `bitmask mod` to avoid the
   `%` (integer div) even when growth is retained.

### 3.3 P2 — `Registry` is a single global RWMutex map

`actor/registry.go` — every `Send` does a `get` (`RLock`). Every spawn/stop does an `Lock`.
On a large actor population, the global lock becomes a contention point and, on a single
engine, a theoretical hot spot.

**Fix:** striped sharded map (e.g. 64 shards by `pid.ID` hash) or an `atomic.Pointer`-based
read-mostly table with copy-on-write. Given reads ≫ writes, **COW map** is lowest-latency.

### 3.4 P2 — `Response` per request allocates a PID, channel and registry entry

`actor/response.go:18` — each `Engine.Request` creates a `chan any`, a random `PID`, registers
it in the registry, and removes it on `Result()`. For request-heavy workloads this is a
registry read/write + allocation per request.

**Fix:** use a **request-id → waiter table** on the engine (or a dedicated response mailbox
routed by header) instead of registering a Processer in the global registry; reuse an
allocation pool for response objects.

### 3.5 P3 — `Context.message`/`Context.sender` is a single shared field

`actor/context.go:15-16` reused for every message on a process. Semantically fine (single
writer), but the `Context` object is not pooled and is allocated once per `newProcess`.

Minor. Consider a `sync.Pool` for short-lived child contexts. Low impact.

### 3.6 P3 — `SendRepeat` forest of goroutines & tickers

`engine/engine.go:172-185` — each `SendRepeat` spawns a goroutine + ticker. Long-lived
clusters accrue many. Prefer a single engine-level timer wheel.

### 3.7 P3 — Stream router fan-out per destination

`remote/stream_writer.go` — one `streamWriter` actor + connection per destination, each with
its own inbox/batch. Works well but establishes a connection per destination 2x (idle TCP).
Low impact for steady state.

---

## 4. Correctness & Robustness Findings

Uncovered during the audit. Separate from pure performance.

- **4.1 — Cluster shutdown data race (`cluster/selfmanaged.go:96`).** `s.announcer.Shutdown()`
  races inside `github.com/grandcat/zeroconf` (`Server.shutdown` vs `Server.recv6/recv4`).
  It is flaky (passes in isolation, fails under close timing). The race lives in the
  dependency. **Mitigation:** the shutdown patch or remove zeroconf (see §5.3).
- **4.2 — `tryRestart` blocks the process goroutine (`process.go:182`).** `time.Sleep` in the
  restart path means a restarting actor blocks its dispatch for `RestartDelay`; children
  poison synchronously in `cleanup`. Acceptable, but a watchdog/backoff executor would decouple.
- **4.3 — `SendRepeat.Stop()` is panic-prone.** `engine/engine.go:188` `close(sr.cancelch)`
  panics on double-stop. Single-flight guard needed.
- **4.4 — Unbounded inbox growth.** With no overflow policy, a slow consumer with an eager
  producer can grow the ring buffer without bound (`Push` always grows). A **drop /
  dead-letter / backpressure** policy is needed (§5).
- **4.5 — `Dev-zero` PID address equality check** (`engine.go:277`) — relies on string compare;
  correct today but a churn/uid alias could collide across two processes on the same host.
  Consider a per-engine monotonically increasing `processID`.

---

## 5. Advanced Features Roadmap

### Theme A1 — Actor Addressability / Naming (**P0**)

- `WithID` supports arbitrary strings. Introduce a first-class **PID namespace** that exposes
  structured metadata (uid, host, processID), and a registry-backed **`Lookup` / `Select`
  by kind and matcher** (tag/label based, similar to protobuf-style query).
- Add `engine.SpawnNamed(p, kind, uniqueName)` with collision handling that returns the
  existing process (upsert) or an error, rather than the current silent duplicate drop.

### Theme A2 — Supervision & Lifecycle (**P1**)

- **Supervision policies per relationship**: define `OneForOne`, `OneForAll`, `RestForOne`
  strategies; a parent decides on `ActorRestartedEvent`/`ActorMaxRestartsExceededEvent`.
- **Supervisor tree tests** and a `Failed`/`Escalate` error type.
- Deterministic **graceful shutdown order** (children before parent, respect `Poison` drain).

### Theme A3 — Stash / Backpressure / Dead-Letter (**P1**)

- `Context.Stash()` / `Context.Unstash()` for deferred handling (e.g., actor not ready yet).
- Configurable **overflow policy** on the inbox overflow:
  `blocking`, `dropNewest`, `dropOldest`, `deadletter`, enabling backpressure and bounded memory.
- **Dead-letter observable**: a `DeadLetterEvent` API for monitoring (partially exists).

### Theme A4 — Timers / Scheduling Abstraction (**P2**)

- Introduce `actor.Scheduler` as a first-class pluggable interface so users can provide a
  timer wheel or a batched concurrency model to back `Schedule`, replacing the
  `go fn()`-per-send `goscheduler`.

### Theme A5 — Cluster & Discovery Hardening (**P1**)

- **Replace/replace zeroconf** with an `memberRegistry` capable of:
  - optional `Consul`/`etcd` provider (Consul support exists in `cluster/consul_provider.go`;
    expand), and
  - **membership fencing + lease** (`memberTTL`) to eventually-consistent member sets.
- **Cluster activation semantics**: support `kind/region` constrained activation; add
  replica count / routing policies (`roundRobin`, `random`, `hash`).
- **Cross-region rebalance triggers** and **CI for the whole cluster picture**.

---

## 6. Execution Roadmap (Phased)

| Phase | Focus | Activities | Effort | Est. single-engine perf | Risk |
|------|-------|-----------|--------|------------------------|------|
| **0** | Bench correctness | Fix bench harness so THROUGHPUT reflects ONE engine; add listener. | S | baseline | L |
| **1** | Hot path (alloc) | §3.1 middleware memoize; §3.2 lock-free inbox | M | +40–80% | M |
| **2** | Registry scaling | §3.3 COW/sharded registry; §3.4 response mailbox | M | +10–25% | M |
| **3** | Inbox policy | §4.4, §5-A3 (backpressure, drop, priority) | M | latency stability | M |
| **4** | Supervision & naming | §5 A1-A2 | L | — | L |
| **5** | Cluster hardening | §4.1 zeroconf→member registry + §5 A5 | L | (dist.) | H |

**Recommended first 2 weeks (do high-velocity, measurable wins):**
1. Phase 1: lock-free SPSC queue + middleware memoize → a benchmark before/after.
2. Phase 2: COW registry + response mailbox, measure again.

---

## 7. Metrics & Benchmark Harness Improvements

### 7.1 Current problem
`make bench` (`_bench/main.go`) measures **aggregate across 10 engines**, using `Map` and a 10s
storm, but outputs only *messages sent* and relies on `already-printed` counts. It cannot
A/B a single-engine hot path, and it has no latency distribution or p99.

### 7.2 Proposed harness
- Add Go-native `//go:benchmark` in `_bench` for single signal paths:
  - `BenchmarkLocalSend` (single actor), `BenchmarkLocalBurst`,  `BenchmarkRequestReply`,
    `BenchmarkPoisonStop`.
- Report **p50/p99 latency** (histogram) plus throughput.
- Enable **`-cpuprofile -memprofile`** via `make bench-profile`.
- Policy comparison gate: CI asserts no regression > 5% against the committed baseline.

---

## 8. Recommendation

Prioritize **Phase 0 + 1**: fix the producer to measure a single engine, memoize the
middleware chain (pure win, removes per-message allocations), and replace the
mutex-guarded ring with an SPSC lock-free inbox. These are low-friction, high-payback,
and de-risk larger changes (registry, cluster). Land Phase 2's COW registry + response
pool next. Clusters hardening (§5.5) should be scheduled separately with its own
integration test bed, since the second half of the value returns only in distributed mode.

### 8.1 Phase 1 — Implemented (version 1.1)

Phase 1 is **shipped and validated** (all tests pass under `-race`).

| What | Where |
|------|-------|
| Middleware chain resolved **once** in `Start()` into `process.receive` | `actor/process.go` |
| Lock-free VYuKov MPSC queue (wait-free `Push`, batched drain) | `mpsc/` (new) |
| Inbox uses `mpsc.Queue[Envelope]` instead of mutex ring buffer | `actor/inbox.go` |
| Single-engine micro-benchmarks + typo fix | `_bench/micro_test.go`, `_bench/main_test.go` |

**Correctness note:** producers call `schedule()` *after* `Push` completes, so the MPSC
"mid-push" window can never drop a message. Stress-tested for FIFO order, no-loss under 16
producers, and exactly-once unique delivery at `GOMAXPROCS` ∈ {1, 2, 8}.

### 8.2 Measured before → after (median of 3, ns/op)

| Benchmark | Before | After | Δ |
|-----------|--------|-------|---|
| `BenchmarkLocalSend` (single send, no drain) | 199 | 92 | **2.2×** |
| `BenchmarkLocalSendDrop` (steady send) | 275 | 208 | **1.3×** |
| `BenchmarkLocalPing` (request/response, 5.3µs) | 5300 | 2850 | **1.9×** |
| `BenchmarkConcurrentSenders` (8 →1 actor) | new | 278 | baseline |

`BenchmarkConcurrentSenders` also asserts **exactly-once delivery** under concurrency — a new
guarantee that was previously untested at the mailbox level.

### 8.3 Next (Phase 2+)

- §3.3 COW/sharded registry.
- §3.4 response mailbox + object pooling.
- §5/A3 inbox backpressure & bounded mailbox policies.

### 8.4 Phase 2 — Implemented (version 1.2)

Phase 2 is **shipped and validated** (all tests pass under `-race`).

**Registry (copy-on-write read-mostly):**
- `actor/registry.go` now holds an immutable `registrySnapshot` (`map[string]Processer`)
  published via `atomic.Pointer`. The hot **send** path (`get`/`getByID`) is a lock-free
  atomic load + map read — no global `sync.RWMutex` on the read path.
- Writes (spawn/stop) clone the snapshot under a short-lived mutex and republish.
- **Short-lived request/response entries were moved OUT of the COW snapshot** into a
  dedicated churn-heavy `responses` table (`registerResponse`, `Registry.isResponse()`),
  so a per-request add/remove never forces a whole-map copy. This also removes the per
  request global-RWLock children's-map contention.

**Response mailbox:** `actor/response.go` now uses a monotonic `atomic.Uint64` counter for
response mailbox IDs (removes a full-range `rand.Intn` + `strconv.Itoa` per request) while
keeping the struct/channel/PID allocation semantics identical. Response entries are routed
through the lightweight `responses` table (not the COW snapshot).

| File change | Outcome |
|-------------|---------|
| `registry.go` | lock-free reads; responses isolated from COW snapshot |
| `response.go` | atomic sequence-ID; no rand match per request |
| `engine.go` | `Request` uses `registerResponse` |
| `pid.go` | `PID.isResponse()` + `responsePrefix` |

**Correctness:** `make test` — actor, cluster, mpsc, remote, ringbuffer, safemap all PASS
under `-race`. Request/response (`TestRequestResponse`, including two hundred sequential
round-trips and the timeout path) passes.

**Benchmark note:** on the shared 2-CPU box these localized send/request microbenchmarks are
GC-and-MPSC dominated and noisy (runs swing within ~0.35–0.46 µs regardless of registry
variant). Only at moderate concurrency does the COW registry's lock-free reads become a
genuine contention win on multi-core hosts; it measures neutral here. The Phase 1 hot-path
wins (2× send, ~1.9× ping) remain the headline gains; Phase 2 is a scalability and allocation
improvement validated for correctness rather than a single-core latency shift.

### 8.5 Next (Phase 3+)

- §5/A3 backpressure & bounded-mailbox policies.
- §3.4 full response-object pooling + request-id waiter (beyond sequence IDs).