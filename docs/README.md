# Satelle docs

V6 rebrand of `satellites`. Porting *from* `../satellites`; that repo stays the
working tool until the satelle MVP lands.

## Read these

- **[spec.md](./spec.md)** — product spec: the local-first design, database, web
  server, documents, sync, versioning.
- **[architecture.md](./architecture.md)** — the port plan, grounded in a scout of
  `satellites`: the single verb-registry seam, what's reused vs rewritten, the sqlite
  design, config model, the CLI verb-cleanup standard, and the MVP build order.
- **[agent-model.md](./agent-model.md)** — the operating model:
  the agent reviewer premise (executor does the work, read-only reviewers
  gate status), the flexible DOT workflow format (node + edge gates, YAML→DOT at
  ingest, validation + spine), how reviewers run (isolated fresh-context review), and
  the agents layer — with package/file pointers.

## Status (2026-06-26)

- ✅ GitHub repos created: `satelle` (public, MIT), `satelle-infra` (private).
- ✅ Both cloned to `~/development/`.
- ✅ Spec + architecture/build plan written (this dir).
- ✅ **Scaffold done (build order step 1)** — `go.mod` (Go 1.26, pure-Go), `internal/arbor`
  ported (LedgerHandler dropped), `internal/buildinfo` (ldflag-stampable), cobra `satelle`
  root with the `register()` pattern, `version` command, `cmd/satelle/main.go`. `go build
  ./...` + `satelle version` work; `go test ./...` green.
- ✅ **Local sqlite stores + directory monitor done (build order step 2)** — per-repo
  `.satelle/satelle.db` opened via the workstate pattern (pure-Go `modernc.org/sqlite`,
  WAL, `busy_timeout`, `_txlock=immediate`, `SetMaxOpenConns(1)`, self-migrating). Dynamic
  primitives: `internal/ledger` (evidence log) + `internal/workitem` (stories & tasks share
  one kind-partitioned store). Authored markdown (documents/workflows/principles/skills) is
  watch-and-indexed by `internal/docindex` (scan-based `Sync` + poll-loop `Watch`, paths
  from TOML `[substrate_roots]`, may live outside `.satelle/`). `internal/config` trims
  cliconfig to local-only. `go test ./...` green; `CGO_ENABLED=0` static build verified.
- ✅ **Local bootstrap done (build order step 3)** — `internal/app.Open()` loads config
  (walk-up, zero-config fallback to CWD), opens `.satelle/satelle.db`, and wires the stores.
  The CLI opens it in-process **only for store-backed commands** (cobra annotation +
  persistent pre/post-run), so `version`/`--help` never create a database. Two commands
  prove the wiring end-to-end: `satelle status` (config + db + store counts) and
  `satelle index` (one-shot run of the directory monitor). `go test ./...` green;
  `CGO_ENABLED=0` static build verified.
- ✅ **Verb/command surface done (build order step 4)** — ported the verb registry
  (`internal/verb`, MCP/auth surface dropped): CLI → `verb.Dispatch` → store, the one seam
  the web server will reuse. Stores wired into the registry at bootstrap. Verbs: `version`
  (now through the registry, closing the step-1 TODO), `story-*`/`task-*` (create/get/list/set,
  shared kind-partitioned handlers), `ledger-append`/`ledger-list`, `doc-list`/`doc-get`/
  `doc-sync`. Command groups follow the verb standard — **all kebab-case**, `list`+`get`
  (read), `create`+`set` (write), partial-update `set` (only passed flags change). Work-item
  create/set auto-record ledger lifecycle events. `go test ./...` green; `CGO_ENABLED=0` ok.
- ✅ **Local web server done (build order step 5)** — `internal/web` is the satellites
  portal stripped to the bone: a plain `http.ServeMux`, **no auth/OAuth/SSE**, rendering a
  single self-contained project page (stories, tasks, authored docs) entirely through
  `verb.Dispatch` — the web reaches data the same way the CLI does, so the two can't drift.
  `satelle serve` runs it (port from config) and runs the directory monitor continuously
  (`DocIndex.Watch`) so the index stays fresh while serving, with graceful Ctrl-C shutdown.
  Single-repo, as specified. `go test ./...` green (httptest coverage); `CGO_ENABLED=0` ok.
- ✅ **`satelle init` done (dogfooding prerequisite)** — scaffolds a repo idempotently:
  `.satelle/`, a fully-commented `satelle.toml` (zero-config defaults), the authored dirs
  (documents/workflows/principles/skills, each `.gitkeep`-tracked), the created+migrated
  `.satelle/satelle.db`, and a managed `.gitignore` block (db out of git; config + authored
  markdown committed). Local-only — none of satellites' server_url/MCP/OAuth/enforcement-hook
  scaffolding. `go test ./...` green.
