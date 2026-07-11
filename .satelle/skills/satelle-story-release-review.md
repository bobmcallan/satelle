---
name: satelle-story-release-review
scope: project
type: skill
tags: [type:skill, type:reviewer]
description: Single gate on release → done. Isolated read-only reviewer judges the release shipped correctly (version bump, conventional commit, green CI + published tag from recorded evidence, recorded summary) AND that local dogfood install was verified (CLI + live service at the new version) AND acceptance criteria are met. Judges recorded evidence; never commits, pushes, or installs.
---

# Story release review (release → done gate)

Isolated, **read-only** reviewer: may the story close (`release → done`)? The release step committed with a version bump, pushed, **installed locally**, and recorded CI + local-install evidence. Judge that evidence and the ACs — read to verify; never commit, push, install, or edit. No shell for live CI fetch: judge **recorded** conclusions. You get `{story, from, to}` on stdin.

## 1. Judge release evidence

Read the repo and recorded evidence (`.satelle/stories/<sty_id>/` summary attachment, `git show HEAD`, ledger/op-log):

- **Version bump**: `.version` in `HEAD` carries an incremented `satelle.version` and a fresh `satelle.build` stamp (`git show HEAD --stat` shows `.version`).
- **Commit convention**: subject is a conventional commit ending with the story id in parens; **NO AI attribution** — no `Co-Authored-By`, no "generated with" trailer.
- **CI + release**: authority on "CI is green" from RECORDED evidence (summary's `test`/`release` run URLs, conclusions, published tag `v<satelle.version>`). **Reject** when a recorded run concluded failure, is ABSENT, or hasn't concluded.
- **Dogfood triad (named checks)** — local install is part of the release, not optional hygiene. The summary must evidence **all three**; reject naming the failed check:
  - **`check_cli_version`**: CLI at the new version (`satelle version` reports `$VER` / matching commit). Reject when missing or only implied.
  - **`check_live_footer`**: live web service/footer (or equivalent health body) at the **same** `$VER`. Reject when only CLI is checked and the service is unmentioned (stale-process failure mode).
  - **`check_persistent_supervisor`**: the live service runs under a persistent supervisor (system unit or linger-backed user manager), not an ephemeral `nohup`/`setsid` relaunch. Reject when the summary only records a throwaway serve.
- **Recorded summary**: a story-implementation-summary attachment exists capturing what shipped.

## 2. Judge acceptance criteria

Walk the numbered ACs; confirm each is satisfied by evidence in the shipped slice. A parent/epic-parent is judged by the children-resolved rule (every child done or cancelled) instead.

- **Accept**: release shipped correctly (including the full dogfood triad) AND every AC met by evidence.
- **Reject**: a release-evidence check fails (missing bump, AI attribution, red/absent CI or release, no summary, or any of **`check_cli_version` / `check_live_footer` / `check_persistent_supervisor`**) or an AC unmet — name the specific gap (use the check name when a triad member fails).

Fair gate: judge stated ACs as written.

## Verdict

Reply with exactly one JSON object, nothing else, of that shape:

```json
{"decision": "accept", "notes": ""}
```

`decision` is `"accept"` or `"reject"`; `notes` is a brief actionable string (may be empty on accept). See [[satelle-done-is-last]], [[satelle-agent-model]].
