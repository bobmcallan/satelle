# Validating a workflow before implementing — split by cost

Story `sty_09ef53d6` (the re-scoped goal of the cancelled `sty_318708ed`). A story
must never be engaged into a workflow that cannot complete — e.g. one whose later
executor step names a skill that does not resolve (the `commit-push` wasted-work
trap the `sty_af798983` assessment recorded). The work is split by what actually
needs model judgement, so a routine `story set in_progress` never waits on an LLM.

## At create/update — the LLM reviewer (thorough, infrequent)

`satelle-workflow-review` (an isolated `claude -p` reviewer, granted scoped
`satelle` CLI access by `sty_e15c15a4`) judges a workflow's full **structure AND
actionability**: requirement 3a of its rubric has it resolve every referenced
skill — each `reviewer_skill` edge and each `@skill:` node prompt — via
`satelle doc get skills <name>` (so embedded defaults count as resolved), and
reject naming any that do not. This runs when the workflow is **authored or
edited**, where a slow, reasoning review is appropriate and only happens when the
workflow actually changes.

## At engagement — a deterministic guard (instant, every time)

On the engagement edge (leaving the workflow's start state for a non-cancel
target), the gater runs `guardEngagementExecutorSkills`: a fast, in-process,
**agent-free** check that every **executor-step** skill on the path to done
resolves in the substrate, rejecting up front and naming any that do not. This is
the exact wasted-work trap — an executor step whose rubric is missing cannot be
performed — and it adds no latency, so engaging a story stays instant.

Scope is deliberately **executor steps only**:

- A missing **executor** skill leaves a step unperformable → hard block.
- A missing **reviewer** gate degrades to advisory by design (keeps fresh repos
  working), so it is not required here; the create/update LLM review is where
  reviewer-gate actionability is judged.
- Skills referenced only on the `cancelled` exit are off the path to done and are
  excluded — a missing cancel reviewer never blocks engagement.

## Why split this way

Running the full LLM review on every engagement was measured to push the local
integration suite from ~105s to ~470s and time out a browser test — an LLM call on
a routine, frequent operation. Splitting it keeps the thorough review where change
happens (create/update) and a zero-latency safety net where the story commits to
the path (engagement). It reuses the shared `wfdot` parser (`Spec.Start()`,
`Spec.ExecutorPathToDoneSkills()`) and the gater's existing substrate lookup — no
workflow parsing or skill resolution is re-implemented.