- ✅ **Dogfooding live** — satelle now governs its own repo: `satelle init` run here;
  remaining phases tracked as stories in `.satelle/satelle.db` (local, gitignored — see
  `satelle story list`); a gateless baseline workflow authored at
  `.satelle/workflows/satelle-baseline-workflow.md` (open→in_progress→done, mirrors the
  satellites baseline, indexed by the monitor); black-box integration tests in `tests/`
  drive the built binary end-to-end (`go test -tags integration ./tests/...`).
- ✅ **Background service done (`satelle service`)** — install/uninstall/status manage a
  systemd **user** unit (Linux/WSL) running `satelle serve`, with linger so it survives
  logout + WSL boot. Machine-wide settings (port/addr/repo) live in `~/.satelle/config.toml`
  (the first use of the global `~/.satelle/` home). Binds `0.0.0.0` so a Windows browser
  reaches it from WSL; native Windows (no systemd) gets Task Scheduler guidance. `make install`
  places the binary on PATH. Verified live here — service active+enabled, reachable on
  localhost and the WSL IP.
- ✅ **Browser e2e automation** — `tests/browser_test.go` drives the web front end in
  headless Chrome (chromedp): tab switching, inline expand-on-click, live filter chips, and
  realtime cross-process updates are all asserted in a real browser (no eyeballing). Runs
  LOCALLY via `make integration` (needs Chrome) — deliberately not in GitHub CI.
- ✅ **Live & navigable UI (round 2)** — an expanded story's progress + ledger timeline now
  update in **realtime** (open expansions are preserved and refreshed across the SSE swap;
  standalone detail pages live-update too; triggers debounced). Breadcrumb navigation
  (project / kind / id). One shared filter component across stories/tasks/documents with
  `order:<field>` client-side sort and `tag:`/`tags:` aliases. Tags render **under the
  title** (not a column), like satellites. A `TestBrowserUserPath` e2e replicates a real
  session — open → expand → agent progresses the story from the CLI → assert the open
  timeline grows live → breadcrumb to the live detail page and back → order-sort.
- ✅ **Release pipeline done** — `.version` is canonical (release tag `v<satelle.version>`);
  `scripts/install.sh` is the `curl … | sh` bootstrap (resolves latest release,
  sha256-verifies, installs to `~/.local/bin`); `.github/workflows/test.yml` runs
  build/vet/gofmt/unit+integration/no-cgo; `.github/workflows/release.yml` builds the
  cross-platform matrix (linux/darwin amd64+arm64, windows amd64) with ldflag-stamped
  buildinfo, tags, and publishes binaries + `install.sh` when `.version` is bumped.
  Integration tests run against a prebuilt binary via `SATELLE_BIN` (`make integration`).
- ⬜ **Next: build order step 6** — workspace aggregation: `~/.satelle/` registry +
  multi-repo web view (the global `~/.satelle/` home now exists). (Step 7: define the sync
  backend interface, shipped disabled.) Both are tracked as stories in the local db.

## Start here (build order step 1)

Scaffold the module so `satelle version` builds:

1. `go mod init github.com/bobmcallan/satelle` (Go 1.26; pure-Go, no cgo).
2. Port `internal/arbor` from `../satellites` as-is (zero-dep slog wrapper; drop the
   optional `LedgerHandler`).
3. Cobra root named `satelle` using the self-registering `register()` pattern from
   `../satellites/internal/cli/root.go`; a `version` command backed by an
   ldflags-stamped buildinfo (wire it through the verb registry later).
4. `cmd/satelle/main.go` entrypoint → `cli.Execute()`.
5. `go build ./... && ./satelle version`.

Then proceed through build order steps 2–7 in [architecture.md](./architecture.md).

## Decisions locked (don't relitigate)

- One binary, local-only: a CLI plus a local web server, no server tier, no auth.
- sqlite via pure-Go `modernc.org/sqlite`, per-repo `.satelle/satelle.db`; SQL kept
  libSQL-compatible. Self-migrating inlined schema (workstate pattern).
- System-of-record split: stories/tasks/ledger in sqlite; workflows/principles/
  documents/skills are markdown source-of-truth, synced into a sqlite index by a
  directory monitor; authored dirs TOML-configurable, may live outside `.satelle/`.
- TOML config follows the satellites model with **defaults for all settings**
  (zero-config works) + gitignored `satelle.local.toml` overlay.
- Keep arbor logging; clean the CLI verbs to the kebab-case standard in
  architecture.md as each command group is ported.
- MVP web: single-repo project page first, workspace aggregation after.
