---
type: document
title: 'Fly topology options for full prototype stack'
description: 'Maps scripts/compose services onto Fly.io: apps/machines, private networking, volumes, regions (syd), secrets. Compares all-on-fly vs fly-control+remote-data (and a split-API variant). Names preferred topology for the 6-week prototype and sovereignty tradeoffs.'
tags:
- document
- fly
- architecture
- topology
- solidsafe
- epic:fly-prototype-stack
timestamp: '2026-07-09T00:00:00Z'
---

# Fly topology options for full prototype stack

*Assessment for epic `fly-prototype-stack` (story sty_a7bb4f0d). Grounded in
`scripts/docker-compose.yml`, `scripts/Dockerfile` / `service-main.sh`, and
[[solidsafe-engine-componentisation-notes]] / [[solidsafe-hybrid-strawman]].*

---

## 1. Starting point (local compose)

| Compose unit | Role | Stateful? | Notes |
|---|---|---|---|
| `postgres` | Metadata / control DB | Yes | Task/job/point models, tenants via `user.groups` |
| `kafka` | Task/job/status/log/object-change bus | Yes (ephemeral OK for prototype) | Topics: `task-queue`, `job-queue`, `task-status`, `job-status`, `task-log`, `job-log`, `object-change` |
| `minio` | S3-compatible object store | Yes | Stand-in for sovereign AU storage plane |
| `controlserver` | FastAPI control API (`SERVICE=controlserver`) | No | Public HTTP; host port 5000 → 80 |
| `taskrunner` | Kafka worker (`CONSUMER_TOPIC=task-queue`) | No | Enumerate items → fan-out jobs |
| `jobrunner` | Kafka worker (`CONSUMER_TOPIC=job-queue`) | No | Actual backup transfer |

**Image model (already decided):** one app image, role via `SERVICE` env — maps 1:1 to
Fly `[processes]` or separate machine groups without rebuilding.

**Roles not in compose yet but same image:** `restoreserver`, `clientauthserver`,
`schedulerunner` (singleton), `logreceiver`, `updaterunner`.

---

## 2. Option A — All-on-Fly (full prototype stack)

**Intent:** Run everything needed for a demoable prototype in Fly region `syd`
for delivery speed. Explicit exception to the hybrid straw man's "bytes stay
on-prem" production shape ([[solidsafe-hybrid-strawman]] §3.3 update).

### Service → Fly unit map

| Unit | Fly placement | Scale | Public? |
|---|---|---|---|
| App image processes: `controlserver` | App `solidsafe-engine` process group `web` | 1–2 machines | **Yes** (HTTP, TLS via Fly) |
| `taskrunner` | Same app process group `taskrunner` | 1 (prototype) | No — 6PN only |
| `jobrunner` | Same app process group `jobrunner` | 1–2 | No |
| `schedulerunner` | Process group `scheduler` | **exactly 1** | No |
| `logreceiver` / `updaterunner` | Process groups or share a `workers` machine | 1 each | No |
| `restoreserver` / `clientauthserver` | Optional process groups when restore/OAuth in scope | 1 | Optional public or private |
| Postgres | **Fly Postgres** (managed) or single machine + volume | Managed preferred | No — attach / private |
| Object store | MinIO on machine + volume **or** Tigris/S3-compatible in-region | 1 volume | No |
| Broker | Kafka KRaft on large machine **or** Redis (see sty_d3152eb3) **or** managed Kafka | 1 (prototype) | No |

**Recommended shape for Option A (prototype):**

1. **One Fly app** for the engine image with process groups:
   `web`, `taskrunner`, `jobrunner`, `scheduler` (+ log/update when needed).
2. **Fly Postgres** attached (or `fly postgres create` in same org/region).
3. **MinIO machine** (separate small app + volume) *or* external S3 API with keys
   in secrets — avoid putting multi-GB backup objects on the web machine.
4. **Broker:** defer final pick to [[kafka-vs-redis-fly]] — topology must reserve
   either a ~2GB+ Kafka machine or a small Redis (Upstash / Fly Redis).

```
                    Internet
                        │
                        ▼
              ┌─────────────────┐
              │ controlserver   │  fly process web (syd)
              │  (public HTTP)  │
              └────────┬────────┘
                       │ 6PN / .internal
        ┌──────────────┼──────────────────┐
        ▼              ▼                  ▼
   Postgres      Broker (Kafka/Redis)   MinIO/S3
   (managed)     (private)              (volume or managed)
        ▲              ▲
        │              │
   taskrunner / jobrunner / scheduler / logreceiver
   (private process groups, same image)
```

### Network model

- **Public:** only `controlserver` (and later `solidsafe-app` gateway/UI, separate app).
- **Private:** all workers, DB, broker, MinIO via Fly private networking
  (`*.internal`, org WireGuard for ops).
- **Gateway path (when wired):** external clients hit gateway app →
  `X-SolidSafe-User` + optional `X-SolidSafe-Gateway` → engine `web` over private
  HTTP. Engine never customer-reachable in production posture (`DEV=0`).

### Secrets model

| Secret / config | Source on Fly |
|---|---|
| `SQLALCHEMY_DATABASE_URL` | Fly Postgres connection string |
| `KAFKA_BOOTSTRAP` or Redis URL | Private host:port of broker |
| `MINIO_*` / S3 credentials | `fly secrets` |
| OAuth connector client secrets | `fly secrets` (rotate off hardcoded `settings.py` defaults first) |
| `GATEWAY_SHARED_SECRET` | Shared with gateway app secrets |
| `DEV` | `0` for any shared prototype URL; `1` only for private smoke |
| Session / encryption keys | `fly secrets` |

