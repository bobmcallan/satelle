---
type: document
title: 'SolidSafe engine — componentisation & Go-port assessment (working notes)'
description: 'Discussion notes on packaging the backup engine as componentised docker images (local compose → fly.io), the scaling/scheduling model, and why a Go port is not warranted. Grounded in a read of the solidsafe_engine (legacy rainbow_wizard) codebase.'
tags:
- document
- solidsafe
- solidsafe-engine
- architecture
- componentisation
- go-port
- decisions
timestamp: '2026-07-09T00:00:00Z'
---

# SolidSafe engine — componentisation & Go-port assessment

*Working notes from a repo review of `solidsafe-engine` (package dir `solidsafe_engine/`,
legacy product name rainbow_wizard), against the goal of moving to componentised docker
images that run locally first and then deliver on fly.io. Related:
component breakdown notes; [[solidsafe-hybrid-strawman]].*

---

## 1. Objective

Move the backend/admin to **componentised docker images**. Sequence: **run locally
first** (docker-compose), **then enable fly.io** delivery. Archived under
`xArchive/`: former `k8s/` and `rainbow_wizard_container/`. Active packaging: `scripts/`.

## 2. Decisions captured this session

- **Componentisation = single image, role-selected** (not 8 separate images). The engine
  already dispatches on a `SERVICE` env var into 8 roles; all roles share `core/` + every
  connector, so separate images would ship the same dependencies N times. One image + a
  role selector maps 1:1 to both docker-compose services and fly `[processes]` groups.
- **Broker: keep Kafka initially.** Redis is likely the right long-term move (simpler,
  cheap on fly), but the swap needs real volume/throughput assessment first, and may
  coincide with a Go port of the worker tier. Not now.
- **Stateful services live outside the image** — Postgres (and later Redis) are provided
  externally: by compose locally, managed/attached on fly.
- **Milestone order:** local compose first (Postgres + Kafka + MinIO + engine roles,
  `.env`, `DEV=1` to skip Keycloak). fly.toml after, resolving the broker question then.
- **Connector SDKs:** vendor the 4 tokenised git-dep SDKs (Pipedrive v1/v2, Xero PM,
  Smartsheet) into the repo. They are in-process Python libraries, not separable services.
- **Go port: not now** (see §5).
- **Microservice granularity:** by existing role, no restructuring. True microservices
  would require carving `core/` into a shared package — deferred.

## 3. The 8 services and how they scale

Each role is a thin entrypoint over a shared, heavy `core/` (~5,751 LOC). Scaling rules
differ per role — this is what a componentised deploy must encode:

| Service (`SERVICE=`) | Entrypoint | Kind | Scaling |
|---|---|---|---|
| `controlserver` | `server.py` (1,067 LOC) | FastAPI admin/control API + frontend | N behind LB (stateless) |
| `restoreserver` | `restorefrontend.py` | FastAPI — the irreversible write-back path | N behind LB |
| `clientauthserver` | `clientauthserver.py` | FastAPI — connector OAuth | N behind LB |
| `taskrunner` | `eventconsumer.py` (task-queue) | Kafka worker — enumerates items, fans out jobs | N via consumer group (≤ partitions) |
| `jobrunner` | `eventconsumer.py` (job-queue) | Kafka worker — runs the actual backup | N via consumer group (≤ partitions) |
| `schedulerunner` | `schedulerunner.py` | timer sweep | **singleton — exactly 1** |
| `updaterunner` | `update-receiver.py` | Kafka worker — object-change → DB | N via consumer group |
| `logreceiver` | `log-receiver.py` | Kafka worker — job-log/status → DB | N via consumer group |

**Is the single image scalable across machines? Yes** — the concurrency model is already
built in:
- Servers are stateless (state in Postgres/Kafka) → horizontal.
- Workers use Kafka **consumer groups** (`group_id='executor'`); instances split partitions
  and load-balance automatically — exactly how KEDA ScaledJobs scale them today. Bounded by
  partition count. Each worker consumes one event, runs it, exits (run-once model).
- The **scheduler is a singleton** — two would double-fire tasks. Deploy count=1.

## 4. Scheduling & execution model

Scheduling is a **dedicated service**, not part of the frontend. Execution is **event-driven
via Kafka**, fully decoupled from enqueue.

```
schedulerunner (timer sweep)  ─┐
   TaskModels due                  ├─► crud.run_taskmodel() ──► task-queue (Kafka)
controlserver ("run now")      ─┘
                                       │
                       taskrunner ◄────┘   run_task(): enumerate items via connector
                          │  fan-out: one message per item
                          ▼
                       job-queue (Kafka)
                          │
                       jobrunner ──► backend().backup(job).run()   ← actual transfer
                          │  status / logs
                          ▼
                job-status / job-log ──► logreceiver ──► Postgres
```

