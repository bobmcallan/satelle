---
name: satelle-baseline-workflow
scope: system
kind: workflow
tags: [kind:workflow]
applies_to: ["*"]
description: REPO OVERRIDE of the embedded canonical satelle-baseline-workflow (config/substrate/workflows). This repo's gated lifecycle — open → in_progress → integrated → deployed → done, with a blocked detour and a cancelled exit. The path to done is gated by isolated reviewers: intent-plan on begin-work, a functional integration check (all tests pass), a functional deploy + health check, and the acceptance review on close. done stays the terminal state (see satelle-done-is-last). It shadows, but never edits, the binary's canonical default.
---

# Baseline workflow (repo override) — gated to done

> **This file is a repo override**, not the canonical source. The binary ships an
> embedded canonical `satelle-baseline-workflow` under `config/substrate/workflows`;
> because this file shares its name it shadows that default for this repo only.
> See the `satelle-repo-agnostic` principle.

A story or task moves **open → in_progress → integrated → deployed → done**, may
detour through **blocked**, and may exit early to **cancelled**. Every edge on the
path to `done` is **gated** by an isolated reviewer; the executor cannot self-enact
a gated edge — a reject pushes back with notes. `done` is always the terminal
state — the integration and deploy gates come *before* it, never after (see the
`satelle-done-is-last` principle).

## Workflow

- **open → in_progress** — begin work; **gated** by `satelle-intent-plan-review`
  (the story must be well-formed before work starts).
- **in_progress → blocked** / **blocked → in_progress** — record/resume a stall.
- **in_progress → integrated** — **gated** by `satelle-integration-review`, a
  functional check that runs the full integration suite (`make integration`) and
  accepts only if **every** test passes.
- **integrated → deployed** — **gated** by `satelle-deploy-review`, a functional
  check that deploys the service locally and validates it with a **health check on
  the CLI and the web UI**.
- **deployed → done** — close the item; **gated** by `satelle-story-done-review`
  (the acceptance criteria must be satisfied). `done` is terminal.
- **open/in_progress/integrated/deployed → cancelled** — abandon (record why).

The closing path is deliberate and fully gated: `in_progress → (integration tests
pass) → integrated → (deploy + health check pass) → deployed → (reviewer accepts
the acceptance criteria) → done`.

```yaml
states:
  - open
  - {name: in_progress, actor: executor}
  - blocked
  - {name: integrated, actor: executor}
  - {name: deployed, actor: executor}
  - done
  - cancelled
transitions:
  - {from: open, to: in_progress, reviewer_skill: "satelle-intent-plan-review"}
  - {from: in_progress, to: blocked}
  - {from: blocked, to: in_progress}
  - {from: in_progress, to: integrated, reviewer_skill: "satelle-integration-review"}
  - {from: integrated, to: deployed, reviewer_skill: "satelle-deploy-review"}
  - {from: deployed, to: done, reviewer_skill: "satelle-story-done-review"}
  - {from: open, to: cancelled}
  - {from: in_progress, to: cancelled}
  - {from: integrated, to: cancelled}
  - {from: deployed, to: cancelled}
```

## Environment

Drives a work item open → in_progress → integrated → deployed → done. The path to
done is enforced by gates (a reject blocks the move); the guardrails below are the
intent the reviewers read.

```yaml
guardrails:
  always:
    - Drive an engaged item to a terminal state (done or cancelled) — don't leave work open indefinitely.
    - Give a story/task numbered acceptance criteria before starting, and satisfy them before moving to done.
    - When work stalls, set status to blocked with a note on what it's waiting on, rather than leaving it silently in_progress.
    - Let the integration gate run the suite — request 'integrated' and let satelle-integration-review judge it, rather than self-asserting the tests pass.
    - Commit and push once the work is integrated and deployed; done is the terminal sign-off.
  ask_first: []
  never:
    - Place any state after done — done is always the terminal success state.
    - Self-enact a gated edge the reviewer has not accepted.
    - Mark an item done with unmet acceptance criteria.
    - Advance past the integration gate with a failing suite, or past the deploy gate with a failing health check.
```