Image stays secret-free; compose `.env` pattern maps to `fly secrets set`.

### Sovereignty / metadata-vs-bytes

| Risk | Severity for Option A |
|---|---|
| Backup **bytes** land in Fly/MinIO in `syd`, not customer on-prem Object Lock | **High** for production claims; **accepted for prototype** if messaging is "demo topology, not sovereign product" |
| Control **metadata** (schedules, job status, user/groups) on Fly Postgres | Medium — already assumed for control plane in hybrid model |
| US-domiciled operator (Fly) processes metadata and possibly bytes | High for sales narrative — must not market this deploy as AU-sovereign |
| Object Lock COMPLIANCE / immutability | Not provided by default MinIO-on-volume — do not claim WORM |

**Verdict:** fine for **internal prototype / sales demo of product UX**, not for
sovereignty certification or customer production data.

---

## 3. Option B — Fly control + remote/on-prem data plane (hybrid)

**Intent:** Match the original hybrid straw man: front end + gateway (+ optionally
controlserver metadata) on Fly; engine workers + object bytes on-prem AU.

### Service → Fly unit map

| Unit | Placement |
|---|---|
| `solidsafe-app` FE + gateway | Fly `syd` (public) |
| `controlserver` | **Either** Fly private (metadata-only if storage is remote) **or** on-prem next to workers |
| Workers (`taskrunner`, `jobrunner`, …) | On-prem (existing k8s or compose) |
| Postgres | On-prem (or Fly if controlserver is on Fly — then workers need secure reachability) |
| Kafka | On-prem next to workers |
| Object store | On-prem sovereign AU + Object Lock |

### Network model

- Public Fly: UI + gateway only.
- Private path gateway → controlserver (WireGuard / Fly private network / VPN /
  Cloudflare Tunnel / tailnet) — must not expose engine to the internet.
- Workers never need inbound public ports; they pull from Kafka and push to storage.

### Secrets model

- Gateway secrets on Fly; engine secrets on-prem secret store.
- Shared `GATEWAY_SHARED_SECRET` coordinated across the tunnel.

### Sovereignty

| Risk | Severity |
|---|---|
| Customer backup bytes stay on-prem AU | **Low** — matches product story |
| Metadata on Fly if controlserver/Postgres stay on Fly | Medium — disclose as control metadata only |
| Operational complexity (two environments, tunnel, dual deploy) | High for a 6-week slice |

**Verdict:** correct **production** topology; heavier for a first prototype if
on-prem flywheel is not already warm.

---

## 4. Option C — Split API (brief)

**Intent:** Public `controlserver` (or gateway only) on Fly; workers + Kafka +
MinIO + Postgres entirely on a single "data" Fly app or a remote VM.

- Slightly cleaner blast-radius than A (web scale ≠ worker scale).
- Still puts bytes on Fly if data app is on Fly → same sovereignty caveat as A.
- Extra app wiring vs single multi-process app; little gain for prototype vs A.

**Not preferred** for 6-week prototype unless web and workers need independent
deploy cadence early (they don't).

---

## 5. Comparison

| Criterion | A All-on-Fly | B Hybrid | C Split API |
|---|---|---|---|
| Time-to-demo | **Best** | Worst | Medium |
| Ops complexity | Medium (broker size) | High (two planes) | Medium–high |
| Sovereignty story | Weak (must label prototype) | **Strong** | Weak if data on Fly |
| Matches existing compose | **Yes** (lift) | Partial | Partial |
| Path to Track B Temporal | Replace broker/workers later behind API contract | Same | Same |

---

## 6. Preferred topology for prototype

**Preferred: Option A — All-on-Fly in `syd`, single engine app with process groups,
managed Postgres, private MinIO (or S3 API), broker choice pending sty_d3152eb3.**

### Rationale

1. **Delivery speed** — decision context for this epic is explicitly "full prototype
   stack on fly for delivery speed."
2. **Parity with `scripts/`** — compose already runs the full path; Fly is a
   re-host of the same image + env, not a redesign.
3. **Single image / multi-process** — already the componentisation decision; maps
   cleanly to Fly process groups without multi-image CI.
4. **Sovereignty is deferred honestly** — hybrid (Option B) remains the production
   target; prototype docs and demos must not claim on-prem Object Lock for this
   deploy.
5. **Gateway/FE stay separate apps** when `solidsafe-app` exists — they sit in front
   of `web`; this engine repo only owns the engine app + data dependencies.

### Explicit non-goals for this topology choice

- Not choosing Kafka vs Redis (child sty_d3152eb3).
- Not implementing `fly.toml` in this story (phased plan sty_36c0395d).
- Not claiming production sovereignty.

### Open dependency

Broker placement (Kafka machine vs Redis/Upstash vs managed Kafka) **changes
machine sizing and cost** under Option A but **does not change** the preferred
app/process layout or the public/private network cut.

---

## 7. Prototype guardrails (carry into phased plan)

1. Label the Fly stack **prototype / non-sovereign** in README and operator notes.
2. `DEV=0` + gateway secret before any shared URL; never customer-direct to engine.
3. Rotate secrets out of `settings.py` before any shared deploy.
4. Keep Object Lock / AU on-prem path on the roadmap as Option B cutover.
5. `schedulerunner` must remain singleton (process count = 1).

---

*Story sty_a7bb4f0d · epic fly-prototype-stack · July 2026*
