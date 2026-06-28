# Satelle — Port Architecture & Build Plan

Grounded in a scout of `satellites` (storage seam, local layout/config, CLI verbs,
logging/web). See [spec.md](./spec.md) for the product spec.

## The one seam: the verb registry

Both the CLI and the web portal reach data the **same way** — neither touches the
database directly:

```
CLI command  ─┐
              ├─→  verb.Dispatch(ctx, "<verb>", jsonBody)  ─→  domain Store  ─→  DB
web handler  ─┘
```

- `internal/cli/dispatch.go` routes: config has `server_url`+token → HTTP to server;
  otherwise → **in-process** `verb.Dispatch`.
- Verbs read/write through concrete stores wired in as package globals:
  `verb.SetDocumentStore(...)`, `SetProjectStore(...)`, `SetLedgerStore(...)`, etc.
  (e.g. `internal/verb/document.go`, store wiring at `cmd/satellites-server/main.go`).
- Today those stores are **Postgres-only** (wired in the server boot); with no server
  the in-process path errors `"store not configured"`.

**Satelle's whole local MVP = implement sqlite-backed stores, wire them into the verb
registry. Then the CLI and the local web server both light up against the local DB,
unchanged.** satelle is always-local, so dispatch is always in-process — no remote branch.

## What's reused vs rewritten

| Layer | Verdict | Notes |
|-------|---------|-------|
| `internal/arbor` (logging) | **Copy as-is** | Thin `log/slog` wrapper, zero deps. Drop the optional `LedgerHandler`. |
| `internal/verb/*` (business logic) | **Mostly reused** | Storage-agnostic — calls store methods. Port with the verb-name cleanup. |
| `internal/server` + `templates/` (portal) | **Reused, stripped** | Plain `http.ServeMux`; handlers call `verb.Dispatch`. Strip auth/OAuth/SSE. |
| `internal/codeindex`, `internal/workstate` | **Copy as-is** | Already pure-Go sqlite, self-migrating — the schema template to follow. |
| `internal/cliconfig` | **Reused, trimmed** | Drop server/token/credstore; keep repo-root config resolution. |
| Domain **stores** (document/project/ledger/story/task) | **Rewritten for sqlite** | Server stores use Postgres SQL (`$1`, golang-migrate). Reimplement against sqlite, same method signatures. This is the bulk of the work. |
| Auth / OAuth / sessions / live SSE | **Dropped** | No auth for local. |

## Database

- Pure-Go `modernc.org/sqlite` (no cgo) — same driver satellites already uses for
  `state.db` / `index.db`. WAL, `busy_timeout`, `SetMaxOpenConns(1)`, self-migrating
  schema inlined in code (the `internal/workstate/store.go` pattern — **not**
  golang-migrate).
- **Per-repo:** `.satelle/satelle.db` next to `.satelle/satelle.toml`.
- **System of record vs source-of-truth split (MVP):**
  - **sqlite (dynamic work):** stories, tasks, ledger, engagement/work state.
  - **markdown on disk (authored artifacts):** workflows, principles, documents,
    skills. The **markdown files are the source of truth**; a **directory monitor**
    (fsnotify-style watcher) syncs them into a sqlite **index** so the CLI/web can
    query them (the `index.db`-caches-symbols pattern, applied to authored docs).
  - **Dirs are TOML-configurable and may live outside `.satelle/`** — generalize the
    existing `[substrate_roots]` (kind → path) so authored sources can be anywhere;
    the watcher tracks whatever paths config points at.
  - Keeps the store rewrite scoped to the dynamic primitives; authored content is a
    watch-and-index concern, not a hand-managed store.

## Config

Follow the current satellites TOML config model exactly — same conventions, a
trimmed `Config` struct:

- **Every setting has a default; zero-config works.** A repo with an empty (or no)
  `satelle.toml` runs on defaults for all keys (`.satelle/` paths, db location,
  substrate roots, web port, log level, etc.) — config only overrides.
- **Repo-root resolution:** walk up from CWD for `.satelle/satelle.toml` (same as
  satellites).
- **Gitignored local overlay:** `satelle.local.toml` beside the committed config for
  per-user overrides.
- Keep `[substrate_roots]` (generalized — authored dirs may be outside `.satelle/`)
  and `data_dir`. Drop `server_url`, tokens, `global_publishers`, credstore.
- Satellites has **no** dispatch `--local` flag (only `install --local`); satelle is
  always-local, so the flag is implicit — keep `install --local`, drop the rest.

## Workspace

Per-repo DBs are the source of truth. A thin global registry under `~/.satelle/`
lists connected repo paths; the web server opens each repo's `satelle.db` and
aggregates → that aggregate is the "workspace". (MVP can ship single-repo first.)

## CLI verb standard (the cleanup)

Current surface is inconsistent: mixed case (`status_transition` snake / `set-status`
kebab / `changedoc`,`commitgate` camel), fragmented reads (`get`/`list`/`show`/
`symbols`/`index`), fragmented writes (`create`/`upsert`/`publish`/`upload`/`sync`/
`deploy` — where `deploy` actually *pulls*). Standardize:

- **All kebab-case.** `status_transition` → `status-transition`; `changedoc` →
  `change-doc`; `commitgate` → `commit-gate`; `codenudge` → `code-nudge`.
- **One read verb per shape:** `list` (many) + `get` (one). Retire `show`/`symbols`
  as read aliases. (`code index` stays — it's a build action, not a read.)
- **One write verb per shape:** `create` (new) + `set`/`update` (mutate). Keep
  `upsert` server-side only, not on the CLI surface.
- **Clarify push/pull:** rename `deploy` (which pulls) → `pull`; unify
  `upload`/`sync`.
- **One status path:** keep `status-transition` (the gate); fold/retire `set-status`.
- **Consistent output:** `output` across story and task (drop the separate task
  `publish`, or make it clearly distinct).

Apply per command-group as it's ported — it's the cheapest moment, and Satelle is a
clean break (new binary), so no back-comat burden.

## Build order (MVP)

1. **Scaffold** the `satelle` module (import path, `version` cmd, copy arbor /
   codeindex / workstate / cliconfig-trimmed). Builds + `satelle version` works.
2. **Local sqlite stores** for stories / tasks / ledger (self-migrating schema), plus
   a **directory monitor** that watches the authored markdown dirs (paths from TOML,
   may be outside `.satelle/`) and syncs them into a sqlite index.
3. **Local bootstrap** `cmd/satelle`: load config, open `.satelle/satelle.db`,
   `SetXStore()` the local stores, run CLI in-process.
4. **Port the verb/command surface** applying the kebab-case + verb standard above.
5. **Local web server**: strip auth/OAuth/SSE, render the project page from local
   sqlite via the same verbs. Single repo first.
6. **Workspace aggregation**: `~/.satelle/` registry + multi-repo web view.
7. **Remote actors (off by default)**: distributed execution is a configuration of
   the reviewer/actor model — bind an actor to a remote backend in `.satelle/actors.toml`;
   local-only out of the box. (SQL stays libSQL-compatible so replica sync could slot
   in later.)

Versioning mirrors satellites (binary-embedded version, semver tags, bump on
client-path change).
