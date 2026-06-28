---
name: satelle-baseline-workflow
scope: system
kind: workflow
tags: [kind:workflow]
applies_to: ["*"]
description: The canonical order-zero lifecycle every satelle repo inherits from the binary — backlog → in_progress → done, with a cancelled exit — authored in the DOT standard (the recursive-actor model). The begin-work edge is gated by satelle-story-intent-review and the close by satelle-story-done-review (a reviewer node), so a story is judged well-formed before work and quality-checked before it closes. This is the EMBEDDED canonical default (config/substrate/workflows); a repo MAY override it by placing a same-named file under .satelle/workflows, but never edits this source.
---

# Baseline workflow (order-zero, gated, DOT)

The default lifecycle the satelle binary ships, authored in the **DOT standard**
(node-centric — see the `satelle-recursive-actor-model` principle): a story or task
moves **backlog → in_progress → done** and may exit early to **cancelled**. Each
gate is an isolated reviewer; the executor never enacts its own transition —
quality management is the point. This is the minimal order-zero lifecycle; a repo
layers richer steps (e.g. a commit-push gate) in its own project workflow.

```dot
digraph satelle_baseline {
  graph [goal="The order-zero lifecycle every satelle repo inherits", vars="story"]
  rankdir=LR

  backlog     [shape=Mdiamond]
  in_progress [actor=executor]
  done        [shape=Msquare, actor=reviewer, prompt="@skill:satelle-story-done-review"]
  cancelled   [actor=reviewer, prompt="@skill:satelle-story-cancel-review"]

  backlog -> in_progress [reviewer_skill="satelle-story-intent-review"]
  in_progress -> done

  backlog     -> cancelled
  in_progress -> cancelled
}
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
