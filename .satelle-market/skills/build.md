---
name: build
scope: project
type: skill
tags: [solo-dev, executor, in_progress, implementation]
description: Executor skill for the in_progress (implementation) step. Implements the story from the stdin work item (title, body, ACs, plan when present) and creates the unit and integration tests the code-ac gate expects. Does not commit, push, bump version, or advance status.
---

# Build (executor step)

You are the **executor** in the `in_progress` (implementation) step: **implement the story**, leave the tree ready for `integration`. The work item (title, body, acceptance criteria) arrives on stdin as JSON — your ENTIRE brief. You don't see the authoring conversation; if the body + acceptance criteria don't define the work well enough, don't guess — report what's missing and stop, tree untouched. Orchestrator and gates handle the return.

## What to do

1. **Read the brief.** Parse stdin JSON: `title`, `body`, `acceptance_criteria` (numbered, each a testable obligation), and the plan if present. ACs are the definition of done — implement all of them, nothing beyond.
2. **Orient before writing.** Read the files the story names and their neighbours; match existing structure, naming, idiom. Process/opinions belong in substrate (`.satelle/`), mechanism in Go — baking a repo-specific decision into a Go branch is a defect even if it "works" (see `satelle-constitution` (repo principle/doc), `satelle-repo-agnostic` (repo principle/doc)).
3. **Implement the slice.** Smallest change satisfying every AC. No scope widening, no opportunistic refactors, no dead code — a library function with zero production callers is rejected (see `satelle-yagni` (repo principle/doc), `satelle-broken-windows` (repo principle/doc), `satelle-agile-increments` (repo principle/doc)).
4. **Create the tests the gate expects.** `in_progress → integration` is gated by `satelle-code-ac-review`: ACs met AND **unit and integration tests created** for changed behaviour. Add/extend `_test.go` beside the change plus integration coverage under `tests/`; a docs/substrate-only story satisfies this via its doc-based ACs instead.
5. **Prove it locally.**
   ```bash
   gofmt -l internal cmd tests   # expect no output; gofmt -s -w on offenders
   go vet ./...
   go test -count=1 ./...
   make integration  # example: your local integration suite command
   ```
   Fix what your change broke. A pre-existing failure you didn't cause isn't yours — report it distinctly.

## What you must NOT do

- Do **not** commit, push, or touch `.version` — `commit`/`push` steps own those.
- Do **not** change status, scope, title, or acceptance criteria — gates govern advances.
- Do **not** edit generated read-only views (home-keyed story attachments, generated `index.md`/`log.md`) — mutate via `satelle`, never the view (see `satelle-generated-readonly` (repo principle/doc)).

## Hand-off

Output the implementation evidence: per AC, what changed and how it's tested; what you ran and what passed; anything incomplete and exactly why. `in_progress → integration` is gated by `satelle-code-ac-review` — it decides whether the slice proceeds.

Skill and reviewer filenames follow `satelle-skill-naming` (repo principle/doc).
Operate only under an enabled `.satelle/` root (`satelle-enable-then-operate` (repo principle/doc)).
