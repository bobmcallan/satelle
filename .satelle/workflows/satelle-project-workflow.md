---
name: satelle-project-workflow
scope: project
type: workflow
tags: [type:workflow]
applies_to: ["*"]
create_review: satelle-story-create-review
description: This repo's project-scope workflow, authored in DOT (the agent model). A story or task moves backlog → in_progress → integration → commit → push → committed → done, with a cancelled exit. It is node-centric: each node is a step carrying an agent; the executor does the work, reviewers gate entry. Entering integration is gated by satelle-code-ac-review (ACs done + unit and integration tests created); leaving it for commit is gated by satelle-integration-review (the integration tests exercise the change) plus the declared intcheck node (runs make integration). The commit executor step bumps satelle.version + stamps the build date and commits; the push executor step pushes and watches the test run + the version-gated release; the committed reviewer (satelle-push-review) confirms the bump, the green CI, and the published release, and emits a PR-style summary; done is the acceptance gate. There is no deploy state — the push to main IS the release, verified by CI. done stays terminal (satelle-done-is-last); a project workflow takes precedence over the embedded satelle-baseline-workflow.
---

# satelle workflow (project) — the agent model, authored in DOT

> **This is a project workflow** under `.satelle/workflows`, the ACTIVE workflow
> for this repo: a project-scope workflow takes precedence over the binary's
> embedded **system** default `satelle-baseline-workflow`. See the
> `satelle-repo-agnostic` and `satelle-agent-model` principles.

The lifecycle is the **DOT graph** below — read it as the authority; this prose
only orients and must not restate it. Each node is a step carrying an `agent`: an
**executor** does the work and mutates the tree; a **reviewer** node gates *entry*
via its `prompt="@skill:NAME"` (read-only — it judges, never mutates). Status
advances only through a reviewer's accept.

Two things the edges don't show. **There is no deploy state** — pushing to `main`
IS the release, verified by CI. And the **always-on gates are declared, not
injected**: the edge-less reviewer nodes `estimate` (`on="in_progress,done"`) and
`intcheck` (`on="commit"`) run on the transitions their `on=` names, so the
DOT is the sole gating authority — no skill tag adds a gate the workflow never
declared (sty_ca9f675f). `estimate` requires a plan estimate entering
`in_progress` and an actual entering `done`; `intcheck` runs `make integration`
entering `commit`. The `commit` step bumps the version + commits; the `push` step
pushes and watches CI + the version-gated release. The `committed -> in_progress`
edge is recovery: a `done` reject returns the story to work to fix and re-traverse,
never bypass.

```dot
digraph satelle_workflow {
  graph [goal="Drive a story to done — every gate accepted, the commit-push release verified by CI", vars="story, repo_root"]
  rankdir=LR

  backlog     [shape=Mdiamond]
  in_progress [agent=executor]
  integration [agent=executor]
  commit      [agent=commit-agent, prompt="@skill:commit"]
  push        [agent=commit-agent, prompt="@skill:push"]
  committed   [agent=reviewer, prompt="@skill:satelle-push-review"]
  done        [shape=Msquare, agent=reviewer, prompt="@skill:satelle-story-done-review"]
  cancelled   [agent=reviewer, prompt="@skill:satelle-story-cancel-review"]
  
  // step opts this workflow into per-transition step summaries (sty_9a139c78):
  // an edge-less declaration, mandatory so a summary failure is surfaced.
  step        [agent=reviewer, prompt="@skill:satelle-step-summary", mandatory=true]

  // Declared scoped reviewers (edge-less, on="<target states>"): always-on gates the
  // workflow itself declares, so the DOT is the sole gating authority — no skill-tag
  // scan injects an undeclared gate (sty_ca9f675f). estimate gates begin-work + close;
  // intcheck runs `make integration` entering commit.
  estimate    [agent=reviewer, prompt="@skill:satelle-estimate-actual-review", on="in_progress,done"]
  intcheck    [agent=reviewer, prompt="@skill:satelle-integration-check", on="commit"]

  backlog -> in_progress
  in_progress -> integration [reviewer_skill="satelle-code-ac-review"]
  integration -> commit [reviewer_skill="satelle-integration-review"]
  commit -> push -> committed -> done

  committed   -> in_progress  // recovery: a done-review reject returns to work

  backlog     -> cancelled
  in_progress -> cancelled
  integration -> cancelled
  commit      -> cancelled
  push        -> cancelled
}
```

## Skill resolution

Every gate/skill this workflow names resolves through the doc-index, **project
scope (`.satelle/skills`) layered over the embedded system defaults**. The
executor steps `commit` + `push` and the reviewer gates (`satelle-code-ac-review`,
`satelle-integration-review`, `satelle-push-review`,
`satelle-story-intent-review`, `satelle-story-done-review`,
`satelle-story-cancel-review`) are authored in this repo's `.satelle/skills` — so
there is no dangling `@skill:`/`reviewer_skill` reference and a story drives to a
terminal state without a missing-skill block. The engagement guard
(`sty_09ef53d6`) deterministically resolves the **executor-step** skills on the
path to done before work begins; reviewer gates degrade to advisory only if their
rubric is genuinely absent. The embedded **baseline** workflow remains the
order-zero fallback: it names the same gate reviewers but, being repo-agnostic,
relies on a repo authoring them — a fresh repo's baseline still completes (its
absent gates degrade to advisory) until the repo adds its own.

## Environment

```yaml
guardrails:
  always:
    - Drive an engaged item to a terminal state (done or cancelled) — don't leave work open indefinitely.
    - Give a story/task numbered acceptance criteria before starting, and satisfy them before moving to done.
    - Bump the version + commit at the commit step, then push at the push step (performing states, run by the commit-agent or in-loop); the committed gate verifies the bump, CI, and the published release before close.
  ask_first: []
  never:
    - Place any state after done — done is always the terminal success state.
    - Self-enact a gated edge the reviewer has not accepted.
    - Mark an item done with unmet acceptance criteria, or advance committed with a failing CI run.
```
