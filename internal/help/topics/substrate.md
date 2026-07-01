# Substrate — how satelle stores and validates its configuration

satelle's process is **configuration, not code**: workflows, skills, and
principles are authored markdown. This topic explains where that substrate lives,
the format it follows, and how it is validated.

## One source of truth: `.satelle/`

`satelle init` lays a complete, self-documenting skeleton under `.satelle/`:

- `satelle.toml` and `agents.toml` (both documented, both optional to edit);
- a dir per kind — `documents/ workflows/ principles/ skills/ stories/` — each
  with a `README.md` describing what it should contain (READMEs are dir
  descriptors; the indexer and OKF normaliser skip them);
- the embedded **baseline workflow** and the embedded skills it references,
  materialised on disk so the default substrate is visible and editable.

The binary still ships embedded canonical defaults; a repo file with the same
(kind, name) overrides its default. `init` materialises the defaults so you never
have to reason about invisible substrate.

## Format: Open Knowledge Format (OKF)

Every authored doc carries YAML frontmatter with a required **`type`** key (OKF):
`type: workflow | principle | skill`, and `type: <category>` for free-form
documents. The directory is authoritative for the kind; `type` mirrors it. A
legacy `kind:` key is migrated to `type:` automatically at ingest. Bodies are
ordinary markdown — a workflow's body carries its DOT graph, a skill's its rubric.

## Validation is deterministic code

`satelle validate` (and the reindex pass) check each doc with a **deterministic
structure check** (`internal/structure`) — frontmatter keys, kebab name matching
the file, a usable definition, a non-stub body, and for a workflow the graph
(connected, terminal `done`, `backlog` start, resolvable executor skills). These
are CODE, not LLM rubrics: harness-independent and never flaky. A swapped agent
(claude, codex, …) cannot change what "valid" means. `satelle validate` needs no
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
