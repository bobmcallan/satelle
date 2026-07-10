---
name: satelle-edits-require-a-story
scope: system
type: principle
tags: [type:principle, principles:session]
applies_to: ["*"]
description: Every edit to the tree requires a story engaged in a performing state, and the work follows that story's governing workflow. Research uses read tools, never Edit/Write. The edit gate is not optional and must never be routed around, disabled, or proceeded past — a gate that does not fire is a defect to surface, not a licence to edit ungated.
---

# Edits require a story

**No tree edit without an engaged story.** Before editing, creating, or deleting
any file, a story (or task) must be engaged in a *performing* — non-terminal
engaging — state of its governing workflow. Open one first: `satelle story
create …`, then `satelle story set <id> --status plan`. That session stays open
through the edits until the story reaches a terminal or parked state (done,
cancelled, blocked); finishing an edit does not close it.

**Research reads; it does not write.** To investigate, use read tools
(Read/grep/Glob) — never Edit/Write to "try" something out.

**The gate is not optional.** The edit gate enforces this rule. Never route
around it, disable it, or proceed when it does not fire: a silently inert gate is
a defect to surface and fix, not permission to edit ungated. Commits and pushes
are gated the same way — commit under the engaged story.

**Follow the workflow.** Drive the engaged story through every transition its
workflow declares; a step the workflow prescribes is authorised by it, never a
reason to pause.

See [[satelle-agent-goals]], [[satelle-agent-model]], [[satelle-constitution]].
