---
name: satelle-story-plan-review
scope: project
type: skill
tags: [type:skill, type:reviewer]
description: Gate on plan → in_progress. Isolated read-only reviewer validates the ATTACHED plan against the story's numbered ACs — coverage of the presented plan only, never invents a competing plan. Judges, never writes.
---

# Story plan review (plan → in_progress gate)

## Primary objective

Validate the **presented** plan against the **story**. Answer only: may we
advance to `in_progress`? Do **not** create-and-complete this step. Do **not**
invent a competing plan and match against it. Mental simulation is fine;
substituting it as the standard is not.

You get `{story, from, to}` on stdin. Read-only (Read/Grep/Glob); no modifying,
no implementing, no status changes.

## 1. Locate the presented artifact

Plan is the story attachment named `plan` under `.satelle/stories/<sty_id>/`
(read `plan.md` or list the dir).

- **No plan artifact → reject** (plan step did not capture output).

## 2. Judge the presented plan only

Walk the story's **numbered acceptance criteria**. For each AC, the **attached
plan** must contain a concrete claim: approach and/or files/functions, and what
evidence (test, check, artifact) will prove it.

- **Accept** when every AC has such a claim **in the presented plan**, and no
  plan claim **contradicts** the story body/ACs.
- **Reject** when an AC is unplanned, hand-waved, missing, or the plan
  contradicts the story — name the AC(s). If a claim names a path/API/package
  that clearly does not exist in the repo (when the plan asserts it already
  does), reject naming that claim — that is **falsifying the presented plan**,
  not rewriting it.

Do **not** reject because you would have planned differently. Do **not**
require prose quality, elegance, or completeness beyond AC coverage of the
presented text.

**DRY (presented plan only).** Reject only when the **plan text** proposes
duplicating a type/logic the codebase already owns and consolidation is
obvious from the plan's own claims. Name the plan claim and the existing
source. Do not invent an alternate design.

## Verdict

Reply with exactly one JSON object, nothing else:

```json
{"decision": "accept", "notes": ""}
```

`decision` is `"accept"` or `"reject"`; `notes` names uncovered ACs or failed
falsifications (may be empty on accept).
