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


## Re-presented edge

Every judging verdict sets `reviewed` to the exact words it rests on. The first verdict on the edge has no prior row, so it judges and sets `reviewed`; a later citation cannot start until that quotation is stored. On a re-presented edge the quotation comparison governs. Open `prior_verdicts`. Take this skill's latest row. If that row has no `reviewed` string, or `reviewed_truncated` is set, re-judge the words this verdict would rest on and set `reviewed` to those exact words. Do not cite an older row that still has a complete `reviewed` string. If that latest row has a complete `reviewed` string, compare those exact words with the words this verdict would rest on. If they are the same words, re-issue that earlier verdict: the same decision, the same `reviewed` string, and notes that cite its `attempt` and skill. An unchanged accept stays accept. An unchanged reject stays reject. A citation does not clear a standing rejection. If the words differ, judge the difference, name what changed in notes, and set `reviewed` to the exact words this verdict rests on. Cite the passages the verdict depends on, not the whole input. Definition-edit fields, edit timestamps, and a selection of story sections are not how unchanged words are established.

## Verdict

```json
{"decision": "accept", "notes": "", "reviewed": "<exact words this verdict rests on>"}
```

`reviewed` is required on every judgment, including the first verdict on this edge. It is the exact words this verdict rests on. Replace the slot; do not omit the field and do not copy the angle-bracket text. A citation of the same words repeats that same `reviewed` string.
