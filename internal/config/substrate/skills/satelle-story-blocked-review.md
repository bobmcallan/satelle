---
name: satelle-story-blocked-review
scope: system
type: skill
tags: [type:skill, type:reviewer]
description: Gate for parking an engaged story (in_progress → blocked). Isolated reviewer allowing the move when a reason is recorded — world-not-ready, a dependency, or preemption — and refusing a bare block with no reason on record.
---

# Story blocked review (park gate)

Isolated reviewer deciding whether a story may move to **blocked** — parked
because the world is not ready (dependency, external gap) **or** because higher-
priority work needs the engagement seat (preemption), not because the ACs are
wrong. Input on stdin: `{story, from, to}` — `story` carries title, body,
acceptance_criteria, tags. Parking is legitimate when work cannot proceed yet
**or** must yield the seat; the ACs stay frozen and the story resumes later with
the same definition.

## Accept when

The story carries a recorded **reason** for parking — a note in the body, a
recent ledger entry, or an explicit statement of why. Legitimate reasons include:

- **World not ready** — waiting on dependency `blocked-by:<id>`, external
  capability missing
- **Preemption** — higher-priority work needs the engagement seat; ideally
  tagged `preempted-by:<id>` naming the story that needs it. Preemption needs
  **no impediment** — the held story may be healthy. It arrives by
  `satelle story stop-request`, which [[satelle-recognise-blockage]] derives

Bar is low: a clear, human-readable reason is enough. May read the repo
(Read/Grep/Glob) and run read-only `satelle` commands to check the story's
ledger if needed.

## Reject when

No reason on record at all — a bare block that would park the work item with
nothing explaining why. On reject, ask for a one-line reason for the audit
trail.


## Re-presented edge

Every judging verdict sets `reviewed` to the exact words it rests on. The first verdict on the edge has no prior row, so it judges and sets `reviewed`; a later citation cannot start until that quotation is stored. On a re-presented edge the quotation comparison governs. Open `prior_verdicts`. Take this skill's latest row. If that row has no `reviewed` string, or `reviewed_truncated` is set, re-judge the words this verdict would rest on and set `reviewed` to those exact words. Do not cite an older row that still has a complete `reviewed` string. If that latest row has a complete `reviewed` string, compare those exact words with the words this verdict would rest on. If they are the same words, re-issue that earlier verdict: the same decision, the same `reviewed` string, and notes that cite its `attempt` and skill. An unchanged accept stays accept. An unchanged reject stays reject. A citation does not clear a standing rejection. If the words differ, judge the difference, name what changed in notes, and set `reviewed` to the exact words this verdict rests on. Cite the passages the verdict depends on, not the whole input. Definition-edit fields, edit timestamps, and a selection of story sections are not how unchanged words are established.

## Verdict

Reply with exactly one JSON object, nothing else of that shape:

```json
{"decision": "accept", "notes": "", "reviewed": "<exact words this verdict rests on>"}
```

`decision` is `"accept"` or `"reject"`; `notes` names what is missing on
reject (may be empty on accept).

`reviewed` is required on every judgment, including the first verdict on this edge. It is the exact words this verdict rests on. Replace the slot; do not omit the field and do not copy the angle-bracket text. A citation of the same words repeats that same `reviewed` string.
