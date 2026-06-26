# satelle

Local-first, open-core substrate for agent-driven work. Satelle governs the
authored process — stories, tasks, an evidence ledger, and authored markdown
(documents, workflows, principles, skills) — backed by a per-repo SQLite
database. The OSS tier runs **100% locally**: a single static binary, no server,
no cgo.

> V6 rebrand and open-core restructure of `satellites`. See [`docs/`](./docs)
> for the product spec and port architecture.

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
local-only (the OSS tier ships no hosted URL).

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

## Architecture

- **Pure-Go SQLite** (`modernc.org/sqlite`, no cgo) — one static binary.
- **System-of-record split:** stories/tasks/ledger are dynamic SQLite rows;
  authored markdown is the source of truth, synced into a SQLite index by a
  directory monitor.
- **Config:** per-repo `.satelle/satelle.toml` with defaults for every setting
  and a gitignored `satelle.local.toml` overlay.

See [`docs/spec.md`](./docs/spec.md) and [`docs/architecture.md`](./docs/architecture.md).

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
