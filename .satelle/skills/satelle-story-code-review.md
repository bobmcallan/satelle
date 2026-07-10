---
name: satelle-story-code-review
scope: project
type: skill
tags: [type:skill, type:reviewer]
description: LEGACY / unused by satelle-project-workflow (in_progress → integration uses satelle-code-ac-review). If invoked, same contract as code-ac — presented code+tests vs ACs only. Prefer satelle-code-ac-review.
---

# Story code review (legacy alias of code-ac)

## Status

**Not referenced** by `.satelle/workflows/satelle-project-workflow.md`. The live
edge `in_progress → integration` uses **`satelle-code-ac-review`**. Keep this
file only so old references resolve; do not wire new edges here.

## Primary objective

If this skill is still invoked: validate **presented** working-tree code and
tests against the **story** ACs only. Same bar as `satelle-code-ac-review`.
Do not tech-lead redesign. Do not implement.

## Judge

1. Each numbered AC is met by visible code (not stubbed).
2. Behavioural changes have unit + integration test coverage (substrate/docs
   exempt).
3. Accept/reject with AC-named gaps only.

## Verdict

```json
{"decision": "accept", "notes": ""}
```
