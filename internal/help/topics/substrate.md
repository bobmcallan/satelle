# Substrate — how satelle stores and validates its configuration

satelle's process is **configuration, not code**: workflows, skills, and
principles are authored markdown. This topic explains where that substrate lives,
the format it follows, and how it is validated.

## Three planes (authored / defaults / runtime)

| Plane | Where | Holds |
|---|---|---|
| **Authored** | `<repo>/.satelle/` (git-optional) | satelle.toml, constitution, edited workflows (incl. `workflows/agents.toml`) / skills / principles, documents, tasks |
| **Defaults** | embedded in the binary | sparse repo-agnostic workflows/skills/principles (resolved virtually; see virtual-defaults work) |
| **Runtime** | `~/.satelle/<repo-key>/` | satelle.db (+wal/shm), logs/, backups/, stories attachment cache |

`satelle runtime path` prints the resolved runtime dir and whether this repo is
still on the legacy in-repo layout. `satelle runtime migrate` copies a legacy
DB + runtime dirs to the home key (explicit; never silent). One repo-key maps to
one isolated DB — never a flat multi-repo bag.

## Authored substrate under `.satelle/`

`satelle init` lays a self-documenting skeleton under `.satelle/`:

- `satelle.toml` and `workflows/agents.toml` (both documented, both optional to edit);
- a dir per authored kind — `documents/ workflows/ principles/ skills/ tasks/` —
  each with a `README.md` describing what it should contain (READMEs are dir
  descriptors; the indexer and OKF normaliser skip them);
- the complete **default solution**, materialised on disk so the default
  substrate is visible and editable: the order-zero **baseline workflow** (the
  minimal repo-agnostic lifecycle — edit it in place to layer your own gates),
  the parent/epic container workflow, the task-execution workflow, and every
  gate skill they reference. A workflow whose `applies_to` category is already
  claimed by an authored workflow is skipped (no same-precedence duplicate);
  the embedded copy still backstops the order-zero fallback either way.

The binary still ships embedded canonical defaults; a repo file with the same
(kind, name) overrides its default. `init` materialises the defaults so you never
have to reason about invisible substrate — and never clobbers an authored file
(an existing workflow set is respected wholesale). `satelle restore` re-installs
the embedded skills/principles over drifted copies; `satelle rebase` goes
further — it backs up `workflows/ skills/ principles/` to a timestamped dir under
the runtime `backups/`, wipes them, and redeploys the complete default solution
(the "start clean" recovery).

## Pre-mutation backup

Before init/restore/rebase **overwrites** an existing file under `.satelle/`,
satelle writes a local copy under the runtime `backups/` tree (kinds:
`pre-mutation/`, `diverged/`, `restore/`, or a timestamped dir for rebase). Local
floor always — heal paths never block for backup. Online/personal push of
pre-images into the bound project's documents partition is **opt-in**
(`[backup] hosted = true`) and requires `satelle login` + project bind;
offline/auth failure degrades to local with a notice. Default is local-only so
init never poisons the documents partition that `satelle sync` pulls (backups/
is a restore exclusion). Set `[backup] local_only = true` (prefer
`satelle.local.toml`) to suppress the advisory that points at the online option.

## Format: Open Knowledge Format (OKF)

Every authored doc carries YAML frontmatter with a required **`type`** key (OKF):
`type: workflow | principle | skill`, and `type: <category>` for free-form
documents. The directory is authoritative for the kind; `type` mirrors it. A
legacy `kind:` key is migrated to `type:` automatically at ingest. Bodies are
ordinary markdown — a route half carries its declaration of done or its step
catalogue, a skill its rubric.

## Validation is deterministic code

The per-noun `satelle <noun> validate` (and the reindex pass) check each doc with a **deterministic
structure check** (`internal/structure`) — frontmatter keys, kebab name matching
the file, a usable definition, a non-stub body, and for a workflow the graph
(connected, terminal `done`, `backlog` start, resolvable executor skills). These
are CODE, not LLM rubrics: harness-independent and never flaky. A swapped agent
(claude, codex, …) cannot change what "valid" means. `satelle <noun> validate` needs no
agent CLI.

`satelle-repo-agnostic` (only satelle's OWN embedded `scope: system` substrate
must avoid repo-specifics) is a satelle-dev concern — never a runtime gate. Your
project substrate is meant to be opinionated; satelle never judges it for that.

## Authoring

Drop a markdown file under the right `.satelle/<kind>/` dir and run `satelle
reindex` — or use `satelle skill|workflow|principle create --from <file>`, which
writes through the deterministic structure check and refuses a non-conforming
artifact. List with `satelle doc list`; read one with `satelle doc get <kind>
<name>`.

See also: `satelle help reviewer-checks`, `satelle help principles`.
