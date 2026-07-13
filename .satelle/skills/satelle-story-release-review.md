---
name: satelle-story-release-review
scope: project
type: skill
tags: [type:skill, type:reviewer]
description: Single gate on release → done. Isolated read-only reviewer judges the release shipped correctly (version bump, conventional commit, green CI + published tag from recorded evidence, recorded summary) AND that local dogfood install was verified (CLI + live service at the new version) AND acceptance criteria are met, AND reports a plan-adherence metric in notes; when a dispatched coder implemented the story, check_plan_consumed judges plan-consumption evidence. Judges recorded evidence; never commits, pushes, or installs. Plan divergence alone never rejects.
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

## 3. Plan adherence (metric, not gate)

Read the story's **plan** attachment when present (on disk under
`.satelle/stories/<sty_id>/` — e.g. `plan.md` — your grant is read-only with no
shell). Compare it against what shipped (step summaries, the release summary,
`git show HEAD` when useful).

Always put a structured plan-adherence line in **notes** on both accept and
reject so the metric lands in the ledger on every close:

- With a plan: `plan-adherence: <met>/<total> — deviations: <named plan steps that changed and why per the summaries, or "none">`
- Without a plan: `plan-adherence: n/a — no plan attached`

**Divergence from the plan is NEVER a sole reject reason.** The ACs are the
contract; the plan is the route (same advisory posture as
`satelle-estimate-actual-review` for estimate-vs-actual). Rejects still come only
from release-evidence checks or unmet ACs.

## 4. check_plan_consumed (named check)

When the story was implemented by a **dispatched coder** (ledger / dispatch
evidence / executor.log shows a `coder` dispatch for `in_progress`), the recorded
evidence must show the coder consumed the plan before implementing:

- a typed `plan-consumed` event in the ledger/op-log (`satelle story log … --kind plan-consumed`), **or**
- a `PLAN-CONSUMED:` (or plan-consumed) statement in the recorded run output
  (dispatch sink under `.satelle/logs/dispatch/`, or `executor.log`) naming the
  plan attachment and the plan steps followed.

**Reject naming `check_plan_consumed`** only when the evidence channel exists for
the story (a dispatched-coder run is recorded) **and** consumption evidence is
absent. An **in-loop-implemented** story (no coder dispatch on the path) is
**exempt** — do not reject for missing plan-consumed on those.

## Verdict

- **Accept**: release shipped correctly (including the full dogfood triad) AND
  every AC met by evidence, AND (when applicable) check_plan_consumed passes.
  Notes carry the plan-adherence metric.
- **Reject**: a release-evidence check fails (missing bump, AI attribution,
  red/absent CI or release, no summary, or any of **`check_cli_version` /
  `check_live_footer` / `check_persistent_supervisor`**), an AC is unmet, or
  **`check_plan_consumed`** fails for a dispatched-coder story — name the
  specific gap (use the check name when a named check fails). Notes still carry
  the plan-adherence metric.

Reply with exactly one JSON object, nothing else, of that shape:

```json
{"decision": "accept", "notes": "plan-adherence: n/a — no plan attached"}
```

`decision` is `"accept"` or `"reject"`; `notes` carries the plan-adherence metric
and any reject gap. See [[satelle-done-is-last]], [[satelle-agent-model]].
