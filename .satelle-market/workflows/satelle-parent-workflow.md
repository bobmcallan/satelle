---
name: satelle-parent-workflow
scope: project
type: workflow
tags: [solo-dev, workflow, parent, lifecycle]
applies_to: ["epic-parent", "parent"]
create_review: satelle-story-create-review
description: Parent lifecycle workflow for stories that coordinate child work. Reviewer-first; defines parent states and how child outcomes feed parent progress. Adoptable as the parent graph for multi-story efforts.
---

# satelle parent workflow — close when the children are done

A **container** story — a `parent` or `epic-parent` — has no slice of its own to
implement; its work IS the child stories under it. So its lifecycle is minimal,
authored in the **DOT standard** (node-centric — see the `satelle-agent-model`
principle): it moves **backlog → done**, and may exit early to **cancelled**.
There is deliberately no `in_progress`/`integration`/`commit_push` step — nothing
is built on the parent itself.

The close is the only gate that matters, and it is the **mandatory spine gate**
`satelle-story-done-review` (every workflow's edge into `done` carries it). That
reviewer is **category-aware**: for a `parent`/`epic-parent` it does not judge the
parent's own acceptance criteria — a container's work is its children — but
instead accepts the close ONLY when **every child story is resolved** (`done` or
`cancelled`), judging the children from the close-gate payload (satelle resolves
them from the database). So a parent cannot close while a child is still open; the operator
finishes or cancels the children first. See [[satelle-done-is-last]], [[satelle-story-classification]], and
[[satelle-agent-model]].

```dot
digraph satelle_parent_workflow {
  graph [goal="Close a parent/epic only when every child story is done or cancelled", vars="story"]
  rankdir=LR

  backlog   [shape=Mdiamond]
  done      [shape=Msquare, agent=reviewer, prompt="@skill:satelle-story-done-review"]
  cancelled [agent=reviewer, prompt="@skill:satelle-story-cancel-review"]
  // step opts into per-transition step summaries (<story-id>): edge-less, mandatory.
  step      [agent=reviewer, prompt="@skill:satelle-step-summary", mandatory=true]

  backlog -> done       // spine gate satelle-story-done-review (category-aware: children resolved)
  backlog -> cancelled
}
```

## Environment

```yaml
guardrails:
  always:
    - A parent/epic closes only when every child story is done or cancelled — finish or cancel the children first.
    - Drive a container to a terminal state (done or cancelled); don't leave it open once its children are resolved.
  ask_first: []
  never:
    - Place any state after done — done is always the terminal success state.
    - Self-enact the close the reviewer has not accepted, or close a parent with an unresolved child.
```
