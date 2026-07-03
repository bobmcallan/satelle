---
name: build
scope: project
type: skill
tags: [type:skill, type:executor]
description: Executor skill for the `in_progress` (implementation) step when dispatched to an isolated builder (agent=builder). Implements the story strictly from the stdin work item — title, body, acceptance criteria, and the plan when the story carries one — creating the unit and integration tests the code-ac gate expects. It does NOT commit, push, bump .version, or advance status; the in_progress→integration edge (satelle-code-ac-review) judges the outcome. Until the staged flip lands, the in-loop orchestrator may use this rubric as its own implementation checklist.
---

# Build (executor step)

You are the **executor** in the `in_progress` (implementation) step. Your job is
to **implement the story** and leave the working tree ready for the
`integration` step. The work item (title, body, acceptance criteria) arrives on
stdin as JSON — it is your ENTIRE brief. You do not see the conversation that
authored it; if the item's body + acceptance criteria do not define the work
well enough to implement, do NOT guess — report precisely what is missing in
your output and stop, leaving the tree untouched. The orchestrator and the
gates handle the return.

## What to do

1. **Read the brief.** Parse the stdin JSON: `title`, `body`,
   `acceptance_criteria` (numbered — each is a testable obligation), and the
   plan when the body carries one. The acceptance criteria are the definition
   of done for this step; implement to them, all of them, and nothing beyond
   them.
2. **Orient in the repo before writing.** Read the files the story names and
   their neighbours; match the existing structure, naming, and idiom. This
   repo's constitution applies: process and opinions belong in the substrate
   (`.satelle/`), mechanism in Go — a change that bakes a repo-specific
   decision into a Go branch is a defect even when it "works"
   (see [[satelle-constitution]], [[satelle-repo-agnostic]]).
3. **Implement the slice.** The smallest change that satisfies every acceptance
   criterion. Do not widen scope, refactor opportunistically, or leave dead
   code — a library function with zero production callers will be rejected
   (see [[satelle-yagni]]).
4. **Create the tests the gate expects.** The `in_progress → integration` edge
   is gated by `satelle-code-ac-review`, which requires acceptance criteria
   met AND **unit and integration tests created** for behaviour the story
   changes. Add/extend `_test.go` beside the change and integration coverage
   under `tests/` as the change warrants; a docs/substrate-only story
   satisfies this with its doc-based acceptance criteria instead.
5. **Prove it locally.**
   ```bash
   gofmt -l internal cmd tests   # expect no output; gofmt -s -w on offenders
   go vet ./...
   go test -count=1 ./...
   make integration
   ```
   Fix what your change broke. A pre-existing failure you did not cause is not
   yours to absorb — report it distinctly in your output.

## What you must NOT do

- Do **not** commit, push, or touch `.version` — the `commit` and `push` steps
  own those.
- Do **not** change the item's status, scope, title, or acceptance criteria —
  the workflow's gates govern every advance.
- Do **not** edit generated read-only views (`.satelle/stories/`, generated
  `index.md`/`log.md`) — mutate records via `satelle`, never the view
  (see [[satelle-generated-readonly]]).

## Hand-off

Your output is the implementation evidence: per acceptance criterion, what you
changed (files) and how it is tested; what you ran and what passed; anything
you could not complete and exactly why. The `in_progress → integration` edge is
gated by `satelle-code-ac-review` — it, not you, decides whether the slice
proceeds.
