---
name: satelle-baseline-workflow
scope: system
kind: workflow
tags: [kind:workflow]
applies_to: ["*"]
description: The canonical order-zero lifecycle every satelle repo inherits from the binary — backlog → in_progress → done, with a cancelled exit. The edges are gated by reviewer skills (satelle-intent-plan-review on entry, satelle-story-done-review on exit) so a story is judged well-formed before work begins and quality-checked before it closes. This is the EMBEDDED canonical default (config/substrate/workflows); a repo MAY override it by placing a same-named file under .satelle/workflows, but never edits this source.
---

# Baseline workflow (order-zero, gated)

The default lifecycle the satelle binary ships: a story or task moves
**backlog → in_progress → done** and may exit early to **cancelled**. Each edge
is **gated** — an isolated reviewer judges the requested transition and either
enacts it or pushes back to the executor. The executor never enacts its own
transition; quality management is the point.

## Workflow

- **backlog → in_progress** — gated by `satelle-intent-plan-review`: the story
  must be well-formed (a clear goal and numbered acceptance criteria) before work
  begins. A reject pushes back with notes; the story stays in backlog.
- **in_progress → done** — gated by `satelle-story-done-review`: the work must
  satisfy the acceptance criteria. A reject pushes back; the story stays
  in_progress.
- **backlog/in_progress → cancelled** — gated by `satelle-story-cancel-review`:
  abandon the item, recording why.

```yaml
states:
  - backlog
  - {name: in_progress, actor: executor}
  - done
  - cancelled
transitions:
  - {from: backlog, to: in_progress, reviewer_skill: "satelle-intent-plan-review"}
  - {from: in_progress, to: done, reviewer_skill: "satelle-story-done-review"}
  - {from: backlog, to: cancelled, reviewer_skill: "satelle-story-cancel-review"}
  - {from: in_progress, to: cancelled, reviewer_skill: "satelle-story-cancel-review"}
```

## Environment

```yaml
guardrails:
  always:
    - Drive an engaged item to a terminal state (done or cancelled) — don't leave work open indefinitely.
    - Give a story/task numbered acceptance criteria before starting, and satisfy them before moving to done.
    - When work stalls, set status to blocked with a note on what it's waiting on, rather than leaving it silently in_progress.
  ask_first: []
  never:
    - Self-enact a transition the reviewer has not accepted.
    - Mark an item done with unmet acceptance criteria.
```
