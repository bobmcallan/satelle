---
name: satelle-parallel-story-workflow
scope: project
type: workflow
tags: [type:workflow]
# Stamp-only: pseudo-category so this does not collide with satelle-project-workflow
# on wildcard * (consistency check). Stamp workflow:satelle-parallel-story-workflow
# selects it; unstamped leaves keep the project workflow.
applies_to: ["parallel-story"]
create_review: satelle-story-create-review
description: >-
  Child lifecycle for parallel epics (stamp-selected only). Opt-in via
  workflow:satelle-parallel-story-workflow — never replaces
  satelle-project-workflow. applies_to is the stamp-only sentinel parallel-story.
  Shape: backlog → plan → in_progress → integration → ready → done. ready =
  branch green and pushed, not merged. Recovery: ready → in_progress. No project
  release path; no changelogcheck on children.
---

# satelle parallel-story workflow — worktree leaf to ready

A **parallel-story** leaf is implementable work driven under a parallel epic.
It is selected **only** by an explicit
`workflow:satelle-parallel-story-workflow` stamp (create or restamp). Unstamped
stories still resolve to [[satelle-project-workflow]]. `applies_to:
["parallel-story"]` is a **stamp-only sentinel** (not a leaf category) so this
workflow never wins category resolution over the project wildcard — stamp
selects by name. Stamping is how dogfood leaves opt into **ready** semantics
instead of the project **release → main** path.

## Why `ready` exists

`done` is always terminal ([[satelle-done-is-last]]). A failed epic final
integration must return a child to work without reopening `done`. So the child
stops at **`ready`**: story branch `story/<id>` is green and **pushed**,
**not** merged to main, **not** done. The epic session merges, runs one final
`make integration`, then drives `ready → done`. Recovery:
`ready → in_progress` (fix in the worktree, re-traverse to `ready`).

## What children must not do

- Push **main**
- Run the project **release** step (version bump + publish path as the child)
- Edit **CHANGELOG.md** for the epic release (epic integrate owns changelog)
- Treat `.version` bumps as a child-owned release (epic integrate owns the
  merged release; product file changes on the branch are fine if the story
  requires them, but the child does not cut the release)

## Lifecycle

Reviewer-first spine reuses intent, plan, and code-AC gates. **integration**
runs worktree-aware tests (`@skill:integrate-worktree`). **ready** is entered
from integration under `satelle-parallel-ready-check` (branch pushed, not
main). **done** uses spine `satelle-story-done-review` when the epic has
merged the branch. There is **no** `changelogcheck` on this workflow — the
epic owns the changelog.

```dot
digraph satelle_parallel_story_workflow {
  graph [goal="Drive a parallel-epic leaf to ready on its branch, then to done only after epic merge", vars="story, repo_root"]
  rankdir=LR

  backlog     [shape=Mdiamond]
  plan        [agent=planner, prompt="@skill:plan"]
  in_progress [agent=executor, prompt="@skill:code-worktree"]
  integration [agent=executor, prompt="@skill:integrate-worktree"]
  ready       [shape=ellipse]
  done        [shape=Msquare, agent=reviewer, prompt="@skill:satelle-story-done-review"]
  cancelled   [agent=reviewer, prompt="@skill:satelle-story-cancel-review"]
  blocked     [agent=reviewer, prompt="@skill:satelle-story-blocked-review", on_enter_agent=blocked-triage, on_enter_prompt="@skill:satelle-story-blocked-triage", from="*"]

  step        [agent=reviewer, prompt="@skill:satelle-step-summary", mandatory=true]

  estimate    [agent=reviewer, prompt="@skill:satelle-estimate-actual-review", on="in_progress,done"]
  // Branch green + pushed (not main). No changelogcheck — epic owns CHANGELOG.
  readycheck  [agent=reviewer, prompt="@skill:satelle-parallel-ready-check", on="ready"]
  design      [agent=reviewer, prompt="@skill:satelle-design-review", on="integration", applies_to="surface:ui"]

  backlog     -> plan         [agent=reviewer, prompt="@skill:satelle-story-intent-review"]
  plan        -> in_progress  [agent=reviewer, prompt="@skill:satelle-story-plan-review"]
  in_progress -> integration  [agent=reviewer, prompt="@skill:satelle-code-ac-review"]
  integration -> ready        // gated by readycheck (on="ready")

  ready       -> done         // spine done-review after epic merge

  // Recovery
  integration -> in_progress
  ready       -> in_progress  // epic integrate reverts offender after failed final integration

  backlog     -> cancelled
  plan        -> cancelled
  in_progress -> cancelled
  integration -> cancelled
  ready       -> cancelled
  blocked     -> cancelled
}
```

## Skill resolution

`plan`, `code-worktree`, `integrate-worktree`, `satelle-parallel-ready-check`,
plus spine reviewers (`satelle-story-intent-review`, `satelle-story-plan-review`,
`satelle-code-ac-review`, `satelle-story-done-review`,
`satelle-estimate-actual-review`, `satelle-story-cancel-review`,
`satelle-step-summary`, design when `surface:ui`).

## Environment

```yaml
guardrails:
  always:
    - Work on branch story/<id> in the story worktree; push that branch only.
    - Stop at ready until the epic merges; do not push main from a child session.
    - Record estimate/actual tags like the project spine (estimate gate).
  ask_first: []
  never:
    - Push main from a parallel-story child.
    - Run the project release path or cut a child-owned changelog release.
    - Place any state after done — done is terminal.
    - Self-enact a gated edge the reviewer has not accepted.
```
