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

You get `{story, from, to}` on stdin. Read-only.

## Accept when

1. The **title** names a concrete change.
2. The **body** states a clear goal / what done looks like.
3. **acceptance_criteria** lists at least one numbered, testable item.

Whole bar. Do not demand a design, estimates, tags, or a particular style.

## Reject when

Intent is unclear: no goal, or ACs are missing or untestable ("make it
nicer"). On reject, name the failed check(s) only.

## Verdict

```json
{"decision": "accept", "notes": ""}
```
