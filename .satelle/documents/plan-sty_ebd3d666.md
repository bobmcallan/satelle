---
story: sty_ebd3d666
type: plan
name: plan
---

# Plan — refuse skipped workflow steps (sty_ebd3d666)

## Shape

One fence on the existing status-set path: before engage/lease/gate, refuse a
status change that is not a declared DOT edge of the governing workflow. Name
the expected successor(s). No new gate skill, no reminder-only path.

## Files

- `internal/wfdot/wfdot.go` — `HasEdge(from,to)`, `Successors(from)`
- `internal/verb/transition_edge.go` — `refuseSkippedStep` (stories + tasks)
- `internal/verb/workitem.go` — call fence when `transitioning`
- tests: plan blow-through regression; park/recovery still pass

## ACs

1. Skip required step → refuse naming expected next (e.g. backlog→in_progress when only plan).
2. Refuse via verb error (agent-visible; same surface as other story-set denies).
3. Declared recovery/park/cancel edges pass.
4. Regression test: plan blow-through refuses.

## Fail-open

No governing workflow / no DOT → allow (structure owns those). Mechanism only.
