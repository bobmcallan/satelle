---
type: document
title: 'Kafka vs Redis (vs keep Kafka) for Fly prototype'
description: 'Inventory of kafka-python/aiokafka usage in solidsafe_engine; scored options for Fly (keep Kafka, Redis adapter, managed bus); effort ranges; recommendation aligned with Temporal as Track B end-goal — no permanent Redis architecture.'
tags:
- document
- kafka
- redis
- fly
- architecture
- solidsafe
- epic:fly-prototype-stack
timestamp: '2026-07-09T00:00:00Z'
---

# Kafka vs Redis for Fly prototype

*Assessment for epic `fly-prototype-stack` (story sty_d3152eb3). Code package is
`solidsafe_engine/` (legacy name rainbow_wizard). Temporal remains Track B end-goal —
Redis must not be designed as permanent.*

Related: [[fly-topology-options]], [[solidsafe-engine-componentisation-notes]].

---

## 1. Inventory of Kafka touchpoints

### 1.1 Topics (`settings.py` + env)

| Setting | Default topic | Purpose |
|---|---|---|
| `tasktopic` | `task-queue` | Work units for taskrunner (enumerate items) |
| `jobtopic` | `job-queue` | Work units for jobrunner (backup transfer) |
| `taskstatustopic` | `task-status` | Task lifecycle status |
| `jobstatustopic` | `job-status` | Job lifecycle status |
| `tasklogtopic` | `task-log` | Task log lines (streaming / receiver) |
| `joblogtopic` | `job-log` | Job log lines |
| `objectchangetopic` | `object-change` | Object change events → DB updater |
| bootstrap | `KAFKA_BOOTSTRAP` | Broker address |

Note: some env keys are duplicated incorrectly in settings (e.g. `JOB_LOG_TOPIC` used for
both job log and job queue) — a Redis port should fix env naming, not copy the bug.

### 1.2 Libraries

| Library | Where | Role |
|---|---|---|
| `kafka-python` | producers, sync consumers, admin | Primary |
| `aiokafka` | async consumers in servers / receivers | Status SSE, log-receiver path, update-receiver |
| `KafkaLoggingHandler` (`core/utilities.py`) | log → topic | Uses producer.send with event id as key |

### 1.3 Producers

| Location | What is sent | Pattern |
|---|---|---|
| `integrations.py` (~340) | Enqueue task dict → `task-queue` | Fire-and-forget KafkaProducer |
| `core/rainbowizard.py` `JobEvent` | Status (`{type}-status`), logs (`{type}-log`), job fan-out → `job-queue`, object-change | Long-lived producer on JobEvent; key = event UUID bytes |
| `eventconsumer.py` | Log lines via `KafkaLoggingHandler` → `{type}-log` | Producer for handler while job runs |
| `log-receiver.py` | Occasional re-publish / task-log | Producer alongside consumer |
| `update-receiver.py` | Downstream messages via AIOKafkaProducer | Async producer |

### 1.4 Consumers

| Location | Topics | Pattern |
|---|---|---|
| `eventconsumer.py` (taskrunner / jobrunner) | `CONSUMER_TOPIC` (`task-queue` or `job-queue`) | **Run-once:** create consumer, `next()`, process, exit. `group_id='executor'`. KEDA ScaledJobs relied on this |
| `log-receiver.py` | job-log, task-log, status topics | Long-running; write logs/status into Postgres |
| `update-receiver.py` | `object-change` | Long-running; apply object changes |
| `server.py` | `task-status` (and similar) | AIOKafkaConsumer for live status streaming |
| `integrations.get_kafka_logs` | log topics | Seek by time/id, drain historical log lines |
| `integrations.get_consumer_offsets` / queue depth | admin API | KafkaAdminClient + consumer group lag for `executor` — used by serveradmin status |

### 1.5 Semantic features used (beyond plain queue)

These matter when comparing Redis:

