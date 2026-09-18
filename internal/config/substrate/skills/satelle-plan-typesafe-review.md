---
name: satelle-plan-typesafe-review
scope: system
type: skill
tags: [type:skill, type:reviewer]
description: Optional TypeSafe/Jev cold gate on entry to in_progress for stories tagged jev:prototype. Skill-authored typed questions; the typesafe runner emits the decision JSON.
---

# Plan typesafe review (optional Jev prototype)

Optional override on entry to `in_progress` for stories tagged
`jev:prototype`. The `[reviewer-typesafe]` binding runs this skill over the
System One HTTP transport. Other reviewers on that edge (`satelle-story-plan-review`
and siblings) stay on the default LLM `[reviewer]` and are not edited by this
prototype.

## What you judge

The transition payload already carries `story`, `docs` (including the attached
planning document), `diff`, and `prior_verdicts`. There is no tools grant — missing evidence
that would have required hunting the tree is a **reject**, not a Go-side grep.

Accept only when the attached planning document covers every numbered acceptance criterion
with concrete files/evidence claims. Reject when an AC is unplanned, hand-waved,
or contradicted.

## Verdict contract

The typesafe runner — not the model — emits the gate verdict JSON that satelle
parses:

```json
{"decision": "accept", "notes": "…"}
```

`decision` is `accept` or `reject`; `notes` carries synthesised typed-answer
summary (choice, confidence, noul atoms). Satelle enacts status only on accept,
same as every other cold gate. Do not invent a second verdict path in Go.

## Typed rubric (System One)

Questions and the optional low-confidence floor are authored here. Go carries no
default threshold and does not compose Nouls into pass/fail.

```typesafe
{
  "questions": {
    "verdict": {
      "type": "choice",
      "instructions": "Given the story and its attached plan in state.payload, should satelle admit this story to in_progress? Accept only when every numbered acceptance criterion is covered by the presented plan with concrete approach/files/evidence. Reject when any AC is missing, hand-waved, or contradicted.",
      "criteria": {
        "accept": "Every numbered AC is planned; the plan does not contradict the story.",
        "reject": "At least one AC is unplanned, vague, or contradicted."
      }
    },
    "plan_present": {
      "type": "noul",
      "instructions": "Does state.payload.docs include an attachment named plan with a non-empty body?"
    },
    "acs_covered": {
      "type": "noul",
      "instructions": "Does the attached plan claim concrete coverage for every numbered acceptance criterion on the story?"
    }
  },
  "verdict": "verdict",
  "min_confidence": 0.7,
  "on_low_confidence": "reject"
}
```
