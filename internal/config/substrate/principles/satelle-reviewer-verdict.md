---
name: satelle-reviewer-verdict
type: principle
tags: [type:principle]
applies_to: ["*"]
description: The verdict rules every judging gate reviewer obeys — what a re-presented edge means and that every verdict sets `reviewed`, and what a blocking finding must carry. Delivered with every judging dispatch, so no reviewer skill restates it; a skill owns only its own verdict JSON shape.
---

# Reviewer verdict rules

These rules bind every judging reviewer on every gated edge. They arrive with the
dispatch, so a reviewer skill does not repeat them and owns only its own verdict
JSON shape.

## Re-presented edge

Every judging verdict sets `reviewed` to the exact words it rests on. The first verdict on the edge has no prior row, so it judges and sets `reviewed`; a later citation cannot start until that quotation is stored. On a re-presented edge the quotation comparison governs. Open `prior_verdicts`. Take this skill's latest row. If that row has no `reviewed` string, or `reviewed_truncated` is set, re-judge the words this verdict would rest on and set `reviewed` to those exact words. Do not cite an older row that still has a complete `reviewed` string. If that latest row has a complete `reviewed` string, compare those exact words with the words this verdict would rest on. If they are the same words, re-issue that earlier verdict: the same decision, the same `reviewed` string, and notes that cite its `attempt` and skill. An unchanged accept stays accept. An unchanged reject stays reject. A citation does not clear a standing rejection. If the words differ, judge the difference, name what changed in notes, and set `reviewed` to the exact words this verdict rests on. Cite the passages the verdict depends on, not the whole input. Definition-edit fields, edit timestamps, and a selection of story sections are not how unchanged words are established.

## Carried accepts

A step that declares `reviewer_carry` narrows what withholds `reviewed`: the step performer's re-attached output document and directed messages to that performer no longer do, because the quotation comparison already sees any change to that document. Every other evidence row still withholds it. When an earlier accept's `reviewed` string is still found verbatim in the material and another reviewer is being dispatched, the binary records that accept as carried (`carried_from`) without dispatching you. A carried row is bookkeeping, not a fresh judgment. When every dispatched reviewer accepts, you are dispatched in the same call: judge the whole material, not only your quotation, since the revision may have changed words you did not quote.

## `reviewed` is required

`reviewed` is required on every judgment, including the first verdict on this edge. It is the exact words this verdict rests on. Replace the slot; do not omit the field and do not copy the angle-bracket text. A citation of the same words repeats that same `reviewed` string.

## Blocking findings

A blocking finding cites a location or quotes text, names a path that is actually reached (trace the caller; a hypothetical is not a finding), sits on this reviewer's own axis, and is not style or a preference for another design. A review with no such finding accepts; do not inflate nits to fill it.

## Context sources

When your briefing lists a `Context sources` block, each source is a read-only tool server this edge offers you, with the tools allowed on it. The block also lists the story's references. Before you judge, look up each listed reference through an offered source, using only the tools the block allows for it, and judge the story against what you find there as well as against the material. Then list every lookup you made in the verdict as `consulted`: an array of `{"source": "<offered source name>", "ref": "<system>:<key>"}`, one entry per lookup. Omit `consulted` when you made none. A source or reference the block does not list is not yours to use or to report.

`consulted` is your own report, not a measurement. satelle drops any entry that names a source it did not offer you and counts the drops on the verdict, so report only lookups you made.

See [[satelle-reviewer-self-contained]], [[satelle-agent-model]].
