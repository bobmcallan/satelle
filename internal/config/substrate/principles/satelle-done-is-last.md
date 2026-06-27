---
name: satelle-done-is-last
scope: system
kind: principle
tags: [kind:principle, principles:always]
applies_to: ["*"]
description: done is ALWAYS the terminal state of a workflow. Quality gates (integration, deploy, review) come BEFORE done, never after — reaching done means every gate on the path has already passed. This is an embedded canonical default the satelle binary ships; a repo's workflow may add states and gates, but done stays last.
---

# done is always last

`done` is the **terminal success state** of every satelle workflow. It is the end
of the path, not a checkpoint along it.

- **Gates precede done.** Any quality gate a workflow enforces — a structure
  review, an integration check that the tests pass, a deploy + health check, an
  acceptance review — sits on an edge *before* `done`. A state can never follow
  `done` on the success path.
- **done means everything passed.** Reaching `done` is the proof that every gate
  on the route from `in_progress` to `done` accepted. There is no work, no
  validation, and no deployment left to do once an item is `done`.
- **Only `cancelled` is a peer terminal.** A workflow's other terminal is the
  early exit `cancelled` (abandon with a reason). Both are terminal; neither is
  followed by further states.

A repo layers its own lifecycle on top of the baseline — it MAY add intermediate
states and gates (e.g. `in_progress → integrated → deployed → done`) — but it
orders them so `done` remains the last state. A workflow that places a state
after `done` violates this principle.

See [[satelle-constitution]].
