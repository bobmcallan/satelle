---
name: satelle-epic-parallel-workflow
scope: project
type: workflow
tags: [type:workflow]
# Stamp-only: pseudo-category so this does not collide with satelle-parent-workflow
# on epic-parent (consistency check). GoverningWorkflow selects by workflow:<name>
# stamp regardless of applies_to; unstamped epic-parent stays on parent workflow.
applies_to: ["epic-parallel"]
create_review: satelle-story-create-review
description: >-
  Parallel epic lifecycle (stamp-selected only). Opt-in via
  workflow:satelle-epic-parallel-workflow at create/restamp —
  satelle-parent-workflow remains the default for unstamped epic-parent/parent.
  applies_to is the stamp-only sentinel epic-parallel (not a story category).
  Shape: backlog → plan → orchestrate → integrate → done. Recovery:
  integrate → orchestrate. Children use satelle-parallel-story-workflow.
---

# satelle epic-parallel workflow — stamp-selected parallel container

A **parallel epic** is an `epic-parent` that actively drives children through
worktree-parallel implementation, then merges and runs **one** final
integration on main. It is **not** the default container path: unstamped
`epic-parent` / `parent` stories still resolve to [[satelle-parent-workflow]]
(`backlog → done` when children are resolved). This workflow is selected only
by an explicit `workflow:satelle-epic-parallel-workflow` stamp (create or
`satelle story restamp … --workflow satelle-epic-parallel-workflow`).
`applies_to: ["epic-parallel"]` is a **stamp-only sentinel** (not a story
category) so category resolution never picks this over the parent workflow —
`GoverningWorkflow` honours the stamp by name (see `internal/wfgovern`).

Lifecycle (DOT is authority):

1. **plan** — `@skill:epic-strategy`: read children + `order:` / dependency
   signals; emit explicit waves (agent **chooses** parallel vs sequential mix).
   Version/changelog work is assigned to **integrate**, never to children.
2. **orchestrate** — `@skill:epic-orchestrate`: execute waves; monitor via
   `satelle story list` / seat (shared runtime plane across worktrees). Does
   **not** spawn sessions — the harness launcher (`.claude` skill) materialises
   worktrees + driving sessions.
3. **integrate** — `@skill:epic-integrate`: merge ready children into main in
   strategy order; `make integration` **once** on the merged tree; push on
   green; walk each child `ready → done`. On failure: identify the offender,
   send it `ready → in_progress`, recover via **integrate → orchestrate**.
4. **done** — spine `satelle-story-done-review` (children resolved) plus
   scoped `satelle-epic-integration-check` (`make integration` on the merged
   tree at close).

`orchestrate → integrate` is gated by `epic-children-ready-review` (every
non-cancelled child at `ready`). See [[satelle-done-is-last]],
[[satelle-agent-model]].

```dot
digraph satelle_epic_parallel_workflow {
  graph [goal="Drive a stamped parallel epic: strategy waves, worktree children to ready, epic-owned merge and final integration, close when children are done", vars="story"]
  rankdir=LR

  backlog     [shape=Mdiamond]
  plan        [agent=planner, prompt="@skill:epic-strategy"]
  orchestrate [agent=executor, prompt="@skill:epic-orchestrate"]
  integrate   [agent=executor, prompt="@skill:epic-integrate"]
  done        [shape=Msquare, agent=reviewer, prompt="@skill:satelle-story-done-review"]
  cancelled   [agent=reviewer, prompt="@skill:satelle-story-cancel-review"]
  blocked     [agent=reviewer, prompt="@skill:satelle-story-blocked-review", on_enter_agent=blocked-triage, on_enter_prompt="@skill:satelle-story-blocked-triage", from="*"]

  step        [agent=reviewer, prompt="@skill:satelle-step-summary", mandatory=true]

  // Final integration on the merged tree at close (configuration, not a hidden binary rule).
  epicintcheck [agent=reviewer, prompt="@skill:satelle-epic-integration-check", on="done"]

  backlog     -> plan         [agent=reviewer, prompt="@skill:satelle-story-intent-review"]
  plan        -> orchestrate  [agent=reviewer, prompt="@skill:satelle-story-plan-review"]
  orchestrate -> integrate    [agent=reviewer, prompt="@skill:epic-children-ready-review"]
  integrate   -> done         // spine done-review + scoped epicintcheck

  // Recovery: failed final integration → re-orchestrate (fix offender in worktree).
  integrate   -> orchestrate

  backlog     -> cancelled
  plan        -> cancelled
  orchestrate -> cancelled
  integrate   -> cancelled
  blocked     -> cancelled
}
```

## Skill resolution

Gates and steps named above resolve through the doc-index (project
`.satelle/skills` over embedded defaults): `epic-strategy`, `epic-orchestrate`,
`epic-integrate`, `epic-children-ready-review`, `satelle-epic-integration-check`,
plus spine `satelle-story-intent-review`, `satelle-story-plan-review`,
`satelle-story-done-review`, `satelle-story-cancel-review`,
`satelle-step-summary`, and blocked-park skills.

## Environment

```yaml
guardrails:
  always:
    - Stamp this workflow explicitly on the epic; do not assume category alone selects it.
    - Children of a parallel epic use satelle-parallel-story-workflow (ready = branch green + pushed, not merged).
    - Version and CHANGELOG for the merged release belong to the integrate stage, never to parallel children.
    - Satelle governs parallelism; the harness launcher spawns worktrees/sessions.
  ask_first: []
  never:
    - Place any state after done — done is always terminal.
    - Self-enact a gated edge the reviewer has not accepted.
    - Close the epic while a non-cancelled child is not done (done-review) or not ready (children-ready-review before integrate).
    - Let parallel children push main or run the project release path.
```
