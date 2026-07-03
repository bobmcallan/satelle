---
name: satelle-skill-naming
scope: system
type: principle
tags: [type:principle]
applies_to: ["*"]
description: The naming convention for satelle skills — satelle-<object>-<name>-<function> — so a skill's owner, subject, and kind read from its filename alone. Reviewers and system skills carry the satelle- prefix and encode object (story|task|execution|workflow|…), an optional name (the stage/aspect), and function (review|check|…). Executor rubrics are bare action names. A skill named against this convention is a defect a reviewer should flag.
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

Examples that follow the convention: `satelle-story-plan-review`,
`satelle-story-release-review`, `satelle-story-done-review`,
`satelle-code-ac-review`, `satelle-task-validate-after-review`,
`satelle-integration-check`.

## Executor rubrics are the exception

An **executor** skill (an in-loop or dispatched *doing* rubric — it mutates the
tree, it does not gate) is named by its **bare action verb**, without the
`satelle-` prefix: `plan`, `release`, `integrate`. The `satelle-` prefix marks
satelle-namespaced reviewers and system skills; a bare action name marks a
repo-local executor step. Do not name an executor `satelle-*-review` — the
`review` function is reserved for read-only gates (see
[[satelle-reviewer-self-contained]]).

## Why this is enforced

The name is the fastest audit surface: `satelle-story-release-review` says
"story object, release stage, reviewer gate" at a glance, and a mismatch between
the name and the artifact (a `-review` that mutates, an executor with a
`satelle-` prefix, an object that is not a real noun) is a design smell a reviewer
should reject. Workflows reference skills by these names, so a consistent scheme
keeps `@skill:`/`reviewer_skill` references predictable and dangling-free.

See [[satelle-constitution]], [[satelle-reviewer-self-contained]].
