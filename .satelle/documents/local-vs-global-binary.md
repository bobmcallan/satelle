---
type: document
title: Local vs global satelle binary
description: How the global satelle install and a repo-local .satelle/satelle pin relate, and what `satelle update --local` does.
tags:
- document
- update
- local
timestamp: '2026-06-29T13:10:00Z'
---

# Local vs global satelle binary

satelle can run from two places, and a repo may pin its own copy.

## The two binaries

- **Global** — the satelle on your `PATH` (the curl installer / `make install`
  writes `~/.local/bin/satelle`, overridable via `SATELLE_INSTALL_DIR`). `satelle
  update` always refreshes this to the latest release.
- **Repo-local pin** — `<repo>/.satelle/satelle`. When present it is the binary
  that runs *for that repo*: at startup satelle resolves the repo root (walking up
  from the cwd for a `.satelle/` dir) and, if a `.satelle/satelle` pin exists that
  is a different file from the one invoked, **re-execs the pin** with the same args
  and environment. A loop-guard environment marker (`SATELLE_LOCAL_EXEC`) stops the
  pin from re-execing itself, and a binary never re-execs its own path. With no
  pin, the global binary runs unchanged.

## `satelle update --local`

`satelle update --local` installs the latest release into `<repo>/.satelle/satelle`
instead of the global install dir — the **same** release resolution, download,
sha256 verification, and atomic replace as the global update, just a local target.
It pins this repo to a satelle version independent of the global install; the
global service is not restarted (it runs the global binary).

This stays repo-agnostic (see the `satelle-repo-agnostic` principle): `--local`
installs a released binary asset — it never compiles from source, which would not
exist in a consuming repo. The mechanism is general; nothing about any one repo is
baked into the binary.

## Two operating modes

The pin distinction gives satelle two implicit modes, keyed off the running
binary's location — no flag:

- **Global (default)** — the satelle on `PATH`. `serve` listens on the default
  port (8787) and renders the connected-projects **landing**: every repo
  registered via `satelle workspace add`, each under `/<slug>/`.
- **Local/repo** — the pin at `<repo>/.satelle/satelle`. `serve` listens on a
  **deterministic per-repo port** and shows exactly **one** project (this repo):
  it ignores the workspace registry, so there is no aggregation and no cross-repo
  bleed. This lets a repo run its own instance alongside the global one without
  colliding.

### Local web port

Local mode derives a stable port from the repo path, in the range **8800–8999**,
so each repo gets its own predictable port and many local instances can run at
once. The same repo always resolves to the same port. Precedence:

1. `--port` flag
2. `[web_port]` in `.satelle/satelle.toml` (explicit override)
3. local-mode deterministic per-repo port (local mode only)
4. `8787` (global default)
