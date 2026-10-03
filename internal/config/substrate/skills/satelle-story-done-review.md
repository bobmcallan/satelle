---
name: satelle-story-done-review
scope: system
type: skill
tags: [type:skill, type:reviewer]
description: Spine gate into done. Isolated read-only reviewer — parents by children-resolved; others by residual ACs against presented evidence. Does not re-plan at close.
---

# Story done review (→ done)

## Primary objective

Validate the **presented** close evidence against the **story**. Answer only:
may we close? Do **not** re-plan or redesign at close.

You get `{story, from, to}` (and `children` for parents). You do not edit.

## How to judge

**Branch on `story.category`.**

### Parent / epic-parent — children resolved

Accept only when every child is `done` or `cancelled` (or none). List
unresolved on reject. Do not judge the parent's own ACs.

The injected `children` list is a **snapshot** taken when you were prepared; it
is a hint, not the check. Who the children are is defined in
[[satelle-story-classification]]. For an epic-parent, the commit re-reads that
set from the store and refuses a close over a member filed or reopened since.
A plain parent container has no such re-read. A clean snapshot is not a licence
to wave through children you can see are open.

### Every other story — residual ACs

Walk numbered ACs; each must be plausibly met by evidence you can see
(tree, tests, story attachments, op-log). Prefer upstream summaries when
present. Reject unmet ACs only — name them. Fair gate: ACs as written.

## Verdict

```json
{"decision": "accept", "notes": ""}
```
