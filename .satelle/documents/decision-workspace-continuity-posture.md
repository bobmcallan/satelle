---
name: decision-workspace-continuity-posture
type: document
tags: [type:document, epic:workspace-rehydrate]
description: Decision for sty_c5c54f02 — workspace continuity is local disk (init gitignore defaults only) or personal hosted rehydrate; supersedes sty_aa7cd897 git-as-SoT / sync-as-mirror guidance.
---

# Decision — workspace continuity: local disk + init defaults; personal = rehydrate

**Story:** sty_c5c54f02  
**Epic:** epic:workspace-rehydrate (`sty_4ae354f4`)  
**Date:** 2026-07-15  
**Status:** decided

## Decision

Two modes, one tree (`.satelle`):

### LOCAL (default)

Continuity is the **developer disk**. `satelle init` **may** write **recommended**
`.gitignore` defaults (the managed block); the **operator owns** `.gitignore`.
Git is **optional** for `.satelle` process and workstate — satelle runs fully
with no requirement that process substrate or the local DB be committed.
Nothing leaves the machine until an area is opted into `[sync]`.

### PERSONAL (opt-in)

The **bound hosted project** is the continuity channel for opted-in areas:

- **Push** = backup of local workspace content to personal hosted.
- **Sync down / rehydrate** = recover after clone, move, or wipe of `.satelle`
  (config deploy, documents pull, and — when shipped — workstate pull).

Git is **not** required for recovery when personal is on. Sync is **backup +
rehydrate**, not a mirror of `git ls-files`.

Git remains appropriate for the **application repo**. It is not the satelle
workspace source of truth.

## Supersedes

**sty_aa7cd897** decided that git is always the source of truth for tracked
`.satelle` content and that scoped sync is a **mirror** of those tracked files
(encoded in `gitignoreBlock` and scaffolded `[sync]` comments). That posture is
**replaced** by this decision for product guidance and recovery UX.

Mechanism stories under epic:workspace-rehydrate implement rehydrate paths;
this story only records the posture and aligns operator-facing text.

## Rationale

`.satelle` is primarily a **developer workspace** (work state, evidence, machine
overlay), not application source. By volume it is almost all local; the thin
process strip (workflows, skills, agents, `satelle.toml`) is operator process,
not product code.

Real use is either:

1. **100% local** — no hosted, optional git for process, or  
2. **Personal multi-machine** — hosted personal for continuity after move/clone.

Neither requires process in git for satelle to work. Framing sync as a git mirror
makes rehydrate incoherent: you cannot recover bytes that were never tracked, and
you should not need git to restore a personal workspace.

## What does not change

- Default scope stays **local** (nothing leaves the machine until opt-in).
- No forced whole-dir `.satelle` ignore flip in this story (may follow once
  rehydrate exists).
- No sync protocol or scope-resolution change here (later children).
- `ensureGitignore` remains marker-guarded: existing repos keep old managed-block
  **comment** text until an operator edits them; accepted (same as sty_aa7cd897
  AC4 — no block-version rewrite engine for comment-only drift).

## Constitution

Init scaffold strings and CLI help are **generic product defaults** (repo-agnostic).
Opinion and supersession history live in this document, not as this-repo pipeline
detail baked into Go.

## See also

- sty_4ae354f4 — epic:workspace-rehydrate  
- sty_aa7cd897 — superseded git-as-SoT / sync-as-mirror guidance  
- sty_1eaa15f5 — local DB placement (per-project; never single global blob)  
- decision-surface-tag-trust.md — decision-doc shape precedent  
- epic:scoped-sync (sty_2ff2232d) — scope ladder foundation  
