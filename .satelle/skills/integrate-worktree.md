---
name: integrate-worktree
scope: project
type: skill
tags: [type:skill, type:executor]
description: >-
  In-loop executor skill for satelle-parallel-story-workflow's integration step.
  Proves the branch integrates in the worktree (gofmt, vet, unit, make
  integration), pushes the story branch only, never main. Stops for ready gate.
---

# Integrate (worktree / parallel-story leaf)

You are the **executor** for `integration` on a parallel-story leaf. The
code-ac gate accepted the slice; **prove the branch integrates** and **push the
story branch** so the leaf can enter `ready`.

## Do

1. Confirm branch is `story/<sty_id>` (not `main`).
2. Format and vet:
   ```bash
   gofmt -l internal cmd tests 2>/dev/null
   go vet ./...
   ```
3. Unit suite: `go test -count=1 ./...`
4. Integration suite: `make integration`
5. Repair **trivial** fallout only (format, fixture, missed call site of this
   story). Larger failures: report and stop for recovery to `in_progress`.
6. **Push the story branch only:**
   ```bash
   git push -u origin HEAD
   ```

## Forbidden

- Push or merge to **main**
- Touch **CHANGELOG.md** for release
- Run the project release / version-cut path
- Advance status yourself — `integration → ready` is gated by
  `satelle-parallel-ready-check`

## Hand-off

Report what ran and the remote branch tip. Ready means green + **pushed**
branch, not merged.

See [[satelle-agent-model]].
