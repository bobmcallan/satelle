---
name: satelle-reviewer-verdict
type: principle
tags: [type:principle]
applies_to: ["*"]
description: The verdict rules every judging gate reviewer obeys — what a re-presented edge means and that every verdict sets `reviewed`. Delivered with every judging dispatch, so no reviewer skill restates it; a skill owns only its own verdict JSON shape.
---

# Reviewer verdict rules

These rules bind every judging reviewer on every gated edge. They arrive with the
dispatch, so a reviewer skill does not repeat them and owns only its own verdict
JSON shape.

## Re-presented edge

Every judging verdict sets `reviewed` to the exact words it rests on. The first verdict on the edge has no prior row, so it judges and sets `reviewed`; a later citation cannot start until that quotation is stored. On a re-presented edge the quotation comparison governs. Open `prior_verdicts`. Take this skill's latest row. If that row has no `reviewed` string, or `reviewed_truncated` is set, re-judge the words this verdict would rest on and set `reviewed` to those exact words. Do not cite an older row that still has a complete `reviewed` string. If that latest row has a complete `reviewed` string, compare those exact words with the words this verdict would rest on. If they are the same words, re-issue that earlier verdict: the same decision, the same `reviewed` string, and notes that cite its `attempt` and skill. An unchanged accept stays accept. An unchanged reject stays reject. A citation does not clear a standing rejection. If the words differ, judge the difference, name what changed in notes, and set `reviewed` to the exact words this verdict rests on. Cite the passages the verdict depends on, not the whole input. Definition-edit fields, edit timestamps, and a selection of story sections are not how unchanged words are established.

## `reviewed` is required

`reviewed` is required on every judgment, including the first verdict on this edge. It is the exact words this verdict rests on. Replace the slot; do not omit the field and do not copy the angle-bracket text. A citation of the same words repeats that same `reviewed` string.

See [[satelle-reviewer-self-contained]], [[satelle-agent-model]].
