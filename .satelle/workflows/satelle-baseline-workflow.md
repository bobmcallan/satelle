---
name: satelle-baseline-workflow
scope: system
kind: workflow
tags: [kind:workflow]
applies_to: ["*"]
description: REPO OVERRIDE of the embedded canonical satelle-baseline-workflow (config/substrate/workflows). This repo's own, currently-gateless variant — open to in_progress to done, with a blocked detour and a cancelled exit — driven advisorily with `satelle story set --status` until the quality-management spine (the isolated reviewer) lands and gating is wired in. It shadows, but never edits, the binary's canonical gated default.
---

# Baseline workflow (order-zero) — repo override

> **This file is a repo override**, not the canonical source. The binary ships an
> embedded canonical `satelle-baseline-workflow` (gated `backlog → in_progress →
> done`) under `config/substrate/workflows`; because this file shares its name it
> shadows that default for this repo only. See the `satelle-repo-agnostic`
> principle. It stays gateless until the reviewer engine (qms-spine) is wired in.

The default lifecycle every satelle repo gets: a story or task moves
**open → in_progress → done**, may detour through **blocked**, and may exit early
to **cancelled**. This repo now runs **gated** (the quality-management spine is
built): the begin-work and close edges are judged by an isolated reviewer
(`satelle-intent-plan-review` on `open → in_progress`, `satelle-story-done-review`
on `in_progress → done`, both repo skills under `.satelle/skills`). The executor
cannot self-enact a gated edge — a reject pushes back with notes. Ungated edges
(blocked, cancelled) stay advisory.

## Workflow

- **open → in_progress** — begin work; **gated** by `satelle-intent-plan-review`
  (the story must be well-formed before work starts).
- **in_progress → blocked** — record that work is stalled on a dependency.
- **blocked → in_progress** — resume once unblocked.
- **in_progress → done** — close the item; **gated** by `satelle-story-done-review`.
  Before requesting the close: run the integration tests (`make integration`) and
  commit/push once they pass — the reviewer then judges the acceptance criteria.
- **open/in_progress → cancelled** — abandon the item (record why).

The closing path is deliberate: `in_progress → (make integration passes) →
commit/push → request done → (reviewer accepts) → done`.

```yaml
states:
  - open
  - {name: in_progress, actor: executor}
  - blocked
  - done
  - cancelled
transitions:
  - {from: open, to: in_progress, reviewer_skill: "satelle-intent-plan-review"}
  - {from: in_progress, to: blocked}
  - {from: blocked, to: in_progress}
  - {from: in_progress, to: done, reviewer_skill: "satelle-story-done-review"}
  - {from: open, to: cancelled}
  - {from: in_progress, to: cancelled}
```

## Environment

Drives a work item open → in_progress → done. Transitions are advisory in the
OSS tier — there is no gate that blocks a move — so the guardrails below are
discipline, not enforcement.

```yaml
guardrails:
  always:
    - Drive an engaged item to a terminal state (done or cancelled) — don't leave work open indefinitely.
    - Give a story/task numbered acceptance criteria before starting, and satisfy them before moving to done.
    - When work stalls, set status to blocked with a note on what it's waiting on, rather than leaving it silently in_progress.
    - After moving an item to in_progress, run `make integration` (the local CLI + browser e2e suite) before closing it.
    - Commit and push only once the integration tests pass; then set the item to done.
  ask_first: []
  never:
    - Move an item straight from open to done — pass through in_progress so the work is visible.
    - Mark an item done with unmet acceptance criteria.
    - Commit or push while the integration tests are failing.
    - Mark an item done before its commit/push has landed.
```
