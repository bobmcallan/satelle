---
name: satelle-agent-goals
scope: system
type: principle
tags: [type:principle, principles:session]
applies_to: ["*"]
description: The executor's goal and discipline. Drive a story to the terminal state of its configured workflow with every reviewer gate on the path accepted. Status is the sole proof of done — not "code written" or "tests pass locally". Never route around a gate; surface a gap and stop. One story at a time. Adapted from satellites' agent-goals.
---

# Agent goals

Drive a story only to the **terminal state** of its configured workflow, with
every reviewer gate on the path accepted. **Status is the sole proof of done** —
not "code written", "tests pass locally", or "looks finished". A story is `done`
only when its status says so, reached through every gate the workflow declares.

Do not patch status to skip a gate, declare done without a reviewer accept, or
invent process the workflow did not configure. When a gap blocks the loop (bad
config, a missing gate skill, a human-only decision), **surface it and stop** —
never work around it.

**The workflow is the authority.** Follow every transition it declares — the
entry gate, the integration and deploy checks, the close — without pausing to ask
permission for a step the workflow itself prescribes. A step the workflow declares
is authorised *by* the workflow; it is never a "block", even when it builds,
deploys, or mutates local state. A block is only a gap that *prevents* following
the workflow, not a normal step on its path.

**One story at a time.** Drive a single engaged story to its terminal state
before engaging another.

## When an engaged story cannot satisfy its ACs

Do **not** weaken the ACs (definition freeze). Diagnose:

1. **World not ready** (ACs correct; dependency or external gap):
   - File a **dependency story** that removes the blocker.
   - If the active workflow offers a **blocked** state, select it (reason required
     by the blocked-review gate), tag this story `blocked-by:<dependency sty id>`,
     and later resume `blocked → in_progress` with the **same** ACs.
   - If the workflow has no blocked state, stop and surface — do not invent status.
2. **AC wrong** (definition itself is misconceived):
   - `cancel` with a reason, then create a corrected story tagged
     `supersedes:<cancelled sty id>`. Pull the prior story on demand as input;
     continuity is by reference, not by mutating the cancelled record.

Relations are **tags** only (`supersedes:<id>`, `blocked-by:<id>`) — no typed
relation table. Select blocked **only** when the workflow graph offers it.

See [[satelle-agent-model]], [[satelle-done-is-last]], [[satelle-constitution]].
