---
name: satelle-project-workflow
scope: project
type: workflow
tags: [type:workflow]
applies_to: ["*"]
description: The generic default project workflow init seeds into a fresh repo, authored in DOT (the agent model). A story moves backlog → in_progress → integration → done, with a cancelled exit. Intent is judged before work begins (satelle-story-intent-review), the implementation is judged against the acceptance criteria before integration (satelle-code-ac-review), and the close is quality-checked (satelle-story-done-review). The declared estimate gate requires a plan estimate at begin-work and an actual at close. Deliberately repo-agnostic — no commit/push/release mechanics; a repo layers its own delivery steps by editing this file (it is seeded as editable substrate, and a same-named authored file is the repo's own).
---

# satelle project workflow (default) — the agent model, authored in DOT

> **This is the seeded default project workflow.** `satelle init` materialises it
> into `.satelle/workflows` as editable substrate: edit it to layer your repo's
> own delivery steps (commit/push gates, deploy checks) on top. See the
> `satelle-agent-model` and `satelle-repo-agnostic` principles.

The lifecycle is the **DOT graph** below — read it as the authority; this prose
only orients and must not restate it. Each node is a step carrying an `agent`: an
**executor** does the work and mutates the tree; a **reviewer** node gates entry
via its `prompt="@skill:NAME"` (read-only — it judges, never mutates). Status
advances only through a reviewer's accept.

The always-on gates are **declared, not injected**: the edge-less reviewer node
`estimate` (`on="in_progress,done"`) runs on the transitions its `on=` names, so
the DOT is the sole gating authority. It requires a plan estimate entering
`in_progress` (`satelle story estimate`) and an actual entering `done`
(`satelle story actual`). There are deliberately **no release mechanics** here —
no version bump, CI watch, or deploy state. A repo that ships code layers those
steps into this file itself.

```dot
digraph satelle_project_workflow {
  graph [goal="Drive a story to done — intent judged before work, the implementation judged against the acceptance criteria, the close quality-checked", vars="story"]
  rankdir=LR

  backlog     [shape=Mdiamond]
  in_progress [agent=executor]
  integration [agent=executor]
  done        [shape=Msquare, agent=reviewer, prompt="@skill:satelle-story-done-review"]
  cancelled   [agent=reviewer, prompt="@skill:satelle-story-cancel-review"]

  // step opts this workflow into per-transition step summaries (an edge-less
  // declaration, mandatory so a summary failure is surfaced).
  step        [agent=reviewer, prompt="@skill:satelle-step-summary", mandatory=true]

  // Declared scoped reviewer (edge-less, on="<target states>"): the always-on
  // estimate/actual gate the workflow itself declares.
  estimate    [agent=reviewer, prompt="@skill:satelle-estimate-actual-review", on="in_progress,done"]

  backlog -> in_progress [reviewer_skill="satelle-story-intent-review"]
  in_progress -> integration [reviewer_skill="satelle-code-ac-review"]
  integration -> done

  backlog     -> cancelled
  in_progress -> cancelled
  integration -> cancelled
}
```

## Skill resolution

Every gate this workflow names resolves through the doc-index, project scope
(`.satelle/skills`) layered over the embedded system defaults. `satelle init`
seeds each referenced reviewer skill beside this file, so there is no dangling
`@skill:`/`reviewer_skill` reference on a fresh repo. Reviewer gates degrade to
advisory only if their rubric is genuinely absent.

## Environment

```yaml
guardrails:
  always:
    - Drive an engaged item to a terminal state (done or cancelled) — don't leave work open indefinitely.
    - Give a story numbered acceptance criteria before starting, and satisfy them before moving to done.
  ask_first: []
  never:
    - Place any state after done — done is always the terminal success state.
    - Self-enact a gated edge the reviewer has not accepted.
    - Mark an item done with unmet acceptance criteria.
```
