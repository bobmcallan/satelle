---
name: satelle-baseline-workflow
scope: system
type: workflow
tags: [type:workflow]
applies_to: ["*"]
create_review: satelle-story-create-review
description: Canonical order-zero lifecycle every satelle repo inherits: backlog → in_progress → done, with cancelled and blocked exits. Reviewer-first gates; estimate check on begin-work and close. Seeded at init as editable substrate.
---

# Baseline workflow (order-zero, gated, DOT)

The default lifecycle the satelle binary ships, authored in the **DOT standard**
(node-centric — see the `satelle-agent-model` principle): a story or task
moves **backlog → in_progress → done** and may exit early to **cancelled**. Each
gate is an isolated reviewer; the executor never enacts its own transition —
quality management is the point. This is the minimal order-zero lifecycle; a repo
layers richer steps (e.g. commit + push gates) in its own project workflow.

```dot
digraph satelle_baseline {
 graph [goal="The order-zero lifecycle every satelle repo inherits", vars="story"]
 rankdir=LR

 backlog [shape=Mdiamond]
 in_progress [agent=executor]
 done [shape=Msquare, agent=reviewer, prompt="@skill:satelle-story-done-review"]
 cancelled [agent=reviewer, prompt="@skill:satelle-story-cancel-review"]
 // Park state: world-not-ready. agent=reviewer so it is not engaged (edit/commit gates).
 blocked [agent=reviewer, prompt="@skill:satelle-story-blocked-review", from="*"]
 // step is a transparent, edge-less declaration: it opts this
 // workflow into per-transition step summaries (satelle-step-summary), marked
 // mandatory so a summary failure is surfaced. It is not a lifecycle state.
 step [agent=reviewer, prompt="@skill:satelle-step-summary", mandatory=true]
 // estimate is a declared scoped reviewer (edge-less, on="<target states>"): the
 // workflow itself declares the always-on plan-estimate/actual gate, so the DOT is
 // the sole gating authority — no skill tag injects an undeclared gate.
 // Advisory where the skill is absent (a fresh repo); enforced where present.
 // Coded ```check: no agent= (engine returns before gateBinding; satelle-dot-standard).
 estimate [prompt="@skill:satelle-estimate-actual-review", on="in_progress,done"]

 backlog -> in_progress [agent=reviewer, prompt="@skill:satelle-story-intent-review"]
 // Implementation exit: CSV edge reviewers (edge-wins — node's done prompt is
 // ignored for this edge, so done-review MUST stay in the CSV). workflow-change
 // n/a-fast-accepts when the slice touches no workflow file.
 // scope-review: bounded slice vs ACs using engagement baseline.
 // Optional: add parallel=true (or parallel=N) to run CSV reviewers concurrently
 // with no short-circuit; default is sequential first-reject.
 in_progress -> done [agent=reviewer, prompt="@skill:satelle-workflow-change-review,satelle-story-scope-review,satelle-story-done-review"]
 backlog -> cancelled
 in_progress -> cancelled
 blocked -> cancelled
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
