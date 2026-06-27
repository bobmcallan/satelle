---
name: satelle-workflow
scope: project
kind: workflow
tags: [kind:workflow]
applies_to: ["*"]
description: This repo's project-scope workflow and its ACTIVE lifecycle — open → planned → in_progress → reviewed → integrated → deployed → done, with a blocked detour and a cancelled exit. The path to done is gated by isolated reviewers: a configuration-over-code plan check on open→planned, the intent check on planned→begin-work, a functional integration check (all tests pass), a functional deploy + health check, and the acceptance review on close. done stays terminal (see satelle-done-is-last). A project workflow takes precedence over the embedded system default satelle-baseline-workflow; no repo workflow is system-scoped.
---

# satelle workflow (project) — gated to done

> **This is a project workflow**, authored under `.satelle/workflows`. It is the
> ACTIVE workflow for this repo: a project-scope workflow takes precedence over
> the binary's embedded **system** default `satelle-baseline-workflow`. A repo
> workflow is never `scope: system` — system scope is reserved for the embedded
> canonical defaults. See the `satelle-repo-agnostic` principle.

A story or task moves **open → planned → in_progress → reviewed → integrated → deployed → done**, may
detour through **blocked**, and may exit early to **cancelled**. Every edge on the
path to `done` is **gated** by an isolated reviewer; the executor cannot self-enact
a gated edge — a reject pushes back with notes. `done` is always the terminal
state — the integration and deploy gates come *before* it (see the
`satelle-done-is-last` principle).

## Workflow

- **open → planned** — judge the intended implementation; **gated** by
  `satelle-plan-config-over-code-review`, which accepts only a plan that lands
  process/gates/opinions as authored substrate rather than baking them into the
  binary (see the `satelle-configuration-over-code` principle).
- **planned → in_progress** — begin work; **gated** by `satelle-story-intent-review`
  (the story must be well-formed before work starts).
- **in_progress → blocked** / **blocked → in_progress** — record/resume a stall.
- **in_progress → reviewed** — **gated** by `satelle-story-code-review`, an LLM
  tech-lead pre-review: it reads the modified code, judges it against the
  acceptance criteria, and checks the integration tests align with the code —
  WITHOUT executing them.
- **reviewed → integrated** — **gated** by `satelle-story-integration-review`, a
  functional check that runs the full integration suite and accepts only if
  **every** test passes.
- **integrated → deployed** — **gated** by `satelle-story-deploy-review`, a functional
  check that deploys the service locally and validates it with a **health check on
  the CLI and the web UI**.
- **deployed → done** — close the item; **gated** by `satelle-story-done-review`
  (the acceptance criteria must be satisfied). `done` is terminal.
- **open/planned/in_progress/integrated/deployed → cancelled** — abandon (record why).

The closing path is deliberate and fully gated: `in_progress → (tech-lead code
review) → reviewed → (integration tests pass) → integrated → (deploy + health
check pass) → deployed → (reviewer accepts the acceptance criteria) → done`.

```yaml
states:
  - open
  - planned
  - {name: in_progress, actor: executor}
  - blocked
  - {name: reviewed, actor: executor}
  - {name: integrated, actor: executor}
  - {name: deployed, actor: executor}
  - done
  - cancelled
transitions:
  - {from: open, to: planned, reviewer_skill: "satelle-plan-config-over-code-review"}
  - {from: planned, to: in_progress, reviewer_skill: "satelle-story-intent-review"}
  - {from: in_progress, to: blocked}
  - {from: blocked, to: in_progress}
  - {from: in_progress, to: reviewed, reviewer_skill: "satelle-story-code-review"}
  - {from: reviewed, to: integrated, reviewer_skill: "satelle-story-integration-review"}
  - {from: integrated, to: deployed, reviewer_skill: "satelle-story-deploy-review"}
  - {from: deployed, to: done, reviewer_skill: "satelle-story-done-review"}
  - {from: open, to: cancelled}
  - {from: planned, to: cancelled}
  - {from: in_progress, to: cancelled}
  - {from: reviewed, to: cancelled}
  - {from: integrated, to: cancelled}
  - {from: deployed, to: cancelled}
```

## Environment

Drives a work item open → planned → in_progress → reviewed → integrated → deployed → done. The path to
done is enforced by gates (a reject blocks the move); the guardrails below are the
intent the reviewers read.

```yaml
guardrails:
  always:
    - Drive an engaged item to a terminal state (done or cancelled) — don't leave work open indefinitely.
    - Give a story/task numbered acceptance criteria before starting, and satisfy them before moving to done.
    - When work stalls, set status to blocked with a note on what it's waiting on, rather than leaving it silently in_progress.
    - Let the integration gate run the suite — request 'integrated' and let satelle-story-integration-review judge it, rather than self-asserting the tests pass.
    - Commit and push once the work is integrated and deployed; done is the terminal sign-off.
  ask_first: []
  never:
    - Place any state after done — done is always the terminal success state.
    - Self-enact a gated edge the reviewer has not accepted.
    - Mark an item done with unmet acceptance criteria.
    - Advance past the integration gate with a failing suite, or past the deploy gate with a failing health check.
```
