---
name: satelle-done-is-last
scope: project
type: principle
tags: [type:principle]
applies_to: ["*"]
description: done is ALWAYS the terminal state of a workflow. Quality gates (intent, commit-push, acceptance review) come BEFORE done, never after — reaching done means every gate on the path has already passed. The binary enforces this (the mandatory done gate on the workflow spine); kept as this repo's project substrate documenting the invariant — a repo's workflow may add states and gates, but done stays last.
---

# done is always last

`done` is the **terminal success state** of every satelle workflow. It is the end
of the path, not a checkpoint along it.

- **Gates precede done.** Any quality gate a workflow enforces — a structure
  review, an intent check, a commit-push check that the CI run succeeded, an
  acceptance review — sits on an edge *before* `done`. A state can never follow
  `done` on the success path.
- **done means everything passed.** Reaching `done` is the proof that every gate
  on the route from `in_progress` to `done` accepted. There is no work, no
  validation, and no release left to do once an item is `done`.
- **Only `cancelled` is a peer terminal.** A workflow's other terminal is the
  early exit `cancelled` (abandon with a reason). Both are terminal; neither is
  followed by further states.

A repo layers its own lifecycle on top of the baseline — it MAY add intermediate
states and gates (e.g. `in_progress → commit_push → committed → done`) — but it
orders them so `done` remains the last state. A workflow that places a state
after `done` violates this principle.

See [[satelle-constitution]].