1. **Consumer groups** (`executor`, `objectupdater`) — partition load-split for workers.
2. **Message keys** (UUID bytes) — partitioning affinity (not strongly required for correctness today).
3. **Offset / lag inspection** — ops and admin UI queue depth.
4. **Historical log read** — `get_kafka_logs` seeks offsets by time; logs are also persisted to object storage / DB — Kafka is a *realtime* path, not the only durable log store.
5. **Run-once workers** — process one message then exit; external scaler spawns next (k8s KEDA). On Fly/compose, `service-main.sh` runs a loop with PARALLELISM instead.

### 1.6 What is *not* Kafka

- Scheduler due-scan → DB + enqueue (producer only).
- Actual backup I/O → MinIO/S3 and vendor APIs.
- Auth / tenancy → Postgres + gateway headers.

Kafka is the **async control plane between controlserver/scheduler and workers**, plus
**realtime status/log fan-in** to receivers.

---

## 2. Options

### Option K — Keep Kafka on Fly

**Variants:**

| Sub-option | Notes |
|---|---|
| K1 Self-host KRaft (as compose does) | One Fly machine, ~1–2 GB RAM min; volume optional for prototype |
| K2 Redpanda single-node | Kafka API compatible; often lighter than full Kafka |
| K3 Managed Kafka (e.g. Aiven, Confluent, Upstash Kafka if available) | Ops outsourced; cost; network egress from `syd` |

**Code change:** none (env `KAFKA_BOOTSTRAP` only). Compose already validates the path.

**Pros:** Zero eng risk on queue semantics; admin lag APIs keep working; Temporal later can
replace the whole orchestration path without intermediate Redis design.

**Cons:** Heaviest Fly machine / cost; KEDA not available — scale workers by fixed process
count; ops burden if broker dies (prototype acceptable).

### Option R — Redis adapter (lists or streams)

**Shape:** thin module replacing producer/consumer edges:

- Lists: `LPUSH`/`BRPOP` per queue name — simple work queues; no consumer groups, weak multi-consumer fairness without extra design.
- Streams: `XADD`/`XREADGROUP` — closer to consumer groups; still no free historical log API like Kafka offsets-by-time.

**Must reimplement or drop:**

| Capability | Redis approach | Effort |
|---|---|---|
| task/job enqueue + worker consume | Streams or lists | M — core path |
| status/log topics → logreceiver | Streams + consumer groups, or side-channel to DB only | M — if keeping parity |
| object-change | Stream | S |
| `get_kafka_logs` live tail | Poll stream / fall back to DB+object store only | S–M (can **drop** Kafka log path for prototype if DB/storage logs suffice) |
| consumer offset admin UI | Approximate stream lag or remove for prototype | S (drop OK) |

**Abstraction risk:** A general `QueueBackend` interface that pretends Kafka and Redis are
equivalent **and** that Temporal will implement later is a **dead-end** — Temporal is
workflow orchestration, not a message bus drop-in. Prefer **call-site shims** (or env-gated
branches at existing producer/consumer sites) over a permanent pluggable bus.

**Pros:** Small Fly footprint (Upstash Redis or single Redis machine); cheap; common prototype
pattern.

**Cons:** 1–2+ weeks of careful port + regression of backup fan-out path; easy to over-abstract;
status/log streaming may regress; **no lasting asset for Track B**.

### Option M — Other managed bus (NATS, SQS, …)

- **SQS:** poor fit for multi-topic status/log fan-in and run-once k8s model; AWS region may fight AU story.
- **NATS JetStream:** lighter than Kafka, still a new client stack everywhere.
- Effort ≥ Redis with less ecosystem familiarity in this codebase → **not recommended** for 6-week prototype.

---

## 3. Scoring (prototype only)

Scale 1–5 (5 = best for 6-week Fly prototype).

| Criterion | K Keep Kafka | R Redis adapter | M Other bus |
|---|---|---|---|
| Engineering effort (lower better → higher score) | **5** (config only) | 2 | 1 |
| Fly ops / cost | 2 | **5** | 3–4 |
| Behavioral fidelity (workers, status, logs) | **5** | 3 | 2 |
| Risk of production bugs in backup path | **5** | 2 | 2 |
| Alignment with Temporal end-goal (no dead-end) | **5** | 3* | 2 |
| Time-to-first-deploy on Fly | **4** | 2 | 1 |
| **Weighted feel** | **Strong default** | Only if Kafka blocked | No |

