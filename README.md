# satelle

Local-first substrate for agent-driven work. Satelle governs the
authored process — stories, tasks, an evidence ledger, and authored markdown
(documents, workflows, principles, skills) — backed by a per-repo SQLite
database. Work moves through a **gated workflow**: the agent executes; isolated
reviewers gate every status change. satelle runs **100% locally**: a single
static binary, no server, no cgo.

> V6 rebrand of `satellites`. See [`docs/`](./docs) for the product spec, port
> architecture, and the operating model.

## Install

```sh
curl -fsSL https://github.com/bobmcallan/satelle/releases/latest/download/install.sh | sh
```

Downloads the latest release binary for your platform, sha256-verifies it, and
installs to `~/.local/bin` (override with `SATELLE_INSTALL_DIR`). Or build from
source: `make install`.

Already installed? `satelle update` self-updates in place — it resolves the
latest release, sha256-verifies it, replaces the binary, and restarts the
background service. `satelle update --check` reports availability without
installing.

## Quickstart

```sh
go build -o satelle ./cmd/satelle   # or: make install

cd your-repo
satelle init           # scaffold .satelle/ (config, database, default workflows + skills) + validate the deployment
satelle story create --title "Ship the thing" --priority high
satelle task create  --title "write release notes"
satelle reindex          # index authored markdown under .satelle/
satelle status         # config, database, and store counts
satelle serve          # local web project page (http://127.0.0.1:8787)
```

While `serve` runs, the project page lists every story/task, and each links to a
trackable detail URL — `http://127.0.0.1:8787/story/<id>` (or `/task/<id>`) —
showing status, acceptance criteria, and the full ledger timeline. The server is
local-only (there is no hosted URL).

### Push-fed UI server (CLI → serve)

The web UI is a **read-only mirror** fed by the CLI (epic:serve-split):

1. Configure `[server] endpoint = "http://127.0.0.1:8787"` in `.satelle/satelle.toml` (or local).
2. Run `satelle serve` (or `satelle service install` for a systemd unit).
3. `satelle ui push` posts a full snapshot; mutating verbs also fire-and-forget change events and auto-snapshot once per process.

Serve never opens per-repo runtime DBs — only `~/.satelle/serve/mirror.db`, partitioned by repo-key.

### Substrate freshness without serve

The CLI keeps the doc index, task store, and story-backlog view current **without
a running serve process**:

1. **SessionStart hook** — `satelle init` scaffolds `satelle reindex` on every agent session.
2. **Explicit `satelle reindex`** — full on-demand pass (docs + tasks + backlog view).
3. **Post-story-verb** — `story create` / `story set` best-effort regenerate the disposable backlog view.

Hand-edited tasks or authored markdown still need a reindex (or the next session
start) to land in the store/index. See `satelle reindex --help`.

### Always-on service

`satelle serve` runs in the foreground. To keep the project page up across
terminals and reboots, install it as a background service:

```sh
make install                 # build + place satelle on PATH (~/.local/bin)
cd your-repo
satelle service install      # systemd user service (Linux/WSL)
satelle service status       # show state + URL
```

Settings live in the machine-wide `~/.satelle/config.toml` (`[service]` port /
addr / repo). Change the port there (or pass `--port`) and re-run
`satelle service install`. The service binds `0.0.0.0` by default, so in **WSL**
it's reachable from a Windows browser at `http://localhost:<port>`. On native
Windows (no systemd), `service install` prints Task Scheduler steps instead.

`init` is idempotent and writes a managed `.gitignore` block (the local
`.satelle/satelle.db` stays out of git; the config and authored markdown are
committed). It's also optional — a repo with no `.satelle/satelle.toml` runs
zero-config on defaults, with data in `.satelle/satelle.db` travelling with the
repo it governs.

## Commands

