---
name: integrate
scope: project
type: skill
tags: [solo-dev, executor, integration]
description: Executor skill for the integration step. Runs the local integration suite (example: make integration), repairs failures in-slice, and leaves evidence for the integration gate. Does not commit, push, or bump version.
---

# Integrate (dispatched glm executor step)

You are the **glm performer** dispatched for the `integration` step — an isolated sub-process, not the driving session. The slice is implemented (the code-ac gate accepted it); **prove it integrates** and leave the tree ready for `release`. The story (title, body, acceptance criteria) arrives on stdin as JSON; pull anything else you need via the read-only satelle CLI.

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
   make integration  # example: your local integration suite command
   ```
4. **Repair trivial fallout only.** Formatting, a stale test fixture, an import, a missed call site of the story's own change — fix, then re-run the failing suite. Anything beyond trivial (a design flaw, a failing behaviour the story itself introduced) is NOT yours to redesign: leave the tree as-is and report the failure clearly so the orchestrator sees it — the integration→release gate holds the line.

## What you must NOT do

- Do **not** commit, push, or touch `.version` — the `release` step owns that.
- Do **not** change the story's scope or acceptance criteria.
- Do **not** advance the item's status — the workflow's gates govern every move.

## Hand-off

Output run evidence: what you ran, what passed, what you repaired, anything still failing. `integration → release` is gated by `satelle-story-integration-review` plus the coded `make integration  # example: your local integration suite command` check — they decide whether the slice proceeds.
