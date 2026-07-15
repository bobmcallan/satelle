---
name: decision-local-db-placement
type: document
tags: [type:document, epic:workspace-rehydrate]
description: Decision for sty_1eaa15f5 — local satelle DB stays per-project (default under the repo); never git-tracked; never a single multi-repo blob; agents only process the active repo's work items.
---

# Decision — local DB placement (per-project; never git; never single global blob)

**Story:** sty_1eaa15f5  
**Epic:** epic:workspace-rehydrate (`sty_4ae354f4`)  
**Date:** 2026-07-15  
**Status:** decided

## Decision

### Chosen default (now)

**Keep the default path** `<repo>/.satelle/satelle.db` (or `<data_dir>/satelle.db`
when `data_dir` / `db` are overridden in config).

- The DB is **per-project work state** (stories, evidence, leases, indexes) for
  **this repo root**, not harness identity.
- It is **never git-tracked** (init recommended ignore includes `satelle.db*`).
- Path-as-identity remains the default: `cd <repo> && satelle …` opens that
  repo's ledger without a separate project-id lookup.

### Explicitly rejected

**A single global `~/.satelle/satelle.db` (or any one file) holding all repos'
work items without a mandatory repo partition.** That collapses multi-repo
serve, confuses worktrees, and — critically — risks an agent listing or
acting on **stories that do not belong to the active repo**.

### Deferred option (not now)

**Home-keyed per-project cache** (e.g. `~/.satelle/projects/<project-id>/satelle.db`)
may be revisited **only after**:

1. stable project bind identity, and  
2. workstate pull / sync-down rehydrate (epic children),

and **only if** every store is still **one project per definition** — not a
flat multi-repo bag. Path migration is **not** part of this story (no forced
migration of existing repo DBs).

## Repo definition is load-bearing (agent boundary)

**Agents must not process stories (or other work items) outside the active repo.**

Therefore any future “common” or home-local layout **must maintain the repo
definition**:

| Layer | Role |
|--------|------|
| **Machine** (`~/.satelle/`, `~/.config/satelle/`) | Identity, workspace registry, credentials, sync cursors — **not** a substitute for the project ledger |
| **Project ledger** (default: `<repo>/.satelle/satelle.db`) | Work items for **one** repo root / bound project only |
| **Hosted personal** (when opted in) | Continuity for that bound project; rehydrate rebuilds the local project ledger |

Session/CLI/serve resolution always binds to a **repo root**. Queries and
dispatches are scoped to that root's store. A home multi-tenant layout is
acceptable **only** as `map[repo_or_project_id] → isolated DB (or schema)`,
never as unscoped global work items.

This is distinct from Claude/Grok putting **harness** state under home: satelle's
DB is **project work**, so the Grok/Claude home analogy applies only to machine
config, not to collapsing project ledgers.

## Continuity when personal is on

Aligned with [decision-workspace-continuity-posture](decision-workspace-continuity-posture.md)
(`sty_c5c54f02`):

- **Local-only:** the per-project DB on disk is continuity (operator's machine).
- **Personal opt-in:** hosted personal workstate is continuity; the **local DB is
  a cache** rebuilt by sync-down. Path (repo vs future home-keyed) is secondary
  to rehydrate.

## What does not change in this story

- No relocation of existing databases.
- No new default for `data_dir` / `db`.
- No change to store schema or multi-repo serve mechanics beyond documenting
  that each open repo uses its own DB path.
- Operator may already override `db` / `data_dir` in satelle.toml — still
  **one path per process/repo**, not a shared multi-repo blob.

## Operator-facing config comments

Init scaffold and settings help describe:

- `data_dir` / `db` as the **per-repo** store home (default under the repo),
- gitignored local state,
- not a global multi-project ledger.

## See also

- sty_4ae354f4 — epic:workspace-rehydrate  
- sty_c5c54f02 / decision-workspace-continuity-posture.md — local vs personal continuity  
- sty_45bfcc50 — workstate pull (rehydrate cache)  
- sty_2f1538a4 — sync-down rehydrate UX  
- Original product framing: agents stay in-repo; common DB only with repo definition  
