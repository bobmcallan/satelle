---
name: satelle-baseline-workflow
scope: system
kind: workflow
tags: [kind:workflow]
applies_to: ["*"]
description: The order-zero baseline lifecycle for satelle work items — open to in_progress to done, with a blocked detour and a cancelled exit. Gateless by design — the OSS local tier ships no reviewer/gate engine, so transitions are advisory guidance, driven with `satelle story set --status` (or `satelle task set`). The richer gated lifecycle is a paid-server concern.
---

# Baseline workflow (order-zero)

The default lifecycle every satelle repo gets: a story or task moves
**open → in_progress → done**, may detour through **blocked**, and may exit early
to **cancelled**. It mirrors the satellites baseline, but is **gateless** — the
OSS local tier ships no reviewer engine, so each transition is advisory guidance
the operator/agent follows, driven through the verb surface
(`satelle story set <id> --status …`). When the paid server tier lands, the same
states gain reviewer gates at the edges; nothing about the authored states
changes.

## Workflow

- **open → in_progress** — begin work on the item.
- **in_progress → blocked** — record that work is stalled on a dependency.
- **blocked → in_progress** — resume once unblocked.
- **in_progress → done** — close the item with its acceptance criteria satisfied.
- **open/in_progress → cancelled** — abandon the item (record why).

```yaml
states:
  - open
  - {name: in_progress, actor: executor}
  - blocked
  - done
  - cancelled
transitions:
  - {from: open, to: in_progress}
  - {from: in_progress, to: blocked}
  - {from: blocked, to: in_progress}
  - {from: in_progress, to: done}
  - {from: open, to: cancelled}
  - {from: in_progress, to: cancelled}
```

## Environment

Drives a work item open → in_progress → done. Transitions are advisory in the
OSS tier — there is no gate that blocks a move — so the guardrails below are
discipline, not enforcement.

```yaml
guardrails:
  always:
    - Drive an engaged item to a terminal state (done or cancelled) — don't leave work open indefinitely.
    - Give a story/task numbered acceptance criteria before starting, and satisfy them before moving to done.
    - When work stalls, set status to blocked with a note on what it's waiting on, rather than leaving it silently in_progress.
  ask_first: []
  never:
    - Move an item straight from open to done — pass through in_progress so the work is visible.
    - Mark an item done with unmet acceptance criteria.
```
