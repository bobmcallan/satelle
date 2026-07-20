---
name: code-worktree
scope: project
type: skill
tags: [type:skill]
description: >-
  In-loop executor skill for satelle-parallel-story-workflow's in_progress step.
  Implements the plan in the story worktree on branch story/<id>. Commits stay
  on the branch; never push main; never edit CHANGELOG.md for epic release;
  never run the project release path. Stops for code-ac-review.
---

# Code (worktree / parallel-story leaf)

You are the **executor** for `in_progress` on a **parallel-story** leaf
(in-loop in a worktree driving session). Implement the plan's slice on
**branch `story/<id>`**. You **build only** — do not advance status past the
code-ac gate.

## Context

1. Confirm you are **not** on `main`:
   ```bash
   git rev-parse --abbrev-ref HEAD   # expect story/<sty_id>
   ```
2. Load story + plan:
   ```bash
   satelle story get <sty_id>
   satelle story doc <sty_id> plan
   ```
3. Record plan consumption (CLI log or stdout `PLAN-CONSUMED: …`).

## Implement

- Build exactly the plan's slice; satisfy every numbered AC.
- Add unit + integration tests for behavioural code changes (docs/substrate-only
  exempt).
- Commit on **this branch** with a conventional subject ending in `(<sty_id>)`.
  **No AI attribution.**
- Format Go with `gofmt -s -w` when shell is available.

## Forbidden (hard)

- `git push origin main` or any merge to main
- Editing **CHANGELOG.md** for the epic/project release (epic integrate owns it)
- Running the project **release** skill path (version-cut publish as this child)
- Advancing status yourself past what gates allow

Product edits to `.version` only if the story's ACs require them **on the
branch**; the epic still owns the merged release commit and changelog.

## Stop

Leave the tree ready for `in_progress → integration` (`satelle-code-ac-review`).

See [[satelle-agent-model]].
