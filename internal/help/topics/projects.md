# Projects — serving one repo or many

satelle's web service is **adaptive**: `satelle serve` (and the background
service) takes **no multi-project flag**. It always serves the **bound repo** at
the root (`/`) and every *other* registered repo under a path prefix
(`/<slug>/`). A single-repo setup is just the case with no other repos
registered — `/` is your project, exactly as before.

## The model

- **Bound repo → `/`.** The bound repo is the service's working directory
  (`[service].repo` in the global config, set by `satelle service install`). It is
  always served at `/` and never moves.
- **Other registered repos → `/<slug>/`.** Each repo added to the workspace
  registry is served by its own child process behind a reverse proxy at
  `/<slug>/` (the slug is derived from the repo's directory name). Each keeps its
  own database — there is no shared store and no cross-project bleed.
- **`/projects`** lists every served project with a link to each.

So adding a project is **additive**: the bound repo stays at `/`, the new one
appears at `/<slug>/`.

## Adding another project to a running service

Use the workspace registry — do **not** re-run `service install`:

    satelle workspace add /path/to/other-repo

The running service notices the registry change within a few seconds and starts
serving it at `/<slug>/`, with `/` untouched. `satelle workspace remove <path>`
stops serving it; `satelle workspace list` shows the registry.

## When to use `service install` vs `workspace add`

- **`satelle workspace add <repo>`** — add another project to the *existing*
  service (served additively at `/<slug>/`). This is the usual way to grow a
  setup.
- **`satelle service install`** — install or reconfigure the service itself
  (port, bind address, and the bound `/` repo). Re-running it with no `--repo`
  preserves the saved bound repo; passing `--repo <repo>` **repoints** `/` to
  that repo. Use it to *choose* which repo lives at `/`, not to add more.

## Where settings live

- **Global, machine-wide:** `~/.satelle/config.toml` (override the directory with
  `SATELLE_HOME`). Holds `[service]` (port/addr/bound repo), `[workspace]` (the
  registered repo paths), `[agent]` (the reviewer CLI), and `[ui]` (light/dark
  theme, shared across repos). Safe to hand-edit.
- **Per-repo:** `<repo>/.satelle/` — that repo's `satelle.toml`, its `satelle.db`
  (the source of truth for its stories/tasks/docs), and its authored markdown.
  Project *data* never leaves the repo.

## Keeping the binary current

`satelle update` self-updates the installed binary to the latest release
(sha256-verified) and restarts the service. `satelle update --check` reports
whether an update is available without installing.
