---
name: satelle-substrate-workflow
scope: system
type: workflow
tags: [type:workflow]
applies_to: ["substrate"]
create_review: satelle-story-create-review
description: Lifecycle for markdown-only substrate changes under .satelle/ and docs/ — no code, no version bump. backlog → in_progress → done; close gated by satelle-substrate-only-check. Category substrate selects this path.
---

# satelle substrate workflow — track a markdown-only change without code ceremony

A **substrate change** — an edit to the authored markdown under `.satelle/`
(workflows, skills, principles, documents, tasks) or `docs/` — has a slice to
commit but **no code**. Forcing it through the project workflow's plan → code-ac
→ integration → version-bump → release path is ceremony a doc edit does not earn.
This workflow gives it a **minimal, category-selected** lifecycle, authored in the
**DOT standard** (node-centric — see the `satelle-agent-model` principle):
**backlog → in_progress → done**, with an early exit to **cancelled**.

Two things make it distinct from the project workflow, both because substrate is
not code:

- **No version bump, no release.** A markdown edit leaves the binary unchanged, so
 no `.version` bump and no `v<version>` tag/CI release are cut. The commit lands
 on `main` and the running service picks it up on its next reindex — no redeploy.
- **The close is the only gate, and it is DETERMINISTIC.** `in_progress → done`
 runs `satelle-substrate-only-check` — a self-contained functional check that
 inspects the story's change set (recorded + working tree + any commit naming it) and **rejects if any non-substrate path was
 touched**. This is the guardrail your category choice rests on: because the
 category (`substrate`) selects this lighter workflow, an agent could mis-file a
 *code* change here to skip the project workflow's gates — the check catches it,
 because the diff itself betrays the code. The category is the choice; the check
 keeps it honest.

`in_progress` **engages** the story so the agent authors the edit in-loop. Where
the repo tracks substrate in git, **commit + push while engaged** (the commit
gate refuses a commit with no engaged story). Repos that git-ignore `.satelle/`
(hosted `[sync] personal` continuity) close via the recorded/live change set
without a commit. The mandatory `step` node records a
per-transition summary, so the change is tracked in satelle's ledger as well as in
git. `backlog → in_progress` is ungated — there is nothing to plan or estimate.

```dot
digraph satelle_substrate_workflow {
 graph [goal="Land a substrate-only (markdown) change — recorded, summarised (and committed where substrate is tracked) — verified to touch no code, without a version bump or release", vars="story, repo_root"]
 rankdir=LR

 backlog [shape=Mdiamond]
 in_progress [agent=executor] // in-loop: author (and commit + push where substrate is tracked)
 done [shape=Msquare] // terminal (the close gate verifies substrate-only)
 cancelled [agent=reviewer, prompt="@skill:satelle-story-cancel-review"]
 blocked [agent=reviewer, prompt="@skill:satelle-story-blocked-review", from="*"]

 // step opts into per-transition step summaries: edge-less, mandatory.
 step [agent=reviewer, prompt="@skill:satelle-step-summary", mandatory=true]

 // The one gate: a DETERMINISTIC functional check on the close — the story's
 // change set must be substrate-only (no code); no commit required.
 subcheck [agent=reviewer, prompt="@skill:satelle-substrate-only-check", on="done"]

 backlog -> in_progress
 in_progress -> done // gated by subcheck (on="done"): substrate-only slice
 // A close reject leaves the story at in_progress (the transition does not enact),
 // so the agent just fixes the slice and re-requests done — no recovery edge needed.
 backlog -> cancelled
 in_progress -> cancelled
 blocked -> cancelled
}
```

## Environment

```yaml
guardrails:
 always:
 - Use this workflow only for substrate-only changes (.satelle/ and docs/ markdown); a change that touches code belongs on the project workflow.
 - Where the repo tracks substrate in git: commit + push the slice IN-LOOP while engaged (the commit gate refuses a commit with no engaged story). Repos that git-ignore .satelle/ close via the change-set channels without a commit.
 - Record the change through the mandatory step summary so it is tracked in satelle and git.
 ask_first: []
 never:
 - Bump .version or cut a release for a substrate-only change — the binary is unchanged.
 - Place any state after done — done is the terminal success state.
 - Ride a code change on this workflow to skip the project workflow's gates — the close check rejects a non-substrate slice.
```
