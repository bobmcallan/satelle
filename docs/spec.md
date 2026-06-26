# Satelle — V6 Spec

Satelle is the V6 rebrand and open-core restructure of the `satellites` product.
"Satelle" (domain `satelle.net`) keeps the satellites lineage — *satelle* is the
Latin/French root of "satellite".

## Open-core model

One binary, **pluggable backend**. A single storage/backend interface has two
implementations; paid features light up when connected/licensed. No fork, no build
tags.

| Tier | What it is | How it's reached |
|------|------------|------------------|
| **OSS / standalone** | CLI-only. Local DB holding the work primitives. Single-user, zero server dependency. | Default — the local backend. |
| **Paid / hosted** | The server tier: centralised MCP, shared documents, multi-user stories/projects, reviewers, the portal, embedded/semantic search. | The server-client backend, activated by config/license. |

**MCP is the dividing line** — it lives only on the paid server. The OSS build is
CLI-only.

## OSS / local tier

Same setup surface as current satellites, but executes **100% locally** — no remote
or online database connection.

- **Install:** global, like satellites; executable inside a repo.
- **Primitives:** documents · stories · workflows · tasks · principles · ledger.
- **Config:** at the repo level — `.satelle/satelle.toml`, mirroring `.satellites/`.
- **`--local` flag:** kept, as in satellites.
- **Auth:** none for local-only operations.

### Database

- **Engine:** SQLite via the **pure-Go `modernc.org/sqlite`** driver (no cgo).
  Decided by precedent: satellites already uses this exact driver for its local
  `.satellites/index.db` and `.satellites/state.db` — so the port reuses a working
  pattern and keeps the single static binary (no native deps).
- **Not libSQL/Turso (for now):** libSQL is SQLite-compatible and would natively give
  the future sync (embedded replicas) and vector-search (paid embedded docs)
  features — but its embedded path needs cgo, regressing the no-cgo install for a
  benefit that's OFF in the MVP. Keep all SQL **libSQL-compatible** so adopting it
  later is a driver swap behind the backend interface, not a rewrite.
- **Location:** **per-repo, local** — `.satelle/satelle.db` next to `.satelle/satelle.toml`.
  *Not* global-with-binary. Data travels with the repo it governs; the binary's
  install dir stays read-only/global.
- **Workspace:** the one global touchpoint. A thin registry under `~/.satelle/`
  lists connected repo paths. The web server opens each repo's own `satelle.db`
  and aggregates. Per-repo DBs are the source of truth; the workspace is an
  aggregation layer, not a second database.

### Web server

- Basic local web server that looks like the current project page.
- When **multiple local repos are connected**, that aggregate **is** the workspace.

### Documents

- **Not embedded** in the OSS tier. MVP stores documents as plain sqlite rows;
  optional FTS5 for keyword search.
- Embedded / semantic search is a **paid-server** feature. Keeps the OSS build
  dependency-light (no embedding model, no vector store).

### Sync

- Online connection / sync is **built in** (the pluggable server backend) but
  **turned off** by default.

### Logging

- **Keep arbor.** It's a thin wrapper over stdlib `log/slog` with zero external
  deps and no remote coupling — already the lightest clean option. Copy
  `internal/arbor` as-is; drop the optional `LedgerHandler` for local (or back it
  with the local sqlite ledger).

### Versioning

- Same scheme as satellites: a version embedded in the binary, semantic-version
  git tags, release-on-tag, and the discipline of bumping the version when the CLI
  client paths change. (Exact mechanism to mirror once the scout confirms how
  satellites wires `satellites.version` and its release gate.)

## Port principles

- Port *from* `satellites`; keep using `satellites` as the working repo until MVP.
- Take the opportunity to **clean the CLI verbs** and make them consistent.
- **Start simple, keep simple.** Prefer the smallest change that serves the
  objective; reuse an existing abstraction before adding a new one.
