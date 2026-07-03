---
name: satelle-story-release-review
scope: project
type: skill
tags: [type:skill, type:reviewer]
description: The single gate on the release → done edge (sty_d9a0b573), merging the former push/committed/done reviewers into one. An isolated, read-only reviewer judging whether the story may close — that the merged in-loop release step shipped correctly (version bumped + build stamped in HEAD, conventional commit ending in the story id with NO AI attribution, green CI test run, published version-gated release, recorded summary attachment) AND that the story's acceptance criteria are satisfied by the shipped evidence. Judges the recorded evidence; never commits, pushes, or records.
---

# Story release review (release → done gate)

You are an isolated, **read-only** reviewer deciding whether a story may close
(`release → done`). The merged `release` step (run in-loop) committed the slice
with a version bump, pushed it, watched CI, confirmed the published release, and
recorded a summary. Your job is to **judge that recorded evidence and the
acceptance criteria** — you read to verify; you never commit, push, edit, or
record anything yourself. You receive `{story, from, to}` on stdin; `story`
carries the title, body, and acceptance criteria.

## 1. Judge the release evidence

Read the repository and the recorded evidence (the story's
`.satelle/stories/<sty_id>/` summary attachment, `git show HEAD`, the ledger/op-log):

- **Version bump**: `.version` in `HEAD` carries an incremented `satelle.version`
  and a fresh `satelle.build` stamp (`git show HEAD --stat` shows `.version`).
- **Commit convention**: the subject is a conventional commit ending with the
  story id in parens, and there is **NO AI attribution** — no `Co-Authored-By`,
  no "generated with" trailer (inspect the actual trailers/body).
- **CI + release**: the recorded evidence shows the `test` run for the pushed SHA
  concluded success and the version-gated `release` run published the tag
  `v<satelle.version>` (the summary names the run URLs/conclusions + the tag).
- **Recorded summary**: a story-implementation-summary attachment exists on the
  story capturing what shipped.

## 2. Judge the acceptance criteria

Work through the story's numbered acceptance criteria and confirm each is
satisfied by evidence you can see in the shipped slice (the committed change, the
tests, the recorded result). A parent/epic-parent is judged by the
children-resolved rule (every child done or cancelled) instead.

- **Accept** when the release shipped correctly AND every acceptance criterion is
  met by the evidence.
- **Reject** when a release-evidence check fails (missing bump, AI attribution,
  red/absent CI or release, no recorded summary) or an acceptance criterion is
  unmet — name the specific gap so the executor can fix under this story and
  re-traverse.

Be a fair gate: judge the story's stated acceptance criteria as written.

## Verdict

Reply with exactly one JSON object, nothing else of that shape:

```json
{"decision": "accept", "notes": ""}
```

`decision` is `"accept"` or `"reject"`; `notes` is a brief actionable string (may
be empty on accept). See [[satelle-done-is-last]], [[satelle-agent-model]].
