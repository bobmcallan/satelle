---
type: document
title: Story mutability and the blocked lifecycle
description: The resolved design for when a story's definition may change, how an engaged story that hits an unachievable AC is parked (blocked) or replaced (cancel + recreate), and how those relations are carried as tags. The spec the freeze-guard and blocked-lifecycle stories build against.
tags:
- document
- workflow
- design
timestamp: '2026-07-04T00:00:00Z'
---

# Story mutability and the blocked lifecycle

The problem: once a story leaves `backlog`, its plan and every gate verdict were
formed against the story **as it was then**. If the definition changes mid-flight,
that work is silently invalidated — and an agent that edits its own acceptance
criteria to make a gate pass has routed **around** the gate, the one thing
[[satelle-agent-goals]] forbids. This document fixes the rule and the escapes.

Adjacent, out of scope: cross-repo filesystem isolation is a **harness** concern,
not satelle's. Using the satelle CLI from another repo's dir to raise stories or
attach documents there is accepted — that controls *story context*, not product
code, and is not an amendment in the sense below.

## The invariant: the definition is immutable once engaged

A story's **definition** — `title`, `body`, `acceptance_criteria`, `category` —
may change **only while the story is in its workflow's entry state** (conventionally
`backlog`). Once engaged, it is frozen.

- **Do not hardcode `"backlog"`.** Workflows are customisable; the mutable state is
  the workflow's **entry node** (the `Mdiamond`, the state a freshly created story
  starts in), whatever it is named. Derive it from the active workflow the same way
  `storyEngaged()` derives its executor states from the DOT — never a Go literal.
- **What still flows post-entry:** `status`, `estimate`, `actual`, `tags`,
  `priority`, and **attachments/documents**. Adding context to a story is not
  amending its definition. Only the four definition fields freeze.
- **Why this is the whole anti-gaming story:** because ACs can't be weakened once
  engaged, no one can move the goalposts to make a reviewer pass. The gates always
  judge the definition the story started with.

## When an engaged story can't be satisfied

The agent still does what it does today — it stops and surfaces — but now it has
**states and relations to enact the outcome** instead of only halting. The agent
diagnoses one question: *is the world not ready, or is the AC wrong?*

### World not ready → `blocked` (+ a dependency story)

The ACs are **correct**; the repo just can't satisfy them yet (a missing
capability, an upstream bug). The agent:

1. Files a **dependency story** that removes the blocker.
2. Selects **`blocked`** on the original, recording a **reason** (mirrors
   `cancel` — a `satelle-story-blocked-review` gate that accepts when a reason is
   on record), and tags it `blocked-by:<dependency sty id>`.
3. On a later turn, once the dependency is `done`, the agent selects
   `blocked -> in_progress` and resumes — **with the same ACs**. Resume is
   **agent-directed**; the `blocked-by` tag is the cue, there is no auto-nudge.

`blocked` is authored substrate — a state + `in_progress <-> blocked` edges in the
workflow DOT — **not** a hardcoded concept. A repo may remove those edges; the
binary must never assume `blocked` exists. Model the node so it is **not** an
`agent=executor` state, so a blocked story does not count as engaged (the edit gate
correctly blocks code edits while work is parked).

### AC wrong → `cancel` + recreate (referencing the old story)

The AC itself is misconceived or unachievable as written, so the story is invalid.
The agent `cancel`s it (cancel-review requires a recorded reason) and creates a
**corrected** story tagged `supersedes:<cancelled sty id>`. Continuity is preserved
**by reference, not by mutation**: the cancelled story stays an immutable audit
record; the agent pulls it on demand (`satelle story get <id>`, its attachments,
its ledger) as **input** to the new one, exactly as it pulls a plan or a summary.

### Neither fits

If the active workflow offers no `blocked` state and the block is not an AC error,
fall back to today's behaviour: stop and surface to the operator.

## Relations are tags, not a new mechanism

`supersedes:<id>` and `blocked-by:<id>` are ordinary **tags** — already in the
payload isolated agents receive, already rendered in the UI, already the convention
(`estimate-minutes:`, `workflow:`). No typed-relation table, no schema, no create
flags. The forward tag on the new/blocked story is enough; the reverse
(`superseded_by`, `blocking`) is a query, not a stored field.

- **Validation is intentionally light** — a bad id surfaces when the agent tries to
  pull it, not as a gate. The reference is guidance, not enforcement.
- **UI, for free:** render `sty_`/`tsk_`-valued tag chips as **links**, and
  `supersedes`, `blocked-by`, and existing parent references all become navigable
  with one change.

## What this decomposes into

- **Definition freeze** — the only Go change: a `satelle story set` guard that
  refuses edits to the four definition fields unless the story is in the workflow's
  entry state (derived, not hardcoded).
- **`blocked` lifecycle** — pure substrate: the `blocked` node + edges in the
  embedded default workflows, the `satelle-story-blocked-review` reason gate, and
  the agent decision-tree guidance in [[satelle-agent-goals]], plus the
  `supersedes:` / `blocked-by:` tag convention.
- **Clickable id-tag chips** — optional web-UI polish serving all id-valued tags.

See [[satelle-agent-goals]], [[satelle-agent-model]], [[satelle-constitution]].
