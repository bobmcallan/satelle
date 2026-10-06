---
name: satelle-story-intent-review
scope: system
type: skill
tags: [type:skill, type:reviewer]
description: Entry gate for begin-work (backlog → plan or in_progress). Isolated reviewer validates PRESENTED story text is well-formed (title, goal body, numbered testable ACs). Does not rewrite the story.
---

# Intent review (begin-work gate)

## Primary objective

Validate the **presented** story draft. Answer only: may work begin / enter
planning? Do not rewrite body/ACs; do not invent a plan.

You get `{story, from, to}` on stdin. You do not edit.

## Accept when

1. The **title** names a concrete change.
2. The **body** states a clear goal / what done looks like.
3. **acceptance_criteria** lists at least one numbered, testable item.

Whole bar. Do not demand a design, estimates, tags, or a particular style.

## Reject when

Intent is unclear: no goal, or ACs are missing or untestable ("make it
nicer"). On reject, name the failed check(s) only.

## First pass: every failed check, once

On the **first** presentation of this edge (no `prior_verdicts` in the payload),
name **every** failed check — never stop at the first. A failure held back for a
later pass costs a full round.

## Edited definitions: judge each edit against the Purpose

While a story is still in its editable state, a rejection may be followed by an
edit to its title, body, acceptance criteria or category, so the story can be
corrected instead of parked. Every such edit is recorded, and when the payload
carries `definition_edits` as `{path}`; open it; the file is the array of `{field, old, new, actor}`, judge
**each edited acceptance criterion against the story's purpose** — the goal its
body states — and not against the last rejection alone:

- **Reject** an edit that **weakens** an AC only to get past a gate: removed
  scope, a lowered threshold, a deleted test or evidence obligation, a promise
  narrowed until it can no longer fail. Quote the **before** and the **after**.
- **Accept** an edit that removes a contradiction, an untestable phrase or a
  promise the code cannot keep, and still leaves the purpose fully delivered.
  An edit is not suspect for being an edit.

## Later rounds

Every judging verdict sets `reviewed` to the exact words it rests on. The first verdict on the edge has no prior row, so it judges and sets `reviewed`; a later citation cannot start until that quotation is stored. On a re-presented edge the quotation comparison governs. Open `prior_verdicts`. Take this skill's latest row. If that row has no `reviewed` string, or `reviewed_truncated` is set, re-judge the words this verdict would rest on and set `reviewed` to those exact words. Do not cite an older row that still has a complete `reviewed` string. If that latest row has a complete `reviewed` string, compare those exact words with the words this verdict would rest on. If they are the same words, re-issue that earlier verdict: the same decision, the same `reviewed` string, and notes that cite its `attempt` and skill. An unchanged accept stays accept. An unchanged reject stays reject. A citation does not clear a standing rejection. If the words differ, judge the difference, name what changed in notes, and set `reviewed` to the exact words this verdict rests on. Cite the passages the verdict depends on, not the whole input. Definition-edit fields, edit timestamps, and a selection of story sections are not how unchanged words are established.

## Verdict

```json
{"decision": "accept", "notes": "", "reviewed": "<exact words this verdict rests on>"}
```

`reviewed` is required on every judgment, including the first verdict on this edge. It is the exact words this verdict rests on. Replace the slot; do not omit the field and do not copy the angle-bracket text. A citation of the same words repeats that same `reviewed` string.
