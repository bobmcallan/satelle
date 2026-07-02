---
name: record-release
scope: project
type: skill
tags: [type:skill, type:executor]
description: Executor skill for the `committed` step. Verifies the pushed slice's release evidence — the .version bump + build stamp, the conventional commit ending in the story id with NO AI attribution, the green `test` run for the SHA, and the published version-gated release — then records a PR-style implementation summary WITH the story as an attachment (`satelle story attach <id> --type story-implementation-summary --file …` → .satelle/stories/<id>/). Replaces the old satelle-push-review reviewer, which wrote files from a read-only role (misaligned; sty_97c53d72): verification-plus-recording is executor work — the done gate judges the recorded evidence.
---

# Record release (executor step)

You are the **executor** in the `committed` step. The `push` step has pushed the
slice and watched CI; your job is to **verify the release evidence and record
it** — the mutating half a reviewer must never do (see [[satelle-agent-model]]:
executors mutate, reviewers judge). The `done` gate then judges what you
recorded.

## 1. Verify the evidence

Confirm, and stop (do not advance) if any fails:

- **The bump**: `.version` carries the incremented `satelle.version` and a fresh
  `satelle.build` stamp, both in `HEAD` (`git show HEAD --stat`).
- **The commit convention**: a conventional-commit subject ending with the story
  id in parens; **no AI attribution** — inspect the actual trailers
  (`git log -1 --format='%(trailers)'`) and the body for `Co-Authored-By` /
  "generated with" lines.
- **The CI runs**: the `test` run for the pushed SHA concluded success, and the
  version-gated `release` run published the tag —
  `gh release view "v$(awk '$1=="satelle.version:"{print $2}' .version)"`.

## 2. Record the summary WITH the story

Write a short PR-style summary (what shipped, why, the SHA, the run
URLs/conclusions, the published tag) to a temp file, then attach it to the
story — the artifact lives in `.satelle/stories/<sty_id>/` and persists across
the story's whole lifecycle:

```bash
satelle story attach <sty_id> \
  --name "commit-summary-<sty_id>" \
  --type story-implementation-summary \
  --file /tmp/summary-<sty_id>.md
```

Do NOT write into `.satelle/documents/` — the old
`story-implementation-summary` sub-bundle is retired; summaries belong with
their story.

## Hand-off to the gate

You never enact your own status advance. The `done` gate
(`satelle-story-done-review`) reads what you recorded — the attached summary
(`satelle story docs <id>`), the ledger, and the op-log — and judges the close.
