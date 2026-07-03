---
name: integrate
scope: project
type: skill
tags: [type:skill, type:executor]
description: Executor skill for the `integration` step. Verifies the implemented slice integrates — gofmt/vet clean, unit and integration suites green — repairing trivial fallout (formatting, a stale fixture, a missed call site) and leaving the tree ready for the commit step. It does NOT commit, push, or bump the version, and it never advances status; the integration→commit gates judge the outcome.
---

# Integrate (executor step)

You are the **executor** in the `integration` step. The slice is implemented
(the code-ac gate accepted it); your job is to **prove it integrates** and leave
the working tree ready for the `commit` step. The story (title, body,
acceptance criteria) arrives on stdin as JSON.

## What to do

1. **Format and vet.**
   ```bash
   gofmt -l internal cmd tests   # expect no output; run gofmt -s -w on offenders
   go vet ./...
   ```
2. **Unit suite.**
   ```bash
   go test -count=1 ./...
   ```
3. **Integration suite.**
   ```bash
   make integration
   ```
4. **Repair trivial fallout only.** Formatting, a stale test fixture, an import,
   a missed call site of the story's own change — fix, then re-run the failing
   suite. Anything beyond trivial (a design flaw, a failing behaviour the story
   itself introduced) is NOT yours to redesign: leave the tree as-is and report
   the failure clearly in your output so the orchestrator sees it — the
   integration→commit gate will hold the line.

## What you must NOT do

- Do **not** commit, push, or touch `.version` — the `commit` step owns that.
- Do **not** change the story's scope or acceptance criteria.
- Do **not** advance the item's status — the workflow's gates govern every move.

## Hand-off

Your output is run evidence: state what you ran, what passed, what you repaired,
and anything still failing. The `integration → commit` edge is gated by
`satelle-story-integration-review` plus the coded `make integration` check —
they, not you, decide whether the slice proceeds.
