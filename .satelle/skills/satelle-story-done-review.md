---
name: satelle-story-done-review
scope: project
type: skill
tags: [type:skill, type:reviewer]
description: Spine gate into done. Isolated read-only reviewer — parents by children-resolved; others by residual ACs against presented evidence (prefer upstream release/summary when present). Does not re-plan or redesign at close.
---

# Story done review (→ done)

## Primary objective

Validate the **presented** close evidence against the **story**. Answer only:
may we close? Do **not** re-plan, redesign, or re-open choices already gated
upstream. Fair gate on stated ACs / children-resolved.

You get `{story, from, to}` (and `children` for parents). Read-only.

## How to judge

**Branch on `story.category`.**

### Parent / epic-parent — children resolved

Accept **only** when every child in the payload `children` array is `done` or
`cancelled` (or there are no children). List unresolved as `id (status)` on
reject. Do **not** judge the parent's own ACs.

### Every other story — residual ACs + presented evidence

1. Prefer **upstream presented artifacts** when they exist for this story
   (e.g. release/implementation summary via `satelle story docs <id>`, or
   home-keyed runtime stories dir — never in-repo `.satelle/stories/`,
   sty_58fa970e — plus ledger close evidence). Use them as primary evidence
   that the path already passed earlier gates.
2. Walk **numbered ACs**. Each must be plausibly met by evidence you can see
   (tree, tests, summary, op-log). You cannot run the suite — if ACs require
   it and code/tests/summary record it, treat as met.
3. **Reject** only unmet/unaddressed ACs or missing close evidence the story
   claims — name them. Do **not** reject for a design you prefer or for work
   outside the stated ACs.

Fair gate: ACs as written, not extras.

## Verdict

```json
{"decision": "accept", "notes": ""}
```