- `schedulerunner.py` sweeps `TaskModel`s with a non-manual schedule, compares last-run vs
  period (hourly / 8-hourly / daily / weekly), and enqueues if due.
- The **frontend also enqueues** on demand (user "create point" → `server.py` →
  `crud.run_taskmodel`) but **never executes** — it is just another producer.
- **Execution is always the worker tier.** A task fans out to N jobs (one per
  mailbox/drive/etc.). That decoupling is what makes workers independently scalable.

## 5. Go-port assessment — not warranted

A Go port is **not simple**; it re-does the highest-risk, highest-leverage parts.

- **Load-bearing reason — vendor SDK availability.** Xero, Pipedrive, Smartsheet, Mailchimp
  have **no first-class Go SDKs**; the devs have vendored their own **Python** SDK wrappers.
  Porting means hand-writing REST clients (or trusting shaky community Go SDKs) per vendor —
  re-doing exactly the connector correctness the strategy docs classify as irreversible /
  hand-code / never-AI-slop. Python is where the ecosystem is.
- **Crypto.** `rclone_crypt/` (712 LOC) is a from-scratch reimpl of rclone's encryption
  (EME, name cipher, base32768, scrypt/nacl). Correctness-critical; a Go redo is pure risk.
- **Shared coupling.** `core/rainbowizard.py` does `from .backends import *`, so every
  service eagerly loads every connector SDK. Nothing is independently portable until that
  wildcard is broken.

**Where a Go beachhead *could* live:** the **plumbing tier** — `schedulerunner` /
`updaterunner` / `logreceiver` (~740 LOC, Postgres + Kafka only, zero vendor SDKs). These
are portable *because they talk to the DB/queue directly*. Prerequisite: break the
`from .backends import *` coupling so plumbing decouples from connectors; then plumbing
could go Go while connectors stay Python behind a stable `JobEvent` / `Backend` interface.
Not a whole-engine port.

## 6. Where the custom dev effort actually went

The engineering is **concentrated and leveraged** — not six hand-written integrations:

| Area | What was built | LOC |
|---|---|---|
| Job/task engine | `core/rainbowizard.py` — `JobEvent`: status/metric emission, incremental "point" logic, encrypted upload, change-logging, job fan-out, billing metrics. The heart. | ~828 |
| Connector framework | `core/backends/backend.py` (abstract Backend/Backup/Auth + retry/backoff/async) **+ `openapibackend.py`** — a generic, OpenAPI-introspecting connector that backs up data types by reflecting over an SDK's API methods. | ~510 + base |
| Per-vendor adapters | `xero.py`, `pipedrive.py`, `smartsheet.py`, `mailchimp.py`, `xeropm.py` — **thin mappings** onto the generic framework (~250–280 each). Not from-scratch integrations. | ~250 ea |
| rclone-compatible crypto | `rclone_crypt/` — native read/write of rclone-encrypted stores. | 712 |
| Domain model + servers | `models.py` / `crud.py` (Task→Job→Point model, tenant groups, alembic), servers, scheduler, log-receiver, Zabbix monitoring. | ~4k |
| Google / Workspace | `gwbackupy/` — **vendored open-source**, adapted (not fully custom). | 2,369 |

**Headline:** the devs built (1) an orchestration engine, (2) a *generic connector framework*
onto which each vendor is a thin adapter, and (3) rclone-compatible crypto — then leveraged
an OSS project for Google. This validates the "wrap the engine, don't rebuild" thesis: the
custom value sits exactly in the framework + engine + crypto (the irreversible, hand-code
parts), which is also the clearest reason not to port to Go.

## 7. Build blockers to clear before the first image builds

1. **`requirements.txt` embeds live GitLab PAT tokens** (`glpat-…`) on 4 connector git deps —
   leaked credentials **and** the `pip install` fails off their network. Vendor the SDKs.
2. **`settings.py` hardcoded production secrets** (Xero/Pipedrive/Smartsheet/Mailchimp/
   Keycloak/Zabbix) — move to `.env` / fly secrets, rotate. (Already in README known-issues.)
3. **Kafka on fly.io** — no KEDA; deferred to the fly milestone with the broker decision.

## 8. Next step

Local compose first: root `Dockerfile` (fix the git deps), `docker-compose.yml`
(Postgres + Kafka + MinIO + engine roles), `.env.example`, `DEV=1` to skip Keycloak — get
`controlserver` + a backup job round-tripping locally. Then `fly.toml` process groups from
the same image, resolving the broker question.
