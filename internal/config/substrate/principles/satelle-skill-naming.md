---
name: satelle-skill-naming
type: principle
tags: [type:principle]
applies_to: ["*"]
description: When naming or reviewing a skill filename: satelle-<object>-<name>-<function>, so a skill's owner, subject and kind read from the name alone. Executor rubrics are bare action names, with no prefix.
---

# satelle skill naming — `satelle-<object>-<name>-<function>`

A satelle skill's **filename encodes what it is**, so owner, subject, and kind are
legible without opening it. The canonical form is:

```
satelle-<object>-<name>-<function>
```

- **object** — the noun the skill concerns: `story`, `task`, `execution`,
  `workflow`, `skill`, `principle`, `code`, `integration`, `step`, `estimate`, …
- **name** — the specific stage or aspect the skill acts on: `plan`, `release`,
  `done`, `intent`, `ac`, `validate-before`, `validate-after`, `actual`,
  `summary`. **Optional** — omit it when the object + function is already
  unambiguous (a bare `satelle-<object>-review` is that object's *structure*
  reviewer; `satelle-integration-review` needs no name).
- **function** — the kind of skill:
  - `review` — a **reviewer** gate: an isolated, read-only judgment that returns a
    verdict (`type:reviewer`). A `satelle-<object>-<name>-review` gates one
    workflow transition; a bare `satelle-<object>-review` validates the artifact
    on create/upsert.
  - `check` — a **functional check**: a self-contained coded gate
    (`type:functional-check`, a `​```check​` block) that decides deterministically
    without an agent, e.g. `satelle-integration-check`.
  - `triage` — an **advisor** the orchestrator consults rather than a gate that
    admits: it recommends, nothing dispatches on its verdict. Advisors carry
    `type:executor` and DO take the `satelle-` prefix — they are the
    satelle-namespaced exception to the bare-verb rule below, e.g.
    `satelle-story-blocked-triage`, `satelle-workflow-advisor`.
  - `summary` — a **summariser**: read-only narration of a transition that
    returns prose, not a verdict (`type:summariser`), e.g. `satelle-step-summary`.
  - `audit` — an **audit** over a corpus rather than one transition: it reports
    findings and enacts nothing (`type:audit`), e.g. `satelle-context-audit`,
    `satelle-reviewer-objective-audit`.

Examples that follow the convention: `satelle-story-plan-review`,
`satelle-story-release-review`, `satelle-story-done-review`,
`satelle-code-ac-review`, `satelle-task-validate-after-review`,
`satelle-integration-check`.

## Executor rubrics are the exception

An **executor** skill (an in-loop or dispatched *doing* rubric — it mutates the
tree, it does not gate) is named by its **bare action verb**, without the
`satelle-` prefix: `plan`, `release`, `integrate`. The `satelle-` prefix marks
satelle-namespaced reviewers and system skills; a bare action name marks a
workflow executor step. Do not name an executor `satelle-*-review` — the
`review` function is reserved for read-only gates (see
[[satelle-reviewer-self-contained]]).

## Known drift: coded checks named `-review`

Two shipped functional checks — `satelle-estimate-actual-review` and
`satelle-task-validate-before-review` — use the `review` suffix this convention
reserves for LLM reviewer gates. Both are coded: a `​```check​` fence decides,
no agent is dispatched.

The drift stands deliberately. A skill NAME is a binding surface: routes and
agent bindings reference skills by name, so renaming one breaks every repo that
binds the old name. The cost of the rename exceeds the cost of the
inconsistency.

The options are recorded, not chosen:

1. Leave both as they are — grandfathered, with this note as the record.
2. Ship the `-check` name alongside the `-review` name as an alias, and
   deprecate the old name over a release.
3. Rename outright, with a migration that rewrites authored route and binding
   references to the new name.

**Going forward:** a new coded check uses `-check`. Only these two names are
grandfathered — a third would make the convention unreadable, which is the
whole reason it exists.

## Why this is enforced

The name is the fastest audit surface: `satelle-story-release-review` says
"story object, release stage, reviewer gate" at a glance, and a mismatch between
the name and the artifact (a `-review` that mutates, an executor with a
`satelle-` prefix, an object that is not a real noun) is a design smell a reviewer
should reject. Workflows reference skills by these names, so a consistent scheme
keeps `@skill:` references predictable and dangling-free.

See [[satelle-constitution]], [[satelle-reviewer-self-contained]].
