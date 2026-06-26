# satelle

Local-first, open-core substrate for agent-driven work. Satelle governs the
authored process — stories, tasks, an evidence ledger, and authored markdown
(documents, workflows, principles, skills) — backed by a per-repo SQLite
database. The OSS tier runs **100% locally**: a single static binary, no server,
no cgo.

> V6 rebrand and open-core restructure of `satellites`. See [`docs/`](./docs)
> for the product spec and port architecture.

## Quickstart

```sh
go build -o satelle ./cmd/satelle

cd your-repo
satelle init           # scaffold .satelle/ (config, database, authored dirs)
satelle story create --title "Ship the thing" --priority high
satelle task create  --title "write release notes"
satelle index          # index authored markdown under .satelle/
satelle status         # config, database, and store counts
satelle serve          # local web project page (http://127.0.0.1:8787)
```

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

## License

MIT — see [LICENSE](./LICENSE).
