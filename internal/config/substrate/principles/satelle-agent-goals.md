---
name: satelle-agent-goals
type: principle
tags: [type:principle, principles:session]
applies_to: ["*"]
description: Drive a story to the terminal state of its configured workflow with every gate accepted. Status is the sole proof of done. Never route around a gate; surface a structural gap and stop. One story at a time unless the container declares a schedule; then the wave says who may start.
---

# Agent goals

Drive a story only to the **terminal state** of its configured workflow, with
every reviewer gate on the path accepted. **Status is the sole proof of done** —
not "code written" or "tests pass locally". A story is `done` only when its
status says so, reached through every gate the workflow declares.

Do not patch status to skip a gate, declare done without a reviewer accept, or
invent process the workflow did not configure. When a **structural** gap blocks
the loop (bad config, a missing gate skill), **surface it and stop** — never work
around it; a practice is no gap: [[satelle-review-structure-not-process]].

**The workflow is the authority.** Follow every transition it declares — the
entry gate, the integration and deploy checks, the close — without pausing to ask
permission. A declared step is authorised *by* the workflow, even when it
builds, deploys, or mutates local state; a block is a gap only if it *prevents*
following the workflow.

**This session is the orchestrator when both seats are in-loop.** When the
executor seat and the orchestrator seat both resolve to the in-loop command,
they are the same agent. The message `complete sty_<id>` starts that story's drive;
`drive epic <id> to done` starts the drive under *Driving an epic*.

**A named performer hands the drive back here.** The step's agent does the step
and does not change status. When it returns, this session runs the next
`satelle story set` the workflow names. An edit refusal on a step allocated to
that performer fences source edits. The drive stays in this session.

**One story at a time — when the container declares no schedule.** Drive a
single engaged story to its terminal state before engaging another. This is not
the rule once a container declares a schedule; see *Driving an epic*.

**Epics close on their children.** An epic is complete only when every child
story is terminal ([[satelle-story-classification]] defines the children).
Intermediate stages are waypoints, not hand-back points.

## Driving an epic

**Drive epic `<id>` to ready** walks only the container to ready. It does not
engage children.

**Drive epic `<id>` to done** requires the container at its waiting step.
`satelle story wave <id>` returns the set that may be engaged. Drive each child
on its own category workflow to its own terminal state. Close the container only
after the wave is empty because every child is terminal.

When `satelle story wave` exits non-zero, that is a **stop**: surface its reason.
A zero exit naming children is not — drive them. Do not pick a child by `order:`,
by the sprint, or by title; `satelle story wave` alone says who may start.

You open the worktrees. Cut an independent child from the epic base, the branch
the container was engaged on. Cut a child whose `depends-on` target is done and
already on trunk from trunk (the epic still merges once); so too a done target
with no branch of its own — never cut from a branch that does not exist. Cut a
child whose target is done but not yet on trunk from that target's branch, not
from main, so the dependent tree holds the change it was written against. A
cancelled dependency is a stop: cut no worktree from it, do not retarget the edge.

## When an engaged story cannot satisfy its ACs

Do **not** weaken the ACs (definition freeze). Diagnose:

1. **World not ready** (ACs correct; dependency or external gap): file a
 **dependency story** that removes the blocker. If the workflow offers a
 **blocked** state, select it (reason required by the blocked-review gate), tag
 this story `blocked-by:<dependency sty id>`, and later resume
 `blocked → in_progress` with the **same** ACs. With no blocked state, stop and
 surface — do not invent status.
2. **AC wrong** (definition misconceived): `cancel` with a reason, then create a
 corrected story tagged `supersedes:<cancelled sty id>`. Pull the prior story on
 demand; continuity is by reference, not by mutating the cancelled record.

Relations are **tags** only. Select blocked **only** when the workflow graph
offers it.

See [[satelle-agent-model]], [[satelle-done-is-last]], [[satelle-constitution]].
