---
name: satelle-project-workflow
scope: project
type: workflow
tags: [type:workflow]
applies_to: ["*"]
description: The generic default project workflow init seeds into a fresh repo, authored in DOT (the agent model). The most basic lifecycle — a story moves backlog → in_progress → done, with a cancelled exit — carrying NO reviewers. The only declared nodes are the CODED estimate gate (a deterministic functional check requiring a plan estimate entering in_progress and an actual entering done) and the mandatory step summary. A repo layers its own gates and delivery steps by editing this file (it is seeded as editable substrate, and a same-named authored file is the repo's own).
---

# satelle project workflow (default) — the most basic lifecycle

> **This is the seeded default project workflow.** `satelle init` materialises it
> into `.satelle/workflows` as editable substrate: edit it to layer your repo's
> own reviewer gates and delivery steps (intent/code reviews, commit/push gates,
> deploy checks) on top. See the `satelle-agent-model` and
> `satelle-repo-agnostic` principles.

The lifecycle is the **DOT graph** below — read it as the authority; this prose
only orients and must not restate it. A story moves **backlog → in_progress →
done** and may exit early to **cancelled**. The edges carry **no reviewers** —
transitions enact directly. Two declared, edge-less nodes are the only
machinery:

- **estimate** (`on="in_progress,done"`) — a **coded** gate (the skill carries a
  deterministic functional check, no agent involved): entering `in_progress`
  requires a plan estimate (`satelle story estimate`), entering `done` requires
  the actual (`satelle story actual`).
- **step** — the mandatory per-transition step summary.

There are deliberately no release mechanics and no LLM reviewers here. A repo
that wants gated quality management authors its gates into this file itself.

```dot
digraph satelle_project_workflow {
  graph [goal="Drive a story to done through the most basic lifecycle — estimates recorded at begin-work and close", vars="story"]
  rankdir=LR

  backlog     [shape=Mdiamond]
  in_progress [agent=executor]
  done        [shape=Msquare]
  cancelled   [shape=Msquare]

  // step opts this workflow into per-transition step summaries (an edge-less
  // declaration, mandatory so a summary failure is surfaced).
  step        [agent=reviewer, prompt="@skill:satelle-step-summary", mandatory=true]

  // Declared scoped reviewer (edge-less, on="<target states>"): the always-on
  // estimate/actual gate — a CODED functional check, not an LLM rubric.
  estimate    [agent=reviewer, prompt="@skill:satelle-estimate-actual-review", on="in_progress,done"]

  backlog -> in_progress
  in_progress -> done

  backlog     -> cancelled
  in_progress -> cancelled
}
```

## Skill resolution

The two skills this workflow names — `satelle-estimate-actual-review` (the coded
check) and `satelle-step-summary` — are seeded by `satelle init` beside this
file, so nothing dangles on a fresh repo.

## Environment

```yaml
guardrails:
  always:
    - Drive an engaged item to a terminal state (done or cancelled) — don't leave work open indefinitely.
    - Record a plan estimate before beginning work and the actual cost before closing.
  ask_first: []
  never:
    - Place any state after done — done is always the terminal success state.
    - Mark an item done with unmet acceptance criteria.
```
