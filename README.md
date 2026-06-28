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

## Quickstart

```sh
go build -o satelle ./cmd/satelle   # or: make install

cd your-repo
satelle init           # scaffold .satelle/ (config, database, authored dirs)
satelle story create --title "Ship the thing" --priority high
satelle task create  --title "write release notes"
satelle index          # index authored markdown under .satelle/
satelle status         # config, database, and store counts
satelle serve          # local web project page (http://127.0.0.1:8787)
```

While `serve` runs, the project page lists every story/task, and each links to a
trackable detail URL — `http://127.0.0.1:8787/story/<id>` (or `/task/<id>`) —
showing status, acceptance criteria, and the full ledger timeline. The server is
local-only (there is no hosted URL).

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
| `ledger` | `append`, `list` |
| `doc` | `list`, `get` |
| | `init`, `index`, `status`, `serve`, `version` |

Both the CLI and the web server reach data the same way — through one verb
registry (`CLI / web → verb.Dispatch → store`), so the two surfaces never drift.

## Workflows & gates — the recursive-actor model

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
node-centric graph where each node is a step carrying an `actor`
(`executor`/`reviewer`) and a reviewer node names its gate (`prompt="@skill:NAME"`,
or an edge `reviewer_skill`). The embedded `satelle-baseline-workflow`
(`backlog → in_progress → done`) is the order-zero default; a repo overrides it
under `.satelle/workflows`, and a YAML lifecycle is auto-converted to DOT on
ingest. How each actor runs (in-loop, isolated `agent -p`, or another harness) is
bound in `.satelle/actors.toml`.

Process is configuration — change the workflow or its skills, change the process,
with no binary release. See `satelle help reviewer-checks` and the
`satelle-recursive-actor-model` and `satelle-dot-standard` principles.

## Architecture

- **Pure-Go SQLite** (`modernc.org/sqlite`, no cgo) — one static binary.
- **Gated workflows:** authored DOT lifecycles drive each story; isolated
  reviewers gate status transitions, with the actor backend bound per repo.
- **System-of-record split:** stories/tasks/ledger are dynamic SQLite rows;
  authored markdown is the source of truth, synced into a SQLite index by a
  directory monitor.
- **Config:** per-repo `.satelle/satelle.toml` with defaults for every setting
  and a gitignored `satelle.local.toml` overlay.

See [`docs/spec.md`](./docs/spec.md), [`docs/architecture.md`](./docs/architecture.md),
and [`docs/recursive-actor-model.md`](./docs/recursive-actor-model.md) (the operating
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
