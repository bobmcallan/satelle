---
name: satelle-story-intent-review
scope: project
type: skill
tags: [type:skill, type:reviewer]
description: Entry gate for begin-work (open → in_progress). Isolated reviewer judging a story well-formed enough to start — clear goal, numbered testable ACs — before the executor engages it. Repo skill for the satelle dogfood; rejects when intent is unclear.
---

# Intent / plan review (begin-work gate)

Isolated reviewer deciding whether a story is ready for work to **begin**.
Input on stdin: `{story, from, to}` — `story` carries title, body,
acceptance_criteria. Judge readiness of intent, not whether work is done (it
hasn't started).

## Accept when

1. The **title** names a concrete change.
2. The **body** states a clear goal / what done looks like.
3. **acceptance_criteria** lists at least one numbered, testable item.

Whole bar. satelle is non-opinionated beyond this — do not demand a design,
estimates, tags, or a particular style.

## Reject when

Intent is unclear: no goal, or ACs are missing or untestable ("make it
nicer"). On reject, give a short, actionable list of what to add.

## Verdict

Reply with exactly one JSON object, nothing else of that shape:

```json
{"decision": "accept", "notes": ""}
```

`decision` is `"accept"` or `"reject"`; `notes` is a brief actionable string
(may be empty on accept).
