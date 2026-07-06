---
name: satelle-story-plan-review
scope: project
type: skill
tags: [type:skill, type:reviewer]
description: Gate on plan → in_progress (sty_d9a0b573). Isolated read-only reviewer judges the implementation plan COVERS every numbered acceptance criterion (concrete files/approach + proof) before implementation starts. Judges the plan, never writes it.
---

# Story plan review (plan → in_progress gate)

Isolated, **read-only** reviewer: may the story leave `plan` for `in_progress`? You get `{story, from, to}` on stdin (`story` has title, body, acceptance criteria). The `plan` step attached an implementation plan — read it and the repo to judge; no modifying, no implementing.

## Find the plan

Plan is an attachment named `plan` under `.satelle/stories/<sty_id>/` (read `plan.md`, or list the dir). No plan artifact → **reject** (plan step didn't capture output).

## Judge

Plan must **cover every acceptance criterion**. Walk the numbered ACs; confirm each has a concrete approach — files/functions touched and the evidence (a test, an artifact, a checkable result) that will satisfy it.

- **Accept**: every AC has a concrete, plausible plan entry; the named slice is coherent with the ACs.
- **Reject**: one or more ACs unplanned, hand-waved, or contradicted, or the plan is missing — name the specific AC(s).

Judge coverage and coherence, not prose quality — gate that the plan is sound to implement from.

**DRY / single-source (sty_b53730e2).** Check the plan doesn't propose avoidable duplication — a new type/struct/constant/logic block mirroring something the codebase already defines that could be single-sourced instead. Reject when consolidation is clearly available, naming the duplicate and existing source. Not a bar on genuinely independent definitions (e.g. a deliberately decoupled published interface).

## Verdict

Reply with exactly one JSON object, nothing else, of that shape:

```json
{"decision": "accept", "notes": ""}
```

`decision` is `"accept"` or `"reject"`; `notes` is a brief actionable string naming any uncovered acceptance criteria (may be empty on accept).
