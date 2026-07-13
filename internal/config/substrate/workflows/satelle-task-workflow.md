---
name: satelle-task-workflow
scope: project
type: workflow
tags: [type:workflow]
applies_to: ["execution", "task"]
create_review: satelle-story-create-review
description: The default lifecycle for a task EXECUTION — one isolated run of a task — authored in DOT (the agent model). An execution moves backlog → in_progress → done, with a cancelled exit. It is DELIBERATELY NOT the story workflow: the begin-run edge is gated by satelle-task-validate-before-review (the run is a well-formed execution of a valid task) and the close edge by satelle-task-validate-after-review (the ACTION was done and its VERIFICATION is satisfied), and it carries NOTHING else — no integration/commit/push states, no code-ac/estimate/commit/push/done-review gates, no version bump, no CI, no release. done is TERMINAL (satelle-done-is-last): a completed run is never moved backward — re-running a task means creating a NEW execution, not reopening this one. Resolved kind-awarely (applies_to ["execution", "task"]) so neither an execution nor a directly-driven task header falls through to the wildcard story workflow.
---

# satelle task-execution workflow — the agent model, authored in DOT

> **This workflow governs a task execution** — one isolated RUN of a task (the
> task header itself is a stable authored work-definition, not a running item).
> An execution resolves to this workflow by its KIND (`applies_to:
> ["execution", "task"]`), so a run — and a task header driven directly — is
> never gated by the wildcard story workflow. See
> the `satelle-agent-model`, `satelle-done-is-last`, and `satelle-repo-agnostic`
> principles.

The lifecycle is the **DOT graph** below — read it as the authority; this prose
only orients and must not restate it. Each node is a step carrying an `agent`: an
**executor** does the work and mutates the tree; a reviewer gates an edge via its
`prompt="@skill:NAME"` (read-only — it judges, never mutates). Status advances only
through a reviewer's accept.

Two reviewer gates bracket the run and are the ONLY gates: the begin-run edge
(`backlog → in_progress`) is gated by **satelle-task-validate-before-review** —
a CODED structural check (no agent run): the run names a parent task header
that exists and is structurally valid, the same contract as
`satelle task validate`; the close edge
(`in_progress → done`) is gated by **satelle-task-validate-after-review** — the
ACTION was carried out and its VERIFICATION is satisfied. There is **no** commit,
push, release, estimate, or integration machinery — an execution is a
work-definition run, not a shippable code slice. The executor working while
`in_progress` MAY be a named `agents.toml` agent (the run's declared executor).

`done` is the **terminal** success state (satelle-done-is-last): it has no
outgoing edge, and a completed run is never moved backward. "Re-running" a task is
a NEW execution, created fresh at `backlog` — not a reopen of a done run.

```dot
digraph satelle_task_workflow {
  graph [goal="Drive a task execution: validate-before → run → validate-after → done; done terminal, re-run is a new execution", vars="execution"]
  rankdir=LR

  backlog     [shape=Mdiamond]
  in_progress [agent=executor]
  done        [shape=Msquare]
  cancelled   [shape=Msquare]

  backlog     -> in_progress [agent=reviewer, prompt="@skill:satelle-task-validate-before-review"]
  in_progress -> done        [agent=reviewer, prompt="@skill:satelle-task-validate-after-review"]

  backlog     -> cancelled
  in_progress -> cancelled
}
```

## Skill resolution

The two gate reviewers this workflow names —
`satelle-task-validate-before-review` and `satelle-task-validate-after-review` —
are seeded by `satelle init` into `.satelle/skills` beside this file, so there is
no dangling `@skill:` reference and a run drives to `done` without a
missing-skill block. Reviewer gates degrade to advisory only if their rubric is
genuinely absent.

## Environment

```yaml
guardrails:
  always:
    - Drive an engaged execution to a terminal state (done or cancelled) — don't leave a run open indefinitely.
    - A run declares its ACTION and how success is VERIFIED before it begins, and satisfies both before it closes.
  ask_first: []
  never:
    - Place any state after done — done is always the terminal success state; re-running a task is a NEW execution, never a backward move of a done run.
    - Self-enact a gated edge the reviewer has not accepted.
    - Mark a run done with its ACTION unaddressed or its VERIFICATION unmet.
```
