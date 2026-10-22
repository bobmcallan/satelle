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

Every judging verdict sets `reviewed` to the exact words it rests on. The first verdict on the edge has no prior row, so it judges and sets `reviewed`; a later citation cannot start until that quotation is stored. On a re-presented edge the quotation comparison governs. Open `prior_verdicts`. Take this skill's latest row. If that row has no `reviewed` string, or `reviewed_truncated` is set, re-judge the words this verdict would rest on and set `reviewed` to those exact words. Do not cite an older row that still has a complete `reviewed` string. If that latest row has a complete `reviewed` string, compare those exact words with the words this verdict would rest on. If they are the same words, re-issue that earlier verdict: the same decision, the same `reviewed` string, and notes that cite its `attempt` and skill. An unchanged accept stays accept. An unchanged reject stays reject. A citation does not clear a standing rejection. If the words differ, judge the difference, name what changed in notes, and set `reviewed` to the exact words this verdict rests on. Cite the passages the verdict depends on, not the whole input. Definition-edit fields, edit timestamps, and a selection of story sections are not how unchanged words are established. When the payload carries `criteria_state`, `## Criteria` decides which criteria you judge, and the whole-verdict rule above governs only a prior row that recorded no `criteria`.

## Criteria

A verdict may carry `criteria`, one entry per acceptance criterion you judged: `{"criterion": <n>, "status": "accept"|"reject"|"unchecked", "finding": "<why>", "reviewed": "<exact words this entry rests on>"}`. `<n>` is the number of the criterion's line; `criteria_source` in the payload says whether those lines are the story's acceptance criteria or a section of the performing step's document, and `criteria_state` lists them with their text. The binary stamps each entry's text itself; do not send one. `unchecked` means you did not judge that criterion. An unchecked or omitted criterion never accepts: when `criteria` is present the gate accepts only if every criterion is accepted and no criterion `0` entry rejects, whatever the top-level `decision` says. An objection that belongs to no single criterion is criterion `0`; it has no text, is never settled, and is judged fresh every round. When `criteria_state` is present, judge only the criteria marked `open`. The binary carries each `settled` criterion's earlier accept itself (`carried_from`) and replaces any entry you send for it. `prior_status` and `prior_finding` on an open criterion are what you said of it last time. A panel seat may also receive `peer_findings`, the other seats' open findings by criterion and never your own; treat them as leads to check against the material, not as verdicts to copy or repeat. A verdict with no `criteria` is decided by its top-level `decision`, except that when every criterion is `settled` the binary carries those accepts and decides from them, so a prose-only reject is dropped: itemise any objection that belongs to no single criterion as criterion `0` in `criteria`, never in the top-level notes alone.

## Review mode and partial verdicts

The work item names the gate's mode in `review_mode`. Absent means full: judge the whole artifact and report every blocking finding. `"fail-fast"` means stop at the first blocking finding and reject. Either way, state what the verdict covered in `coverage`: `{"complete": true}` when you judged the whole artifact, or `{"complete": false, "stopped_at": "<the finding you stopped at>"}` when you stopped early. A fail-fast reject has not reviewed anything past the finding it stopped at.

A **partial prior verdict** is a row in `prior_verdicts` marked `partial: true`. `coverage.stopped_at` names where it stopped, and is present only when the earlier reviewer stated it; when it is absent, start from that row's notes, the finding the reject cites. A partial verdict is not acceptance of anything it did not review, and the quotation comparison above does not apply to it. Judge in two parts: first whether the finding at `stopped_at` (or in that row's notes) is fixed, then the whole remainder of the material as a first review would, not only the difference since that row. Never carry or re-issue a partial verdict as an accept.

## Carried accepts

A step that declares `reviewer_carry` narrows what withholds `reviewed`: the step performer's re-attached output document and directed messages to that performer no longer do, because the quotation comparison already sees any change to that document. Every other evidence row still withholds it. When an earlier accept's `reviewed` string is still found in the material word for word (line breaks and spacing are ignored) and another reviewer is being dispatched, the binary records that accept as carried (`carried_from`) without dispatching you. A carried row is bookkeeping, not a fresh judgment. When every dispatched reviewer accepts, you are dispatched in the same call: judge the whole material, not only your quotation, since the revision may have changed words you did not quote. When the payload carries `criteria_state`, judge its open criteria instead: a settled criterion is one whose text and quotation are unchanged. The whole-material judgment applies to a verdict without `criteria`.

## `reviewed` is required

`reviewed` is required on every judgment, including the first verdict on this edge. It is the exact words this verdict rests on. Replace the slot; do not omit the field and do not copy the angle-bracket text. A citation of the same words repeats that same `reviewed` string.

## Blocking findings

A blocking finding cites a location or quotes text, names a path that is actually reached (trace the caller; a hypothetical is not a finding), sits on this reviewer's own axis, and is not style or a preference for another design. A review with no such finding accepts; do not inflate nits to fill it.

See [[satelle-reviewer-self-contained]], [[satelle-agent-model]].
