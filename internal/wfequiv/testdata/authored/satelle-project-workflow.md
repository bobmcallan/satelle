---
name: satelle-project-workflow
scope: project
type: workflow
tags: [type:workflow]
applies_to: ["*"]
create_review: satelle-story-create-review
description: This repo's project-scope workflow, authored in DOT (the agent model). A story moves backlog → plan → in_progress → integration → release → done, with a cancelled exit. It is REVIEWER-FIRST (a reviewer gates every transition). The plan step DISPATCHES to an isolated read-only planner (agent=planner); in_progress, integration, and release run IN-LOOP on the driving session (agent=executor) so the orchestrator performs the work with full session context — no isolated worker subprocess for code/integrate/release. Every stage is reviewed: backlog → plan by satelle-story-intent-review; plan → in_progress by satelle-story-plan-review + architecture + integration-coverage (CSV, parallel=true); in_progress → integration by satelle-code-ac-review then satelle-story-scope-review then satelle-workflow-change-review (CSV) plus scoped satelle-format-vet-check (gofmt + go vet, zero tokens); integration → release by satelle-integration-review plus scoped satelle-integration-check; release → done by satelle-story-release-review plus scoped satelle-changelog-entry-check (CHANGELOG.md must carry the released version). Always-on estimate gates begin-work and close. FOUR CODED DEPLOYMENT GATES carry the deployment objectives, one gate per objective so a reject names the objective: satelle-build-unit-check (go build + go test, on integration), satelle-integration-check (make integration AND an assertion that the operator's installed binary and running service are unchanged, on release), satelle-ci-published-check (the test and release workflows concluded success for the actual HEAD SHA and the tag for .version is published, on done), satelle-dogfood-check (the installed CLI reports .version and the serving process runs the installed binary under a persistent supervisor, on done). These four VERIFY and never PERFORM: no coded gate pushes, tags, updates, installs or restarts anything.
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

