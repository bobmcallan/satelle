---
title: SolidSafe — Prototype vs Rewrite: the Hybrid Straw Man
type: document
description: Working-session straw man for how the 6-week prototype and the rewrite coexist — two tracks, one API contract.
---

# SolidSafe — Prototype vs Rewrite: the Hybrid Straw Man

Working session · 9 July 2026

> **This is a straw man of how the 6-week prototype and the rewrite coexist.** It states one structure — **two tracks, one contract** — so we can argue with it today. React to the **structure**: is the contract in the right place, and who holds the pen on the hand-code spine? If a boundary is wrong, that's the conversation.

## 1. The shape

**We ship the prototype on the wrapped existing engine + a new front end, and let the rewrite proceed in parallel behind the same contract.**

The 6-week prototype does not wait for, and is not built on, the rewrite. It rides **rainbow_wizard as-is** — the six live connectors that already work — with a **new self-service front end** on top. Beth's **saas_backup rewrite** continues on its own timeline. Both sides implement **one API contract**. The front end is the durable asset; the engine behind the contract is swappable. The prototype validates the commercial bet — including whether customers actually use granular restore — *before* the platform is bet on.

**Break it:** is the contract genuinely stable enough that the rewrite lands behind it with *no* change to the front end — or does the old engine's shape leak through the seam and make the 6 weeks throwaway?

### Stack (planes)

| Plane | Kind | What |
|-------|------|------|
| Self-service front end | **new · the deliverable** | connect service · schedule · create point · backup status · restore — written once, survives the engine swap |
| API contract (`OpenAPI over the gateway`) | **new · critical** | the front end binds here, never to engine internals — **the boundary that makes the two tracks independent** |
| Track A — Wrapped engine `rainbow_wizard` | **reused · NOW** | 6 live connectors · workers · storage — implements the contract today, unchanged |
| Track B — Rewrite `saas_backup` | **new · LATER · parallel** | Temporal · three-layer query · encryption — implements the *same* contract when ready |
| On-prem sovereign data plane | **foundational** | AU · on-prem · customer backup bytes never leave — front end + gateway on fly see **metadata only** |

## 2. The prototype: from → to

The 6 weeks is a **thin new layer on an unchanged spine**, not a rebuild. Everything below the contract is what runs today; the delta is the top two planes.

### From — today · internal single-operator tool

- **Admin console** — engine-served; one operator sees all customers, full access
- **Keycloak auth** — internal login
- **Engine + 6 connectors + Kafka workers** — rainbow_wizard
- **Sovereign storage** — AU, on-prem

Deployed entirely **on-prem (k8s)**. No tenancy, no self-service, no customer portal.

### To — 6 weeks · wrapped multi-tenant SaaS

- **Customer self-service front end** — connect · schedule · create point · status · restore
- **Gateway · per-customer isolation · API contract** — the new tenant boundary
- **Engine + 6 connectors + Kafka workers** — *unchanged*
- **Sovereign storage** — AU, on-prem

Split: **control plane on fly** (metadata) · **data plane on-prem** (bytes). Customers self-serve.

The two **reused/foundational** planes are identical on both sides — the prototype adds only the **front end** and the **gateway/contract**. That is the whole build, and it is what makes 6 weeks real.

## 3. The boundaries to argue about

### 1. The contract is the whole game

The front end binds to the **OpenAPI contract**, never to the existing engine's endpoints. Get this right and the rewrite lands by repointing the gateway — the UI is untouched. Get it wrong and the rewrite forces a UI rebuild — *the two tracks collided and the 6 weeks were throwaway.* Agree the front-end↔engine seam is the one thing we hold stable, and that it's the gateway boundary we already have?

### 2. The prototype rides the six real connectors — not HubSpot / Salesforce

The rewrite's staged plan — and its own architecture diagrams (§4) — open on **HubSpot · Salesforce · Zendesk**. Our revenue runs on **Google · Mailchimp · Pipedrive · Smartsheet · Xero · Xero PM**. The prototype must demo on the connectors customers *use* — which is exactly what wrapping the existing engine gives us for free. Open question for the rewrite track: where do the six live connectors enter its plan?

### 3. fly is the control plane; on-prem is the data plane

Front end + gateway on **fly** (`syd`) — schedules, status, metadata. Engine + customer bytes stay **on-prem**. Sovereignty stays a storage-plane property; fly never touches backup content, so its US domicile reaches only control metadata. Agree the data/control split is where the line goes?

> **Update (decision, July 2026):** full prototype stack may run on **fly.io** for delivery speed; that may force Kafka → Redis (or similar) for queueing, with **Temporal as the end-goal orchestrator**. That is a deliberate prototype topology exception to the pure control/data split — assess architecture and effort explicitly before coding.

### 4. Isolation and restore are the hand-code spine — senior-owned

Two pieces of the wrap are **not** AI-code: **tenant isolation** (every query scoped by `user.groups` — the boundary that turns the super-admin tool multi-tenant) and, if in scope, the **single guarded restore path** (irreversible · dual-approval · round-trip tested). Everything else behind the contract is AI-buildable, design-curated.

### 5. The real collision is the senior's time — not the code

The tracks are independent in code and deadline. They are *not* independent in people: two devs, and both the wrap's hand-code spine (§4) and the rewrite need the senior. **This is the decision the session exists to make** — either Beth splits (a few days on the wrap spine, the rest on the rewrite), or junior + AI carry the wrap with Beth reviewing only isolation and restore. Name it, or it silently becomes the thing that blows the 6 weeks.

## 4. The rewrite destination — deferred behind the contract

These are the two architecture diagrams from `SolidSafe_Architecture_working_doc.docx` (§2.1, §2.3). They are **Track B's destination — Beth's target**, reached one layer at a time behind the contract *after* the commercial bet is validated. The prototype does *not* become this in 6 weeks. Note that even here the connector layer names HubSpot / Salesforce / Zendesk, not the six we run — the §3.2 question, in picture form.

### Rewrite · system architecture (logical layers)

Web app (Next.js) / API clients / Auth → API layer (FastAPI) → Event streaming (Kafka/Redpanda) + Orchestration (Temporal, open) → Integration connectors → Encryption → Storage three tiers (raw JSON MinIO · Parquet/Iceberg · pgvector) → Metadata index (PostgreSQL) → SQL/DuckDB + semantic search → Security overlay → Observability / load testing.

*Working-doc §2.1. Diagrams live in the architecture doc / original HTML figure assets.*

### Rewrite · workflow orchestration (Temporal)

Scheduler / Customer UI / Kafka events → API service (FastAPI + Clerk) → Temporal server (+ KEDA) → Worker service (backup / restore / erasure workflows) → Connector layer → SaaS APIs; Redis rate-limit; Vault; three-layer storage; Postgres metadata.

*Working-doc §2.3.*

### What the prototype defers to this track

None of the following moves a boundary in §1–§3, so none blocks the 6 weeks:

- Three-layer query — Parquet / Iceberg / DuckDB / pgvector
- Envelope encryption / BYOK / WORM / GDPR erasure
- Canonical schema / app-to-app migration
- Temporal / KEDA orchestration (end-goal; prototype may use lighter queues first)
- Entity resolution · AI search · webhooks

---

Regards,  
Bob

SolidSafe / Cybersecure · hybrid straw man for the working session — not a commitment · diagrams §4 from SolidSafe_Architecture_working_doc.docx
