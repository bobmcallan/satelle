---
type: document
title: 'Effort estimate and phased plan for Fly prototype deploy'
description: 'Phased plan for full-stack Fly prototype (Option A topology, keep Kafka): milestones MVP→workers→storage→hardening; person-day estimates; dependencies (API contract, SDKs); risks; go/no-go on Redis (no-go).'
tags:
- document
- fly
- plan
- estimate
- solidsafe
- epic:fly-prototype-stack
timestamp: '2026-07-09T00:00:00Z'
---

# Effort estimate and phased plan for Fly prototype deploy

*Assessment for epic `fly-prototype-stack` (story sty_36c0395d).*

**Inputs locked by sibling stories:**

| Input | Decision | Source |
|---|---|---|
| Topology | **Option A — all-on-Fly `syd`**, single engine app + process groups; hybrid Option B = production target later | [[fly-topology-options]] |
| Queue | **Keep Kafka** (KRaft or Redpanda on Fly / managed). **Redis = NO-GO** for prototype unless Kafka blocked after try | [[kafka-vs-redis-fly]] |
| Image | Single image, `SERVICE` role select | [[solidsafe-engine-componentisation-notes]] |
| Temporal | Track B only — not in prototype phases | hybrid straw man |

---

## 1. Phased plan (ordered milestones)

### Phase 0 — Preconditions (parallel with other epics)

| # | Work | Owner area |
|---|---|---|
| 0.1 | Secrets out of `settings.py` defaults → env / `fly secrets`; rotate known leaks | engine hygiene |
| 0.2 | Gateway contract / FE epic progress far enough for identity headers (`DEV=0` path) | epic:api-contract-frontend |
| 0.3 | Confirm private SDK strategy (stubs OK for controlserver smoke; real SDKs for live backup demo) | packaging |
| 0.4 | Fly org + region `syd` + billing + `flyctl` auth | ops |

**Exit:** can put non-secret config in env; know whether demo needs real connectors.

### Phase 1 — MVP: control plane only on Fly

**Goal:** HTTPS `controlserver` + Postgres answering `/docs` and identity-aware reads (with `DEV=1` initially).

| # | Work | Est. |
|---|---|---|
| 1.1 | `fly.toml` for engine app: process `web` only (`SERVICE=controlserver`) | 0.5 d |
| 1.2 | Fly Postgres (or equivalent) + `SQLALCHEMY_DATABASE_URL` | 0.5 d |
| 1.3 | Alembic migrate / seed minimal user for gateway or `force_user_id` | 0.5 d |
| 1.4 | Deploy image from `scripts/Dockerfile`; health check; secrets | 1 d |
| 1.5 | Smoke: `/docs` 200, simple authenticated read path | 0.5 d |

**Not required yet:** Kafka, workers, MinIO, scheduler.

**Exit:** public (or private) URL for control API; DB durable.

### Phase 2 — Workers + Kafka (full backup path topology)

**Goal:** Enqueue task → taskrunner → jobrunner path works against Fly-hosted broker.

| # | Work | Est. |
|---|---|---|
| 2.1 | Broker: Kafka KRaft **or** Redpanda private app/machine; `KAFKA_BOOTSTRAP` | 1–2 d |
| 2.2 | Process groups: `taskrunner`, `jobrunner`, `scheduler` (count=1), later `logreceiver` | 1 d |
| 2.3 | Wire compose-parity env; verify run-once / PARALLELISM loop on Fly | 1 d |
| 2.4 | End-to-end dry run with stub or one connector (task enqueue + status) | 1–2 d |

**Exit:** scheduler or manual run creates jobs; status visible in DB/API.

### Phase 3 — Object storage

**Goal:** Backup bytes land in object store (prototype MinIO volume or S3 API).

| # | Work | Est. |
|---|---|---|
| 3.1 | MinIO app + volume **or** attach S3-compatible store; secrets | 1 d |
| 3.2 | Destination config in seed data; jobrunner write smoke | 1 d |
| 3.3 | Document **non-sovereign** prototype storage disclaimer | 0.25 d |

**Exit:** one successful job writes objects; logs recoverable.

### Phase 4 — Hardening for shared prototype URL

