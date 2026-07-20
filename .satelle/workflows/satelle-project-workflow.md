---
name: satelle-project-workflow
scope: project
type: workflow
tags: [type:workflow]
applies_to: ["*"]
create_review: satelle-story-create-review
description: This repo's project-scope workflow, authored in DOT (the agent model). A story moves backlog → plan → in_progress → integration → release → done, with a cancelled exit. It is REVIEWER-FIRST (a reviewer gates every transition). The plan step DISPATCHES to an isolated read-only planner (agent=planner); in_progress, integration, and release run IN-LOOP on the driving session (agent=executor) so the orchestrator performs the work with full session context — no isolated worker subprocess for code/integrate/release. Every stage is reviewed: backlog → plan by satelle-story-intent-review; plan → in_progress by satelle-story-plan-review; in_progress → integration by satelle-code-ac-review then satelle-workflow-change-review (CSV edge); integration → release by satelle-integration-review plus scoped satelle-integration-check (make integration); release → done by satelle-story-release-review plus scoped satelle-changelog-entry-check (CHANGELOG.md must carry the released version). Always-on estimate gates begin-work and close.
---

# satelle workflow (project) — the agent model, authored in DOT

> **This is a project workflow** under `.satelle/workflows`, the ACTIVE workflow
> for this repo: a project-scope workflow takes precedence over the binary's
> embedded **system** default `satelle-baseline-workflow`. See the
> `satelle-repo-agnostic` and `satelle-agent-model` principles.

The lifecycle is the **DOT graph** below — read it as the authority; this prose
only orients and must not restate it. Each node is a step carrying an `agent`.
This workflow is **reviewer-first** (a reviewer gates every transition). **Plan**
dispatches to an isolated read-only `planner`; **in_progress**, **integration**,
and **release** run **in-loop** on the driving session (`agent=executor`) so the
session performs the work with its normal context, principles, and tools — not a
fresh isolated worker subprocess. The driving session orchestrates transitions,
performs the executor stages, and lets the gates judge.

`plan` stays on its own read-only binding because it is entered from the
non-performing `backlog` state, and the dispatch lock-guard refuses a code-writer
dispatched from a non-performing state. A **reviewer** node only gates *entry*
via its `prompt="@skill:NAME"` (read-only — it judges, never mutates). Status
advances only through a reviewer's accept. The gating begins at intake:
`backlog -> plan` is gated by `satelle-story-intent-review`. A reject leaves the
story at `backlog` to be fixed and re-requested (or cancelled via
`backlog -> cancelled`).

Two things the edges don't show. **There is no deploy state** — pushing to `main`
IS the release, verified by CI. And the **always-on gates are declared, not
injected**: the edge-less reviewer nodes `estimate` (`on="in_progress,done"`) and
`intcheck` (`on="release"`) run on the transitions their `on=` names, so the DOT is
the sole gating authority. `estimate` requires a plan estimate entering
`in_progress` and an actual entering `done`; `intcheck` runs `make integration` on
entry to `release` (the `integration -> release` edge), alongside that edge's
`satelle-integration-review` — so `integration` is a **visible testing step**, not a
gate hidden inside another transition (sty_15dbc0dd). The `integration -> in_progress`
and `release -> in_progress` edges are recovery: a reject returns the story to work
to fix and re-traverse, never bypass.

