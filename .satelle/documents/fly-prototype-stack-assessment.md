---
type: document
title: 'Fly.io full prototype stack — architecture and effort assessment'
description: 'Epic rollup for fly-prototype-stack: preferred all-on-Fly topology, keep Kafka (Redis no-go), Temporal deferred, phased plan with ~15–20 person-day likely engine path. Links child assessments.'
tags:
- document
- fly
- architecture
- assessment
- solidsafe
- epic:fly-prototype-stack
timestamp: '2026-07-09T00:00:00Z'
---

# Fly.io full prototype stack — architecture and effort assessment

*Epic parent rollup (`sty_30619a02` / `epic:fly-prototype-stack`). Child assessments:*

| Order | Story | Document |
|---|---|---|
| 1 | Topology options | [[fly-topology-options]] |
| 2 | Kafka vs Redis | [[kafka-vs-redis-fly]] |
| 3 | Phased plan + effort | [[fly-prototype-phased-plan]] |

Context: [[solidsafe-hybrid-strawman]], [[solidsafe-engine-componentisation-notes]].

---

## Decisions

| Topic | Call |
|---|---|
| **Topology** | **Option A — full prototype stack on Fly (`syd`)**: one engine app with process groups; Fly Postgres; private MinIO or S3 API; private broker. Hybrid (control on Fly / bytes on-prem) remains **production** target, not prototype path. |
| **Queue** | **Keep Kafka** (self-host KRaft, Redpanda, or managed). Same `KAFKA_BOOTSTRAP` contract as `scripts/docker-compose.yml`. |
| **Redis** | **NO-GO** for prototype implementation stories. Reopen only if Kafka fails ops/cost criteria after a real Fly attempt, with a time-boxed narrow shim (no permanent queue framework). |
| **Temporal** | **Deferred** to Track B rewrite — not an interim Redis design driver. |
| **Image model** | Unchanged: single image, `SERVICE` role select. |
| **Sovereignty** | Prototype deploy is **non-sovereign** if bytes sit on Fly/MinIO. Do not market as AU Object Lock product topology. |

---

## Why this path

1. **Delivery speed** — epic decision was full stack on Fly for the 6-week prototype.
2. **Compose parity** — lift existing packaging rather than redesign orchestration mid-demo.
3. **Kafka already load-bearing** — task/job queues, status/log pipelines, admin lag; Redis port is 6–16 person-days of risk for a throwaway bus.
4. **Temporal later** — a polished multi-backend queue is dead-end work; keep the spine until Track B replaces orchestration properly.

---

## Effort (engine Fly path)

| | Optimistic | Likely | Pessimistic |
|---|---|---|---|
| Person-days | ~11.5 | **~17.5** | ~31 |
| Calendar (1 eng, FE parallel) | ~2 wk | **~2–3 wk** | ~5 wk |

Phases: **0 preconditions → 1 web+Postgres → 2 workers+Kafka → 3 storage → 4 harden/CI**.

---

## Risks (top)

- Kafka machine cost/OOM → Redpanda or managed Kafka, not Redis-first.
- Secrets still hardcoded → rotate before shared URL.
- Sovereignty narrative leak → label prototype explicitly.
- Gateway/FE lag → Phases 1–3 can use `DEV=1`; Phase 4 needs contract epic.

---

## Implementation stories to cut next (not this epic)

1. fly.toml + controlserver + Fly Postgres  
2. Private Kafka/Redpanda + worker process groups  
3. MinIO/S3 on Fly  
4. DEV=0 + gateway secret  
5. CI build/deploy  

**Do not cut:** Replace Kafka with Redis.

---

## Acceptance map (epic parent)

| AC | Satisfied by |
|---|---|
| Child stories cover topology, Kafka/Redis, effort, recommendation | sty_a7bb4f0d, sty_d3152eb3, sty_36c0395d **done** |
| Assessment under `.satelle/documents` | This file + three children |
| Explicit Kafka vs Redis call; Temporal deferred | Keep Kafka; Redis no-go; Temporal Track B |
| Effort ranges for chosen path | ~15–20 person-days likely |
| All children done before epic closes | Required at parent close |

---

*Epic fly-prototype-stack · July 2026*