| # | Work | Est. |
|---|---|---|
| 4.1 | `DEV=0` + `GATEWAY_SHARED_SECRET` + network isolation (engine not customer-direct) | 1 d |
| 4.2 | Optional: `clientauthserver` / `restoreserver` if demo needs OAuth/restore | 2–4 d (scope cuttable) |
| 4.3 | CI: build image, optional `fly deploy` on main/tag | 1–2 d |
| 4.4 | Operator runbook: scale process groups, restart broker, backup Postgres | 0.5 d |

**Exit:** gateway-only access posture; deploy repeatable.

### Phase 5 — Out of scope for this plan (explicit)

- Kafka → Redis rewrite
- Temporal / KEDA
- Production AU Object Lock sovereignty (Option B cutover)
- Full multi-tenant product polish beyond gateway contract

---

## 2. Person-day summary

| Phase | Optimistic | Likely | Pessimistic | Size |
|---|---|---|---|---|
| 0 Preconditions | 1 | 2 | 4 | S–M |
| 1 MVP web+Postgres | 2 | 3 | 5 | M |
| 2 Workers+Kafka | 4 | 6 | 10 | M–L |
| 3 Storage | 2 | 2.5 | 4 | S–M |
| 4 Hardening + CI | 2.5 | 4 | 8 | M |
| **Total (engine Fly path)** | **~11.5** | **~17.5** | **~31** | **L** |

**Redis alternative (rejected):** add **+6–16 person-days** to Phase 2 for adapter work ([[kafka-vs-redis-fly]]) — not in baseline.

**Calendar (one engineer, part parallel with FE):** ~2–3 weeks likely for Phases 1–3; Phase 4 depends on gateway readiness. Fits inside a 6-week prototype if FE/gateway is parallel and restore is scoped tightly.

---

## 3. Dependencies and risks

### Dependencies

| Dependency | Blocks | Mitigation |
|---|---|---|
| API contract / gateway headers | Phase 4 shared URL; multi-tenant demo | Phase 1–3 can use `DEV=1` |
| Real connector SDKs (vs stubs) | Live backup demo in Phase 2–3 | Stubs for boot; vendor SDKs before customer demo |
| Fly account / syd capacity | All phases | Confirm early (Phase 0) |
| Secrets rotation | Any non-private deploy | Phase 0.1 hard gate before shared URL |
| solidsafe-app FE | End-to-end self-service story | Engine phases independent until Phase 4 |

### Risks

| Risk | Impact | Mitigation |
|---|---|---|
| Kafka memory/cost on Fly | Budget / OOM | Prefer Redpanda; single-node; managed fallback — **not Redis first** |
| Sovereignty messaging confusion | Sales/compliance | Label prototype non-sovereign; Option B later |
| `schedulerunner` double-fire | Duplicate backups | Process count = 1 always |
| Hardcoded secrets | Incident | Phase 0.1 before public |
| Scope creep (restore, all 8 roles) | Blow 6 weeks | MVP = web+workers+storage; restore optional Phase 4.2 |
| Kafka→Redis reopen mid-flight | Delay | Only if Phase 2.1 fails ops criteria after measured try |

---

## 4. Go / no-go: Kafka → Redis

| Decision | **NO-GO on Redis for implementation stories** |
|---|---|
| Do implement | Kafka (or Redpanda/managed Kafka) under Option A |
| Do not cut stories for | Redis streams adapter, multi-backend queue framework |
| Reopen criteria | Documented Kafka ops failure on Fly (cost/stability) after Phase 2.1 attempt **and** time-box ≤1 week for **narrow** streams shim (work queues only) |

This is the gate for cutting follow-on implementation stories after this epic closes.

---

## 5. Suggested follow-on implementation stories (after epic)

Cut only after this assessment is accepted:

1. `fly.toml` + deploy controlserver + Fly Postgres (Phase 1)
2. Private Kafka/Redpanda + worker process groups (Phase 2)
3. MinIO/S3 destination on Fly (Phase 3)
4. DEV=0 + gateway secret wiring (Phase 4.1)
5. CI image build / deploy (Phase 4.3)

Do **not** cut: "Replace Kafka with Redis."

---

## 6. One-page recommendation

Ship the Fly prototype as **all-on-Fly (syd), keep Kafka, phase web→workers→storage→harden**. Budget **~15–20 person-days** likely for the engine path. Treat sovereignty as a **later cutover** (Option B), not a prototype blocker. Temporal stays Track B.

---

*Story sty_36c0395d · epic fly-prototype-stack · July 2026*
