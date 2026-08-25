---
name: satelle-recognise-blockage
type: principle
tags: [type:principle]
applies_to: ["*"]
description: When a gate, a missing dependency, or a contradictory instruction stops progress: stop retrying, park the engaged story as blocked with a structured reason, and let the blocked-triage path diagnose it.
---

# Recognise blockage

When work **cannot advance by the process**, treat that as **blockage** — not as
a licence to thrash, escalate to "remove the hook", or bypass enforcement
(gate-routing discipline: [[satelle-agent-goals]]).
Park, reason, resume.

## Engaging a story is not blockage

**Needing to engage a story is NOT blockage** — it is the workflow **entry step**.
When no story is engaged and the edit or commit gate denies a write, **engage and
proceed** (open or select a story, drive it into a performing state per the
governing workflow). See [[satelle-edits-require-a-story]].

**Blockage** is a gap that persists **even with a story engaged**: a deny that
stays denied after correct engagement, a missing dependency, or contradictory
instructions with no legal transition.

## Preemption is not blockage either

Higher-priority work needing the engagement seat is **preemption**, not
blockage: the held story is healthy, nothing is impeding it. The path is
`satelle story stop-request <holder> --reason "…"` — the holder is refused
forward moves, parks itself (`blocked`) with the reason on record and a
`preempted-by:<id>` tag, and resumes later on the same ACs.

**Never cancel a healthy story to free the seat.** `cancelled` is terminal;
revival is a NEW story tagged `supersedes:<id>`, so a seat-motivated cancel
destroys the record permanently. No declared `cancel-reason` value means
"preempted".

## Recognition signals

Any of these is enough:

1. **Repeated deny** — the same tool call (or the same fused command) is denied
 again by a gate or hook with no new information.
2. **Unmeetable precondition** — a required state the agent cannot satisfy from
 *here* while a story is already engaged (wrong story engaged; artifact
 absent; a gate precondition that engagement alone cannot meet).
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
- Treating a missing engagement as blockage instead of engaging a story
- Cancelling a healthy story to free the engagement seat

See [[satelle-agent-goals]], [[satelle-edits-require-a-story]],
[[satelle-story-blocked-triage]], [[satelle-agent-model]].
