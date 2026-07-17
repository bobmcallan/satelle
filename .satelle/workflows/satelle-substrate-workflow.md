---
name: satelle-substrate-workflow
scope: project
type: workflow
tags: [type:workflow]
applies_to: ["substrate"]
create_review: satelle-story-create-review
description: Lifecycle for SUBSTRATE-only changes — edits to authored markdown under .satelle/ (workflows, skills, principles, documents, tasks) and docs/, which leave the binary unchanged. Resolved by category the same way the parent workflow is: a story with category "substrate" gets this workflow (a category-specific applies_to beats the wildcard satelle-project-workflow). A minimal path authored in the DOT standard (the agent model): backlog → in_progress → done, with a cancelled exit. There is deliberately NO plan/code-ac/integration step and NO version bump or release — a markdown edit has no code to plan, no acceptance-criteria implementation to code-review, no integration behaviour to test, and does not change the binary (so no v-tag/CI release is cut). in_progress engages the story so the agent authors + commits + pushes the substrate slice IN-LOOP (the commit gate requires an engaged story). The one gate is the close (in_progress → done): the DETERMINISTIC functional check satelle-substrate-only-check, which rejects if the committed slice touches any non-substrate path — the guardrail that keeps the category choice honest, so a code change cannot ride this lighter workflow to dodge the project workflow's gates. done stays terminal (satelle-done-is-last).
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
  inspects the committed slice and **rejects if any non-substrate path was
  touched**. This is the guardrail your category choice rests on: because the
  category (`substrate`) selects this lighter workflow, an agent could mis-file a
  *code* change here to skip the project workflow's gates — the check catches it,
  because the diff itself betrays the code. The category is the choice; the check
  keeps it honest.

`in_progress` **engages** the story so the agent authors the edit and **commits +
pushes it IN-LOOP** (the commit gate refuses a commit with no engaged story, so
the commit must happen while engaged). The mandatory `step` node records a
per-transition summary, so the change is tracked in satelle's ledger as well as in
git. `backlog → in_progress` is ungated — there is nothing to plan or estimate.

```dot
digraph satelle_substrate_workflow {
  graph [goal="Land a substrate-only (markdown) change — committed, pushed, summarised — verified to touch no code, without a version bump or release", vars="story, repo_root"]
  rankdir=LR

  backlog     [shape=Mdiamond]
  in_progress [agent=executor, prompt="@skill:substrate"]                                          // in-loop: author + commit + push the substrate slice
  done        [shape=Msquare]                                           // terminal (the close gate verifies substrate-only)
  cancelled   [agent=reviewer, prompt="@skill:satelle-story-cancel-review"]
  // Park: agent=reviewer keeps blocked non-engaging; on_enter_agent runs triage
  // once on entry (sty_5cabe26f) without opening edit/commit gates while parked.
  blocked     [agent=reviewer, prompt="@skill:satelle-story-blocked-review", on_enter_agent=blocked-triage, on_enter_prompt="@skill:satelle-story-blocked-triage", from="*"]

  // step opts into per-transition step summaries (sty_9a139c78): edge-less, mandatory.
  step        [agent=reviewer, prompt="@skill:satelle-step-summary", mandatory=true]

  // The one gate: a DETERMINISTIC functional check on the close — the committed
  // slice must be substrate-only (no code), or the close is rejected.
  subcheck    [agent=reviewer, prompt="@skill:satelle-substrate-only-check", on="done"]

  backlog     -> in_progress
  in_progress -> done         // gated by subcheck (on="done"): substrate-only slice
  // A close reject leaves the story at in_progress (the transition does not enact),
  // so the agent just fixes the slice and re-requests done — no recovery edge needed.
  backlog     -> cancelled
  in_progress -> cancelled
  blocked     -> cancelled
}
```

## Environment

```yaml
guardrails:
  always:
    - Use this workflow only for substrate-only changes (.satelle/ and docs/ markdown); a change that touches code belongs on the project workflow.
    - Commit + push the slice IN-LOOP while engaged at in_progress; the commit gate refuses a commit with no engaged story.
    - Record the change through the mandatory step summary so it is tracked in satelle and git.
  ask_first: []
  never:
    - Bump .version or cut a release for a substrate-only change — the binary is unchanged.
    - Place any state after done — done is the terminal success state.
    - Ride a code change on this workflow to skip the project workflow's gates — the close check rejects a non-substrate slice.
```
