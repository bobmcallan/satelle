---
name: satelle-recognise-blockage
scope: system
type: principle
tags: [type:principle, principles:session]
applies_to: ["*"]
description: Recognise process blockage and park — never thrash a denied tool call or ask to remove enforcement. When gates, missing deps, or contradictory instructions stop progress, stop retrying, move the engaged story to blocked with a structured reason, and let the blocked-triage path diagnose. Close the final engaged story last, after residual git work.
---

# Recognise blockage

When work **cannot advance by the process**, treat that as **blockage** — not as
a licence to thrash, escalate to "remove the hook", or route around enforcement.
Park, reason, resume.

## Recognition signals

Any of these is enough:

1. **Repeated deny** — the same tool call (or the same fused command) is denied
   again by a gate or hook with no new information.
2. **Unmeetable precondition** — a required state the agent cannot satisfy from
   *here* (nothing engaged; wrong story engaged; artifact absent).
3. **Missing dependency** — an edge, story, skill, or external input the step
   needs is not available.
4. **Contradictory instruction** — substrate, task, and live state disagree so
   no legal next step exists.

## Prescribed move

1. **Stop** retrying the denied call unchanged.
2. **Park** the engaged story: `in_progress → blocked` with a **structured
   reason** (what was attempted, what denied it, what was tried) — body note,
   ledger, and/or a hold-reason attachment. The blocked-review gate requires a
   reason on record.
3. **Triage** — entering **blocked** is what engages the triage path
   ([[satelle-story-blocked-triage]]). That skill diagnoses, records reasoning
   on the story, and actions an in-process unblock **within** satelle's gates.
   Resume via the workflow's declared `blocked → in_progress` edge (same ACs).

If the workflow has no blocked state, surface and stop — do not invent status.

## End-of-session ordering (vehicle closes last)

The **final engaged story** (the session vehicle) closes **only after** residual
git activity: lessons attach, close record, final commit, push. Once every story
is terminal, engagement is zero — any Bash whose command pattern-matches
`git commit` / `git push` is a guaranteed commitgate deny with **no** in-process
escape. Sequence tidy-up **before** the last `done` transition, never after.

## Anti-patterns

- Retrying a denied call **unchanged**
- Asking the operator to **remove or disable** a hook or gate
- **Routing around** enforcement (edit via shell, `git commit --no-verify`)
- Silently abandoning the story
- Closing the last engaged story while residual commit/push work remains

## Motivation (not normative)

Session traces of solvable process blocks (fused engage+commit deny; edit-gate
with nothing engaged; post-close commit with zero engagement) are the cases this
principle names — see `.satelle/documents/session-trace-workflow-review-followups.md`
and related traces. They illustrate; they do not define process.

See [[satelle-agent-goals]], [[satelle-edits-require-a-story]],
[[satelle-story-blocked-triage]], [[satelle-agent-model]].