| Group | Verbs |
|-------|-------|
| `story` / `task` | `create`, `get`, `list`, `set` |
| `task` (only) | `archive` — dispose of a superseded header (record archived + files moved to `.satelle/backups/`) |
| `ledger` | `append`, `list` |
| `doc` | `list`, `get` |
| `hosted` | `login`, `logout`, `whoami` — authenticate to a hosted satelle-server (OAuth 2.1 + PKCE); optional, the local service runs standalone without it |
| `project` | `create`, `list`, `bind`, `show` — manage projects on the hosted satelle-server; `bind` records which hosted project this repo's personal sync targets. Requires `login` for create/list |
| `sync` | `scopes`, `config` (push/deploy), `documents` (push/pull), `workstate` (push/pull), `rehydrate`/`pull` (no push) — personal opt-in targets **this repo's bound hosted project only**; local is the default. **Recover after clone/wipe:** install → `login` → `project bind <slug>` → `sync rehydrate` (config deploy first, then documents + workstate pull) |
| `publish` | `push`, `list`, `adopt`, `check` — team catalog (select local artifacts to share; not a second home for the repo). Distinct from personal `sync` |
| `settings` | Read/write config in two scopes: repo `<key> [value]` (committed `.satelle/satelle.toml`; no args lists all) is the default; `--global server <url>` sets the machine-wide hosted server in `~/.satelle/config.toml` (no login; sign in after via the UI or `login`) |
| | `init`, `reindex`, `status`, `serve`, `version` |

Both the CLI and the web server reach data the same way — through one verb
registry (`CLI / web → verb.Dispatch → store`), so the two surfaces never drift.

## Workflows & gates — the agent model

satelle governs work as a **gated workflow**: a story or task moves through a
lifecycle of **steps**, and it is `done` only when its status says so — reached
through every gate on the path.

- **The agent is the executor** — it does the work and drives the story forward.
- **satelle is the gatekeeper of status** — each forward transition is judged by
  an isolated, fresh-context **reviewer** (an `agent -p` rubric, or a deterministic
  functional check). Accept enacts the transition; reject pushes notes back. A
  reviewer is **read-only** — it judges, never mutates. Each gate is one isolated,
  fresh-context call over a payload satelle builds; satelle does the context
  selection, the reviewer reads what it needs through its read-only tools.

Workflows are **authored substrate** in the **DOT standard** (Graphviz): a
node-centric graph where each node is a step carrying an `agent`
(`executor`/`reviewer`) and a gate is named `prompt="@skill:NAME"` on a reviewer
node or an edge (the legacy edge `reviewer_skill=` attribute still parses). The embedded `satelle-baseline-workflow`
(`backlog → in_progress → done`) is the order-zero default; a repo overrides it
under `.satelle/workflows`, and a YAML lifecycle is auto-converted to DOT on
ingest. How each agent runs is bound in `.satelle/agents.toml` — the reviewer's
agent CLI (`claude` works; `codex` is a selectable stub) and its read-only grant;
the executor runs in-loop.

Process is configuration — change the workflow or its skills, change the process,
with no binary release. See `satelle help reviewer-checks` and the
`satelle-agent-model` and `satelle-dot-standard` principles.

## Architecture

- **Pure-Go SQLite** (`modernc.org/sqlite`, no cgo) — one static binary.
- **Gated workflows:** authored DOT lifecycles drive each story; isolated
  reviewers gate status transitions, with the agent backend bound per repo.
- **System-of-record split:** stories/tasks/ledger are dynamic SQLite rows;
  authored markdown is the source of truth, synced into a SQLite index by a
  directory monitor.
- **Config:** per-repo `.satelle/satelle.toml` with defaults for every setting
  and a gitignored `satelle.local.toml` overlay. Optional `stories_keep_closed`
  (count) and `stories_keep_days` (age) prune closed stories' attachment dirs
  under `.satelle/stories` — moving them to `.satelle/backups/stories/`; a
  non-terminal story's dir is always kept.

See [`docs/spec.md`](./docs/spec.md), [`docs/architecture.md`](./docs/architecture.md),
and [`docs/agent-model.md`](./docs/agent-model.md) (the operating
model: reviewer premise, DOT workflows, isolated fresh-context review).

## Development

```sh
go test ./...           # unit + package tests (also run in CI)
make integration        # black-box CLI + browser end-to-end (local only)
```

The integration suite (in `tests/`, behind the `integration` build tag) runs
against a real binary and **drives the web front end in headless Chrome**
(chromedp) — tab switching, inline expand, live filtering, and realtime updates
are all asserted in a real browser, not eyeballed. It needs a Chrome/Chromium
binary (`SATELLE_CHROME` overrides the path); it **runs locally only**, not in
GitHub CI, because it needs a browser and the running binary. `make integration`
builds satelle once and passes it via `SATELLE_BIN` (point that at any binary to
test it directly). Releases are cut by `.github/workflows/release.yml` when
`.version` is bumped; CI (`test.yml`) runs unit tests + build/vet/gofmt only.

satelle dogfoods itself — this repo is set up with `satelle init`, and its
remaining build phases are tracked as stories in the local database.

## License

MIT — see [LICENSE](./LICENSE).