**Reviewer bindings (sty_2f9b63b5):** the default `[reviewer]` is opus on a command
harness and judges gated *edges* (intent, plan trio, code-ac/scope/workflow-change,
integration-review, release-review, cancel/blocked). The high-frequency
`step` node allocates **`agent=reviewer-summary`** (Grok ACP, low effort) for
per-transition prose. `estimate`, `fmtcheck`, `unitcheck`, `intcheck`, `cicheck`,
`dogfoodcheck`, and `changelogcheck` are coded ```check gates: the engine returns at `skillCheck` before `gateBinding`
(`internal/agentstep/engine.go`), so they name **no** `agent=` — there is no
binding, grant, or role on that path (see `satelle-dot-standard`). A
second-vendor "cross-check" on judgment edges was retired: isolated reviewers
already receive a fresh process with no shared conversation.

Two things the edges don't show. **There is no deploy state** — pushing to `main`
IS the release, verified by CI. And the **always-on gates are declared, not
injected**: the edge-less reviewer nodes `estimate` (`on="in_progress,done"`),
`fmtcheck` (`on="integration"`), and `intcheck` (`on="release"`) run on the
transitions their `on=` names, so the DOT is the sole gating authority.
`estimate` requires a plan estimate entering `in_progress` and an actual entering
`done`; `fmtcheck` runs `gofmt` + `go vet` on entry to `integration` (the
`in_progress -> integration` edge, sty_343fe595) so format/vet never consume LLM
tokens; `intcheck` runs `make integration` on entry to `release` (the
`integration -> release` edge), alongside that edge's `satelle-integration-review`
— so `integration` is a **visible testing step**, not a gate hidden inside
another transition (sty_15dbc0dd).

**The four deployment objectives** (sty_7a2dc74b) each have exactly one coded
gate, so a failed deployment names the objective that failed rather than
rejecting generically: `unitcheck` (build + unit test) on entry to
`integration`; `intcheck` (integration test, *and* an assertion that the
operator's installed binary and running service are unchanged) on entry to
`release`; `cicheck` (the `test` and `release` workflows concluded success for
the actual `HEAD` SHA, and the tag for `.version` is published) and
`dogfoodcheck` (the installed CLI reports `.version`, and the serving process
runs the installed binary under a persistent supervisor) on entry to `done`.

These four **verify, they never perform**. No coded gate pushes, tags, updates,
installs, or restarts: the executor deploys and the gate confirms. That split is
deliberate — a reviewer that deploys concentrates deployment risk in the gate
path, and an automated restart has already stopped this repo's live service once
(sty_f20f3f3b). `cicheck` and `dogfoodcheck` exist because the LLM
`satelle-story-release-review` judges CI ids and status lines *the executor wrote
into its own summary*; these two read GitHub and the machine directly, so a
summary that softens a failure cannot pass. The `integration -> in_progress` and
`release -> in_progress` edges are recovery: a reject returns the story to work
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
  // Terminal success. Nothing fires on entry: under FLAT DISPATCH the
  // orchestrator is the sole scheduler, so the lessons corpus is captured by the
  // orchestrator running `satelle story retrospect <id>` after close, not by the
  // state dispatching an agent at itself.
  done        [shape=Msquare]
  cancelled   [agent=reviewer, prompt="@skill:satelle-story-cancel-review"]
  // blocked is a park state (not engaged): world-not-ready, same ACs on resume.
  // agent=reviewer so the edit/commit gates do not treat it as engaged work.
  // Entry dispatches NOTHING (sty_05a5e203, retiring sty_5cabe26f): the
  // orchestrator consults [blocked-triage] under @skill:satelle-story-blocked-triage
  // when it parks a story, and records the advice on the story so the
  // blocked-review gate judges it. Park gate stays blocked-review.
  blocked     [agent=reviewer, prompt="@skill:satelle-story-blocked-review", from="*"]

  // step opts this workflow into per-transition step summaries (sty_9a139c78):
  // an edge-less declaration, mandatory so a summary failure is surfaced.
  // Cheap Grok binding (agent=reviewer-summary) — high frequency, short prose;
  // the only of the summary/check nodes that actually spawns an LLM.
  step        [agent=reviewer-summary, prompt="@skill:satelle-step-summary", mandatory=true]

  // Declared scoped reviewers (edge-less, on="<target states>"): always-on gates the
  // workflow itself declares. estimate gates begin-work + close; fmtcheck runs
  // gofmt + go vet on entry to integration (sty_343fe595); unitcheck runs
  // go build + go test on the same entry; intcheck runs
  // `make integration` on entry to release — i.e. on the integration -> release edge,
  // alongside that edge's satelle-integration-review — so integration is a VISIBLE step;
  // cicheck and dogfoodcheck gate entry to done on the released artifact actually
  // being green on GitHub and actually running on this machine.
  // estimate: tags only. Producer = driving session (not planner):
  //   satelle story estimate before in_progress; satelle story actual before done.
  // See @skill:plan and documents/estimate-and-lessons.md (sty_b9ecd5d2).
  // Coded ```check gates name no agent= (engine returns before gateBinding).
  //
  // THE FOUR DEPLOYMENT OBJECTIVES (sty_7a2dc74b). A deployment succeeds only
  // when all four pass, and each maps to exactly ONE named gate so a rejection
  // identifies which objective failed rather than surfacing generically:
  //   1. build + unit test          -> unitcheck    (on integration)
  //   2. integration test, without
  //      disturbing the install     -> intcheck     (on release)
  //   3. push + await GitHub        -> cicheck      (on done)
  //   4. satelle update + test      -> dogfoodcheck (on done)
  // Gates 3 and 4 VERIFY, they never PERFORM: they read CI and the live machine
  // rather than pushing or updating. A reviewer that deploys concentrates
  // deployment risk in the gate path, and an automated restart has already
  // stopped this repo's live service once (sty_f20f3f3b).
  estimate    [prompt="@skill:satelle-estimate-actual-review", on="in_progress,done"]
  fmtcheck    [prompt="@skill:satelle-format-vet-check", on="integration"]
  unitcheck   [prompt="@skill:satelle-build-unit-check", on="integration"]
  intcheck    [prompt="@skill:satelle-integration-check", on="release"]
  cicheck     [prompt="@skill:satelle-ci-published-check", on="done"]
  dogfoodcheck [prompt="@skill:satelle-dogfood-check", on="done"]
  // changelogcheck fails closed on release→done when CHANGELOG.md has no entry
  // for the version on HEAD (sty_f52ba0c3). Same coded-check convention: no agent=.
  changelogcheck [prompt="@skill:satelle-changelog-entry-check", on="done"]
  // design: surface-scoped UI design-system gate (sty_e4359efe / epic:surface-scoped-steps).
  // Enqueued only for surface:ui stories on entry to integration — same spine for all.
  design [agent=reviewer, prompt="@skill:satelle-design-review", on="integration", applies_to="surface:ui"]

  backlog     -> plan         [agent=reviewer, prompt="@skill:satelle-story-intent-review"] // intake gate: a story must pass intent-review to enter plan
  // Multi-reviewer plan gate: plan-review + architecture + integration-coverage,
  // concurrent (parallel=true) — all-must-accept, no short-circuit (sty_4f0a15db).
  // Plan gates use the default [reviewer] (sty_2f9b63b5 retires cross-family agent=reviewer-plan).
  // agent=reviewer is the explicit default form — prompt=@skill: skills require agent= to parse as a gate.
  plan        -> in_progress  [agent=reviewer, prompt="@skill:satelle-story-plan-review,satelle-story-architecture-review,satelle-story-integration-coverage-review", parallel=true]
  // code-ac then scope then workflow-change (CSV); fmtcheck (scoped on=integration)
  // enforces gofmt+vet deterministically (sty_343fe595). scope uses engagement
  // baseline + story diff (sty_814ad29a). workflow-change n/a-fast-accepts when
  // the slice touches no workflow file (sty_9882b8c6).
  in_progress -> integration  [agent=reviewer, prompt="@skill:satelle-code-ac-review,satelle-story-scope-review,satelle-workflow-change-review"]
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
`satelle-code-ac-review`, `satelle-format-vet-check`, `satelle-integration-review`,
`satelle-integration-check`, `satelle-story-release-review`,
`satelle-estimate-actual-review`, `satelle-story-cancel-review`,
`satelle-step-summary`), and the post-release lessons capture (`satelle-lessons`,
run by the ORCHESTRATOR after close via `satelle story retrospect <id>` on the
`[retrospective]` binding — nothing fires on entry) are authored in this
repo's `.satelle/skills` — so there is no dangling `@skill:` reference and a
story drives to a terminal state without a missing-skill block.
`satelle-workflow-change-review` (CSV sibling on `in_progress → integration`)
resolves from the **embedded** substrate (not repo-authored). Reviewer gates
degrade to advisory only if their rubric is genuinely absent. Lessons are
offline corpus (not session-injected).

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
