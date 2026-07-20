---
name: epic-integrate
scope: project
type: skill
tags: [type:skill]
description: >-
  In-loop executor skill for satelle-epic-parallel-workflow's integrate step.
  Merges ready children into main in strategy order, runs make integration once
  on the merged tree, pushes on green, walks children ready→done. On failure
  reverts the offender to in_progress and recovers via integrate→orchestrate.
  Owns version/changelog for the merged release.
---

# Epic integrate (in-loop executor step)

You are the **executor** for the **integrate** step of a parallel epic
(in-loop). Children are at `ready` (branch green + pushed, not merged). **Merge,
test once, push, close children.** You never self-enact `integrate → done`.

## Do

1. **Engage** the epic if not already engaged (commit/push gates require it).
2. **Merge in strategy order** (from the plan waves / order tags):
   ```bash
   git checkout main && git pull --ff-only
   # for each ready child branch story/<id> in order:
   git merge --no-ff story/<id> -m "merge(story/<id>): <title> (<id>)"
   ```
   If a sibling already landed and the next merge conflicts, rebase the
   remaining branch onto updated main in its worktree (or merge-resolve
   honestly), re-test that branch if needed, then continue.
3. **Version + changelog (epic-owned).** For the merged release slice: bump
   `.version` as required by the product change, update `CHANGELOG.md` (+ embed
   sync if this repo requires it), stage only the epic release files + merged
   content. Conventional commit subject ending with the **epic** id. **No AI
   attribution.**
4. **One final integration on the merged tree:**
   ```bash
   make integration
   ```
   Exactly **one** full integration attempt per integrate pass (not per child).
5. **On green:** `git push origin main`. Then walk each merged child:
   ```bash
   satelle story set <child_id> --status done   # gates: done-review
   ```
   (or the workflow transition the child graph declares for `ready → done`).
6. **On failure:** identify the offending child (merge conflict author or test
   regression introduced by a branch). Send that child
   `ready → in_progress` for fix in its worktree. Request
   `integrate → orchestrate` recovery (do not force done). After fix, children
   re-traverse to `ready`; re-enter integrate and re-run merge → test → push.

## Do not

- Skip `make integration` on the merged tree.
- Let children push main or bump version themselves.
- Close the epic while any non-cancelled child is not `done`.
- Self-enact past `satelle-story-done-review` / `satelle-epic-integration-check`.

See [[satelle-agent-model]], [[satelle-done-is-last]].
