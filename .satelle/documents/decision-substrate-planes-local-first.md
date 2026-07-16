---
name: decision-substrate-planes-local-first
type: document
tags: [type:document, epic:substrate-planes]
description: Decision — three substrate planes (sparse virtual defaults in the binary; user-authored edits in repo .satelle; runtime state home-keyed). Local-first stays; default complexity moves into the app, not the user's dirs. Cross-repo rule — create stories anywhere, action them nowhere but home.
---

# Decision — substrate planes and local-first simplification

**Date:** 2026-07-16
**Status:** decided
**Origin:** session review — cross-repo containment audit that widened into a complexity audit.

## Context

satellites (predecessor) was all server-side: nothing stored locally, but a
connection was required. satelle swung fully local-first — the right call, and
it stays. The correction needed is where the *default* complexity landed: on
the user's directories instead of inside the app.

Today satelle materializes **three copies of truth** — defaults embedded in the
binary, seeded copies under repo `.satelle/`, and the runtime DB — and the
`.gitignore` seam between authored and runtime files is the scar. Measured in
this repo: 16/38 skills, 9/18 principles, 4/5 workflows are name-matches of
embedded defaults; all 40 documents are records, not configuration.

Two facts shape the fix:

- **An edited default is legitimate substrate, not drift.** A developer is
  expected to edit the standard workflow on day one, which cascades into
  repo-specific reviewer/step skills. That IS satelle usage. Only *unedited*
  seed copies are waste.
- **`.satelle` is git-optional.** Like `.claude`, it is agent/user assistance
  that MAY be committed, mostly is not. satelle must not care whether it is
  tracked; nothing may assume it is a team contract.

## Decision

### Three planes

| Plane | Where | Holds |
|---|---|---|
| **Defaults** | binary only, resolved virtually | deliberately sparse, repo-agnostic workflows/skills/principles — never seeded to disk unedited |
| **Authored** | repo `.satelle/` (git-optional) | satelle.toml, satelle.local.toml, edited workflows/skills/principles, constitution, decision docs — only what a human touched |
| **Runtime** | `~/.satelle/<repo-key>/` | satelle.db (+wal/shm), logs, backups, stories cache, sync cursors |

Effective process = virtual defaults overlaid by disk edits — the git-config
mental model (system → repo → local), with provenance visible via CLI and the
web UI. The binary detects edited-vs-unedited (diff against embedded) so a
day-one edit survives and an untouched seed resolves virtually.

This exercises the option **deferred** in
[decision-local-db-placement](decision-local-db-placement.md) (sty_1eaa15f5):
both preconditions (project bind identity, workstate rehydrate — sty_45bfcc50,
sty_2f1538a4) are now met. Its invariant is kept unchanged: **one project per
DB, never a flat multi-repo bag**.

### Cross-repo containment

- **Fence:** never mutate another repo's tree. With runtime state home-keyed
  there is no `.satelle` carve-out — the rule is one sentence.
- **Stories:** agents MAY create stories on other repos (they hold the
  context); they may NEVER progress or action them. Implementation happens in
  a session opened in that repo.
- **Opt-in:** a `[gate]` toml option (default: deny) allows outside-tree edits
  for a satelle install that deliberately spans multiple repos.
- **Enforcement is layered, honestly:** an embedded principle carries the
  intent; the Bash gate is a best-effort tripwire (resolve `cd`/`git -C`/
  redirect/absolute targets against the pinned session anchor — never CWD,
  which moves). A bypassed hook is expected under `bypass`; the effort is the
  reminder and the boundary, not a wall.

### UI

The web page stays (always-on passive monitor is what a browser is for; a TUI
is a new surface, i.e. added complexity). Investment goes to an
effective-process view with provenance (default vs edit), so checking "what
runs and why" does not mean reading raw markdown — the developer reads the
files; a user should not have to.

## Out of scope, filed separately

The plan-step blow-through is an enforcement defect in the workflow transition
gate (it reminded instead of refusing) — independent of any layout change.

## See also

- decision-local-db-placement.md (sty_1eaa15f5) — deferred option now exercised
- decision-workspace-continuity-posture.md (sty_c5c54f02) — local vs personal continuity
- Stories: epic:substrate-planes (filed with this decision)