```dot
digraph satelle_workflow {
  graph [goal="Drive a story to done — plan reviewed against the ACs, implemented in-loop, released and verified by CI, every gate accepted", vars="story, repo_root"]
  rankdir=LR

  backlog     [shape=Mdiamond]
  plan        [agent=planner, prompt="@skill:plan"]     // DISPATCHED: isolated read-only planner
  in_progress [agent=executor, prompt="@skill:code"]    // IN-LOOP: driving session implements
  integration [agent=executor, prompt="@skill:integrate"] // IN-LOOP: driving session tests
  release     [agent=executor, prompt="@skill:release"] // IN-LOOP: driving session releases
  // Terminal success. on_enter_agent dispatches [retrospective] once with
  // @skill:satelle-lessons to attach a typed friction corpus (order:9) without
  // making done engaging — same pattern as blocked's on_enter triage.
  done        [shape=Msquare, on_enter_agent=retrospective, on_enter_prompt="@skill:satelle-lessons"]
  cancelled   [agent=reviewer, prompt="@skill:satelle-story-cancel-review"]
  // blocked is a park state (not engaged): world-not-ready, same ACs on resume.
  // agent=reviewer so the edit/commit gates do not treat it as engaged work.
  // on_enter_agent dispatches [blocked-triage] once on entry (sty_5cabe26f) without
  // making blocked engaging — orthogonal to agent=; park gate stays blocked-review.
  blocked     [agent=reviewer, prompt="@skill:satelle-story-blocked-review", on_enter_agent=blocked-triage, on_enter_prompt="@skill:satelle-story-blocked-triage", from="*"]

  // step opts this workflow into per-transition step summaries (sty_9a139c78):
  // an edge-less declaration, mandatory so a summary failure is surfaced.
  step        [agent=reviewer, prompt="@skill:satelle-step-summary", mandatory=true]

  // Declared scoped reviewers (edge-less, on="<target states>"): always-on gates the
  // workflow itself declares. estimate gates begin-work + close; intcheck runs
  // `make integration` on entry to release — i.e. on the integration -> release edge,
  // alongside that edge's satelle-integration-review — so integration is a VISIBLE step.
  // estimate: tags only. Producer = driving session (not planner):
  //   satelle story estimate before in_progress; satelle story actual before done.
  // See @skill:plan and documents/estimate-and-lessons.md (sty_b9ecd5d2).
  estimate    [agent=reviewer, prompt="@skill:satelle-estimate-actual-review", on="in_progress,done"]
  intcheck    [agent=reviewer, prompt="@skill:satelle-integration-check", on="release"]
  // changelogcheck fails closed on release→done when CHANGELOG.md has no entry
  // for the version on HEAD (sty_f52ba0c3).
  changelogcheck [agent=reviewer, prompt="@skill:satelle-changelog-entry-check", on="done"]
  // design: surface-scoped UI design-system gate (sty_e4359efe / epic:surface-scoped-steps).
  // Enqueued only for surface:ui stories on entry to integration — same spine for all.
  design [agent=reviewer, prompt="@skill:satelle-design-review", on="integration", applies_to="surface:ui"]

  backlog     -> plan         [agent=reviewer, prompt="@skill:satelle-story-intent-review"] // intake gate: a story must pass intent-review to enter plan
  plan        -> in_progress  [agent=reviewer, prompt="@skill:satelle-story-plan-review"]
  // code-ac then workflow-change (CSV; existing gate first). workflow-change
  // n/a-fast-accepts when the slice touches no workflow file (sty_9882b8c6).
  in_progress -> integration  [agent=reviewer, prompt="@skill:satelle-code-ac-review,satelle-workflow-change-review"]
  integration -> release      [agent=reviewer, prompt="@skill:satelle-integration-review"] // tests adequate (+ scoped intcheck runs make integration) -> enter release
  release     -> done         [agent=reviewer, prompt="@skill:satelle-story-release-review"]

  integration -> in_progress  // recovery: a test/review reject returns to work
  release     -> in_progress  // recovery: a release/done reject returns to work

  // Park / resume: world-not-ready (reason gated on entry). Resume is agent-directed.
  backlog     -> cancelled
  plan        -> cancelled
  in_progress -> cancelled
  blocked     -> cancelled
  integration -> cancelled
  release     -> cancelled
}
```

## Skill resolution

Every gate/skill this workflow names resolves through the doc-index, **project
scope (`.satelle/skills`) layered over the embedded system defaults**. The
dispatched `plan` (`@skill:plan`), and the in-loop `in_progress` (`@skill:code`),
`integration` (`@skill:integrate`) and `release` (`@skill:release`) rubrics, and
the reviewer gates (`satelle-story-intent-review`, `satelle-story-plan-review`,
`satelle-code-ac-review`, `satelle-integration-review`, `satelle-integration-check`,
`satelle-story-release-review`, `satelle-estimate-actual-review`,
`satelle-story-cancel-review`, `satelle-step-summary`), and the post-release
lessons capture (`satelle-lessons`, dispatched on enter-done via the
`[retrospective]` binding) are authored in this repo's `.satelle/skills` — so
there is no dangling `@skill:` reference and a story drives to a terminal state
without a missing-skill block. `satelle-workflow-change-review` (CSV sibling on
`in_progress → integration`) resolves from the **embedded** substrate (not
repo-authored). Reviewer gates degrade to advisory only if their rubric is
genuinely absent. Lessons are offline corpus (not session-injected).

## Environment

```yaml
guardrails:
  always:
    - Drive an engaged item to a terminal state (done or cancelled) — don't leave work open indefinitely.
    - Give a story numbered acceptance criteria before starting, and satisfy them before moving to done.
    - Dispatch only the plan stage to the isolated planner; perform in_progress, integration, and release in-loop as the driving session (agent=executor); let reviewer gates judge every transition.
    - Bump the version + commit + push + record the release in the in-loop release step; the release gate verifies the bump, CI, the published release, and the acceptance criteria before close.
  ask_first: []
  never:
    - Place any state after done — done is always the terminal success state.
    - Self-enact a gated edge the reviewer has not accepted.
    - Mark an item done with unmet acceptance criteria, or release with a failing CI run.
    - Re-dispatch in_progress / integration / release to an isolated worker when this workflow assigns them to the executor — those stages stay in-session.
```
