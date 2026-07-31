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
- **Every project → `/r/<slug>/`.** Each registered repo — the launch repo
  (`[service].repo` in the global config, set by `satelle service install`) and
  every repo added with `satelle workspace add` — is listed on the landing and
  served under `/r/<slug>/`. The slug is the repo directory's **basename**.
  Basenames must be unique across the workspace: seeding a second repo with a
  colliding basename is rejected. Legacy colliding partitions (if any) render
  under their full `repo_key` so landing links stay unique.
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

## When the UI looks stale

The web UI is a **push-fed mirror**: the CLI posts a snapshot as each mutating
verb finishes, and the service renders that copy. A push that never lands — the
service was restarting (`satelle update` cycles it), down, or unreachable — used
to leave the mirror on its last good frame forever, which reads as a stuck story
rather than a stale view.

The service now **re-requests** a snapshot for every project it renders: once at
startup — which is what repairs the push a restart just swallowed — and then
every **five minutes**, so a dropped push repairs itself within that bound
without you doing anything. Two things you may still see:

- **A `stale · last update <time>` chip** on the landing row or the project
  header. That means the service could *not* re-request state — so the frame is
  presented as unconfirmed, never as current. Check that the service is running
  (`satelle service status`) and that `satelle` is installed beside it.
- **Nothing changed after a mutation.** Re-seed by hand from the repo:

      satelle workspace add

  That is the manual recovery path — the same verb you joined with — and it is
  always safe to re-run.

The per-repo database stays the source of truth throughout; the mirror is only a
read-only copy of it.

## When to use `service install` vs `workspace add`

- **`satelle workspace add <repo>`** — register a project in the workspace registry
  and seed the push-fed serve mirror. `[server] endpoint` is required to seed; it
  usually lives in gitignored `.satelle/satelle.local.toml`. When unset and a local
  serve is running at the service port, `workspace add` writes that endpoint into
  local.toml and seeds in one command. Without a reachable serve it still
  registers but exits non-zero with the exact remedy. Later mutations push
  automatically. The project appears on the landing and is served at `/r/<slug>/`
  (basename of the repo path). A colliding basename with an already-seeded
  partition fails closed with a clear error — rename the directory and re-run.
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
