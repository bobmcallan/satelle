---
name: satelle-story-plan-review
scope: system
type: skill
tags: [type:skill, type:reviewer]
description: Plan gate — on plan → in_progress, or backlog → plan when the readiness step proposes its plan before its gates. Read-only reviewer validates the ATTACHED plan against the story's numbered ACs, never inventing a competing plan. Raises every blocking finding in the first pass; on a re-presented edge the quotation comparison governs.
---

# Story plan review (plan gate)

## Primary objective

Validate the **presented** plan against the **story**. Answer only: may the
story advance across this edge (`from` → `to` in the payload)? Do **not**
create-and-complete this step. Do **not** invent a competing plan and match
against it.

You get `{story, from, to}` on stdin. You do not edit or implement.

## 1. Locate the presented artifact

Plan is the `docs` entry named `plan` (`name`, `type`, `path`). The file at `path` is the plan body. Prefer that — it is how a Bash-less reviewer
reads what it judges. When `truncated: true`, or for a fuller
pull when shell is granted: `satelle story doc <sty_id> plan`.

Do **not** look under in-repo `.satelle/stories/` (obsolete post-relocation).

- **No plan artifact → reject**.

## 2. Judge the presented plan only

Every numbered AC must have a concrete claim **in the attached plan**
(approach and/or files, and what evidence will prove it).

- **Accept** when every AC is covered by the presented plan without
 contradicting the story.
- **Reject** when an AC is unplanned, hand-waved, or contradicted — name it.
 Falsify checkable plan claims against the repo only when the plan asserts
 something already exists; do not rewrite the plan.

Do not reject for style or for a design you prefer.

## 3. First pass: every blocking finding, once

On the **first** presentation of this edge (the payload has no
`prior_verdicts`), enumerate **every** blocking finding — read the whole plan
against every AC and stop nowhere early. A blocker held back for a later pass
is a defect of the review, not of the plan: it costs a full round to surface.

## 4. Later rounds

Every judging verdict sets `reviewed` to the exact words it rests on. The first verdict on the edge has no prior row, so it judges and sets `reviewed`; a later citation cannot start until that quotation is stored. On a re-presented edge the quotation comparison governs. Open `prior_verdicts`. Take this skill's latest row. If that row has no `reviewed` string, or `reviewed_truncated` is set, re-judge the words this verdict would rest on and set `reviewed` to those exact words. Do not cite an older row that still has a complete `reviewed` string. If that latest row has a complete `reviewed` string, compare those exact words with the words this verdict would rest on. If they are the same words, re-issue that earlier verdict: the same decision, the same `reviewed` string, and notes that cite its `attempt` and skill. An unchanged accept stays accept. An unchanged reject stays reject. A citation does not clear a standing rejection. If the words differ, judge the difference, name what changed in notes, and set `reviewed` to the exact words this verdict rests on. Cite the passages the verdict depends on, not the whole input. Definition-edit fields, edit timestamps, and a selection of story sections are not how unchanged words are established.

## Verdict

```json
{"decision": "accept", "notes": "", "reviewed": "<exact words this verdict rests on>"}
```

`reviewed` is required on every judgment, including the first verdict on this edge. It is the exact words this verdict rests on. Replace the slot; do not omit the field and do not copy the angle-bracket text. A citation of the same words repeats that same `reviewed` string.