\*Redis scores middling on Temporal alignment only if the adapter is **throwaway** and
**narrow** (no grand Queue interface). A polished multi-backend bus scores **1** (wasted work).

### Effort ranges (person-days)

| Option | Eng | Ops/Fly setup | Total rough |
|---|---|---|---|
| K1 Kafka KRaft on Fly | 0.5–1 d (fly.toml + health) | 1–2 d (sizing, volume, restart) | **~2–3 d** |
| K2 Redpanda | 0.5 d (image swap, same API) | 1 d | **~1.5–2 d** |
| K3 Managed Kafka | 0.5 d (bootstrap URL/TLS) | 0.5–1 d account/network | **~1–2 d** |
| R Redis streams (work queues only; logs via DB/storage) | 5–8 d | 0.5 d Upstash/Fly Redis | **~6–9 d** |
| R Redis full parity (status/log/admin) | 10–15 d | 0.5 d | **~11–16 d** |
| M NATS/SQS | 8–12 d | 1 d | **~9–13 d** |

---

## 4. Recommendation

### Call: **Keep Kafka for the Fly prototype (Option K). Do not implement Redis now.**

**Go/no-go for Kafka→Redis before implementation stories: NO-GO (Redis).**

### Rationale

1. **The expensive, correctness-critical path is already Kafka-shaped** — run-once workers,
   job fan-out, status/log pipeline. Changing it mid-prototype burns senior time that the
   hybrid straw man assigns to isolation and restore, not queue plumbing.
2. **Fly cost of one Kafka machine is real but smaller than eng risk** of a botched adapter
   during a 6-week demo window. Prefer a slightly larger machine (or Redpanda) over a rewrite.
3. **Temporal is the end-goal orchestrator** (Track B). A Redis design that aspires to be
   the long-term bus is **wrong architecture**; a throwaway Redis shim is **throwaway work**.
   Keeping Kafka preserves the current spine until Temporal replaces *orchestration*, not
   just the transport.
4. **Componentisation notes already said "keep Kafka initially"**; this assessment confirms
   that call under the full-stack-on-Fly decision.

### Preferred Kafka sub-option for Fly

1. **First try: Redpanda single-node** (Kafka API, lighter) or **Kafka KRaft** image already
   used in compose — same `KAFKA_BOOTSTRAP` contract.
2. If memory/cost painful after deploy: **managed Kafka** with private networking — still
   zero application code change.
3. **Only reopen Redis** if: (a) Kafka/Redpanda cannot run stably in `syd` within budget,
   **and** (b) prototype scope drops live Kafka log tailing / admin lag, **and** (c) a
   **narrow** streams shim is time-boxed ≤1 week with no generic bus abstraction.

### What *not* to build

- No permanent `QueueBackend` interface "for Temporal later" — Temporal is not a queue backend.
- No dual-write Kafka+Redis.
- No Redis as Track B destination.

### Implications for phased plan (sty_36c0395d)

- Phase Fly deploy with **Kafka (or Redpanda) in the private network** under Option A topology.
- Implementation stories: `fly.toml` process groups + managed Postgres + MinIO/S3 + broker
  machine — **not** "port workers to Redis."
- Optional stretch: drop unused `get_kafka_logs` dependency on deep history if storage logs
  cover restore debugging (independent of Redis).

---

## 5. Summary table for epic parent

| Decision | Value |
|---|---|
| Prototype queue | **Keep Kafka** (KRaft or Redpanda on Fly, or managed) |
| Redis for prototype | **No-go** unless Kafka blocked by ops/cost after try |
| Temporal | Deferred Track B; does not justify Redis interim architecture |
| Abstraction | Env + existing call sites only; no multi-backend framework |

---

*Story sty_d3152eb3 · epic fly-prototype-stack · July 2026*
