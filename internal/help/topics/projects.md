# Projects — one landing, many repos

satelle's web service is **adaptive**: `satelle serve` (and the background
service) takes **no multi-project flag**. The root (`/`) is always a
**connected-projects landing** — a launcher listing every registered project —
and *every* repo, including the one you launched from, is served under its own
path prefix (`/<slug>/`). A single-repo setup is just the case with one project
on the landing.

## The model

- **`/` → the landing.** The root is a launcher: one card per project with live
  story/task/doc counts, plus a panel for adding a project, opening help, and
  keeping the binary current. It is not any single repo's project page.
- **Every project → `/<slug>/`.** Each registered repo — the launch repo
  (`[service].repo` in the global config, set by `satelle service install`) and
  every repo added with `satelle workspace add` — is served by its own child
  process behind a reverse proxy at `/<slug>/` (the slug is derived from the
  repo's directory name). Each keeps its own database — no shared store, no
  cross-project bleed.
- **`/projects`** redirects to `/` (back-compat for older links).

So adding a project is **additive**: a new card appears on the landing and the
repo is served at its `/<slug>/`.

## Adding another project to a running service

Use the workspace registry — do **not** re-run `service install`:

    satelle workspace add /path/to/other-repo

The running service notices the registry change within a few seconds and starts
serving it at `/<slug>/`, with a fresh card on the landing — no restart.
`satelle workspace remove <path>` stops serving it; `satelle workspace list`
shows the registry.

## When to use `service install` vs `workspace add`

- **`satelle workspace add <repo>`** — register another project. It appears on
  the landing and is served at `/<slug>/`. This is the usual way to grow a setup.
- **`satelle service install`** — install or reconfigure the service itself
  (port, bind address, and which repo is the launch/working-directory repo).
  Re-running it with no `--repo` preserves the saved repo; passing `--repo <repo>`
  changes the launch repo. It does not move anything off `/` — the landing is
  always at `/`.

## Where settings live

- **Global, machine-wide:** `~/.satelle/config.toml` (override the directory with
  `SATELLE_HOME`). Holds `[service]` (port/addr/launch repo), `[workspace]` (the
  registered repo paths), `[agent]` (the reviewer CLI), and `[ui]` (light/dark
  theme, shared across repos). Safe to hand-edit.
- **Per-repo:** `<repo>/.satelle/` — that repo's `satelle.toml`, its `satelle.db`
  (the source of truth for its stories/tasks/docs), and its authored markdown.
  Project *data* never leaves the repo.

## Keeping the binary current

`satelle update` self-updates the installed binary to the latest release
(sha256-verified) and restarts the service. `satelle update --check` reports
whether an update is available without installing.
