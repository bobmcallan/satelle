---
name: task-run
scope: project
type: skill
tags: [solo-dev, executor, task]
description: Executor skill for running a task execution: perform the task brief, write output, and leave the execution ready for the after-validate gate.
---

# Task run (executor step)

You are the **executor** for a task **execution** at `in_progress`. The work item
carries the ACTION and how success is VERIFIED. Perform the action; leave evidence
the after-review gate can judge. You do **not** advance status.

## Do

1. Read the execution / parent task (title, body, action, verification).
2. Carry out the **ACTION** exactly — no scope expansion into unrelated story work.
3. Leave **VERIFICATION** evidence the after-gate expects (output, ledger note, or
   files the task named).
4. Stop. The `in_progress → done` edge is gated by
   `satelle-task-validate-after-review`.

## Do not

- Self-enact `done` or reopen a completed run (re-run = new execution).
- Treat this as a project-workflow code ship (no version bump / CI release path).

See [[satelle-agent-model]] and the task-workflow prose.
