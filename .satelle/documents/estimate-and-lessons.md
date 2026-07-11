---
type: document
title: Estimate wall-time and post-done lessons (process note)
description: Light process guidance — project-workflow estimates must budget dogfood; improvement work attaches lessons after done.
tags: [document, process, epic:workflow-review-followups]
timestamp: '2026-07-12T00:00:00Z'
---

# Estimate wall-time and post-done lessons

Light conventions only — no new binary commands. Lives here under
`.satelle/documents/`; the estimate gate still only checks **presence** of tags
(`satelle-estimate-actual-review`).

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

## Lessons after done

**When:** always for stories on an **improvement epic** (or any story tagged
`epic:*` whose work taught process/tooling lessons). **Recommended** for every
shipped project-workflow story that hit a non-obvious reject, flake, or process
trap — skip pure mechanical substrate typos.

**What:**

1. Attach a short `lessons` document on the story:
   ```bash
   satelle story attach <sty_id> --name lessons --type lessons --body "…"
   ```
2. Record a ledger row so the timeline shows it (when the CLI surface supports
   `kind=lessons`; otherwise the attachment alone is the durable artifact):
   ```bash
   satelle story log <sty_id> --kind lessons --message "…"
   ```
   If `--kind lessons` is unavailable, attach only and mention lessons in the
   step summary.

Keep lessons actionable (what to do next time), not a diary.
