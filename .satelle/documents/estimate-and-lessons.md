---
type: document
title: Estimate wall-time (process note)
description: Light process guidance — project-workflow estimates must budget dogfood. Lessons capture lives in @skill:satelle-lessons (post-release), not here.
tags: [document, process]
timestamp: '2026-07-13T00:00:00Z'
---

# Estimate wall-time

Light conventions only — no new binary commands. The estimate gate still only
checks **presence** of tags (`satelle-estimate-actual-review`).

## Who records estimates (producer ownership)

The estimate gate only checks **tag presence**. Ownership is the **driving
session**, not the isolated planner (sty_b9ecd5d2):

- **Before `plan → in_progress`:** driving session runs
  `satelle story estimate <id> --time … --tokens …`.
- **Before `release → done`:** driving session runs
  `satelle story actual <id> --tokens … [--time …]` (usually during release).

The planner skill may size the work in prose; it must not be assumed to set tags.
See `@skill:plan` (section "Estimate tags").

## Estimates (project-workflow stories)

When recording a plan estimate (`satelle story estimate <id> --time … --tokens …`),
**wall-time for a project-workflow story includes dogfood**, not just coding:

| Include | Why |
| --- | --- |
| Implement + unit/integration | Obvious |
| **CI** (`test` + version-gated `release` on the push) | Push is the publish path; wait/observe, do not assume green |
| **`satelle update`** | Installs the **published** asset — not a local `make install` substitute |
| **Live footer verify** | Dogfood triad: CLI version + live footer + persistent supervisor |

Under-running estimates almost always omit the release/dogfood tail. Budget it.

Substrate-workflow stories (markdown-only, no version bump) do **not** need the
dogfood tail in the estimate.

## Lessons (moved)

Post-release satelle-friction lessons are captured by **`@skill:satelle-lessons`**,
dispatched once on enter-`done` via the project workflow's
`on_enter_agent=retrospective`. Enumerate the corpus with:

```bash
satelle story lessons
```

Do not attach lessons into session context. Optional input for
`tsk_context-audit` / `@skill:satelle-context-audit`.
