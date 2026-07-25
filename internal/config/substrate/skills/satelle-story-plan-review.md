---
name: satelle-story-plan-review
scope: system
type: skill
tags: [type:skill, type:reviewer]
description: Gate on plan → in_progress (when a workflow has a plan step). Isolated read-only reviewer validates the ATTACHED plan against the story's numbered ACs — presented plan only, never invents a competing plan.
---

# Story plan review (plan → in_progress gate)

## Primary objective

Validate the **presented** plan against the **story**. Answer only: may we
advance to `in_progress`? Do **not** create-and-complete this step. Do **not**
invent a competing plan and match against it.

You get `{story, from, to}` on stdin. Read-only; no modifying, no implementing.

## 1. Locate the presented artifact

Plan is the story attachment named `plan` in the stdin payload's **`docs`**
array (`name`/`type`/`body`). Prefer that — it is how a Bash-less reviewer
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

## Verdict

```json
{"decision": "accept", "notes": ""}
```
