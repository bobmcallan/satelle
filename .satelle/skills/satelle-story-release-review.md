---
name: satelle-story-release-review
scope: project
type: skill
tags: [type:skill, type:reviewer]
description: Single gate on release → done (sty_d9a0b573), merging former push/committed/done reviewers. Isolated read-only reviewer judges the release shipped correctly (version bump + build stamp in HEAD, conventional commit ending in story id with NO AI attribution, and — as CI-green authority — recorded run URLs/conclusions/tag confirm a green test run + published release, rejecting failing/absent/unconcluded runs, plus a recorded summary) AND that acceptance criteria are met. Judges recorded evidence; never commits, pushes, or records.
---

# Story release review (release → done gate)

Isolated, **read-only** reviewer: may the story close (`release → done`)? The merged `release` step (in-loop) committed the slice with a version bump, pushed it, and recorded CI run URLs/conclusions and the published release as a summary — without babysitting runs. Judge that recorded evidence and the ACs — read to verify; never commit, push, edit, or record. No shell: judge recorded conclusions, don't fetch CI live. You get `{story, from, to}` on stdin (`story` has title, body, acceptance criteria).

## 1. Judge release evidence

Read the repo and recorded evidence (`.satelle/stories/<sty_id>/` summary attachment, `git show HEAD`, ledger/op-log):

- **Version bump**: `.version` in `HEAD` carries an incremented `satelle.version` and a fresh `satelle.build` stamp (`git show HEAD --stat` shows `.version`).
- **Commit convention**: subject is a conventional commit ending with the story id in parens; **NO AI attribution** — no `Co-Authored-By`, no "generated with" trailer (inspect actual trailers/body).
- **CI + release**: you are the authority on "CI is green" — judge from RECORDED evidence (the summary's `test`/`release` run URLs, conclusions, published tag `v<satelle.version>`). **Reject** when a recorded run concluded failure, is ABSENT, or hasn't concluded — pending/in-progress/missing conclusion is absent evidence, never an implied pass; name it.
- **Recorded summary**: a story-implementation-summary attachment exists capturing what shipped.

## 2. Judge acceptance criteria

Walk the numbered ACs; confirm each is satisfied by evidence in the shipped slice (committed change, tests, recorded result). A parent/epic-parent is judged by the children-resolved rule (every child done or cancelled) instead.

- **Accept**: release shipped correctly AND every AC met by evidence.
- **Reject**: a release-evidence check fails (missing bump, AI attribution, red/absent CI or release, no summary) or an AC unmet — name the specific gap.

Fair gate: judge stated ACs as written.

## Verdict

Reply with exactly one JSON object, nothing else, of that shape:

```json
{"decision": "accept", "notes": ""}
```

`decision` is `"accept"` or `"reject"`; `notes` is a brief actionable string (may be empty on accept). See [[satelle-done-is-last]], [[satelle-agent-model]].
