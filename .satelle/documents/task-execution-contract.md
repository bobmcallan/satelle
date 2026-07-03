---
type: document
title: Task execution contract
description: What a task execution declares (ACTION + VERIFICATION), how it is gated, and how its executor may be a named agents.toml agent.
tags:
- document
- task
- execution
timestamp: '2026-07-02T00:00:00Z'
---

# Task execution contract

satelle's executable task is a **two-entity** primitive (sty_ef08ce2a): a stable
**task header** (a work-definition authored under `.satelle/tasks/tsk_*.md`) and
its **executions** — isolated runs, each a separate item parented to the task and
materialized under the task's own folder `.satelle/tasks/<tsk_id>/<exe_id>.md`.
An execution carries the lifecycle; the header does not. "Re-running" a task
means creating a **new execution**, never moving a completed one backward
(`done` is terminal — [[satelle-done-is-last]]).

## What an execution declares

The executor's contract is the run's **body**, and it declares two things:

- **ACTION** — what this run does (create/modify a file, produce a document, run
  a mechanism, reconcile some state).
- **VERIFICATION** — how success is shown: the concrete, checkable evidence that
  the ACTION was carried out (the artifact exists and contains the change, a named
  test/command would pass, a recorded result).

Both must be present before the run begins and satisfied before it closes. A run
whose ACTION is vague or whose success cannot be checked is not a valid run.

## How an execution is gated

An execution resolves — by its **kind** — to the `satelle-task-workflow`
(`applies_to: ["execution"]`), never the wildcard story workflow. Two
reviewer-only gates bracket it:

- **`satelle-task-validate-before-review`** (`backlog → in_progress`): the run is
  a well-formed execution of a **valid task** — the parent task header exists and
  declares an ACTION + VERIFICATION, and the run restates its own.
- **`satelle-task-validate-after-review`** (`in_progress → done`): the ACTION was
  carried out and the VERIFICATION is satisfied, judged from repository evidence.

Reviewers judge; they never enact. A status transition runs the edge's gate
through the shared reviewer dispatch (`transitionGater`), updates the execution's
file frontmatter + the op-log on accept, and blocks on a reject.

## A run's OUTPUT is collected per task (sty_890b86cb)

Beyond the execution's `<exe_id>.md` work-definition, each run's **output** — what
the executor actually did and produced — is collected as an OKF document beside it
under the task's bundle, `.satelle/tasks/<tsk_id>/output-<exe_id>.md` (frontmatter
`type: task-execution-output` + the `generated: satelle` marker). One write seam
serves both run modes:

- **Dispatched runs** (a named agents.toml agent): satelle already holds the
  spawned agent's stdout at the dispatch site and writes it through mechanically —
  nothing for the run to do.
- **In-loop runs** (the driving session performs the step): the orchestrator holds
  the outcome, so it calls **`satelle execution record <exe_id>`** as the run's
  final act (piping the run output on stdin, or `--output`). This is a REQUIRED
  closing step of an in-loop run — the exit gate expects the run's evidence to be
  discoverable per task, not only interleaved in the central `.satelle/logs/executor.log`.

The output doc is per-run (a new run's `<exe_id>` never overwrites a prior run's),
and the central `executor.log` is unchanged (it stays the operational log).

## The executor may be an agents.toml agent

While `in_progress`, the work is done by the **executor**. The executor MAY be a
named agent from `.satelle/agents.toml` — the run declares which agent carries out
its ACTION, resolved through satelle's agent dispatch (the same harness mechanism
that runs reviewers). Naming an agent is optional; an unnamed run uses the default
executor harness. This keeps the run's *who* configurable substrate, not code.

See [[satelle-agent-model]], [[satelle-done-is-last]], [[satelle-constitution]].
