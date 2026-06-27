# Reviewer checks — the gates on a story

satelle's quality spine runs an isolated, fresh-context reviewer over each
requested transition. The active workflow (`satelle-baseline-workflow`) names a
reviewer skill per edge; the skill's markdown body rides as the reviewer's system
prompt; the work item and the requested transition go in on stdin; the reviewer
returns one JSON verdict `{"decision":"accept"|"reject","notes":"…"}`. Accept lets
the transition enact; reject blocks it and pushes the notes back to the executor.
Reviewers are **read-only** (`Read,Grep,Glob`) — they judge, never mutate. The
executor never enacts its own transition.

An edge is gated only when the workflow names a reviewer skill **and** that
skill's rubric is installed in the substrate. A named-but-absent rubric is
treated as advisory, so a fresh repo keeps working until the rubrics ship.

## Create gate — `satelle-story-review`

Runs when a draft is created (opt-in per repo via `[review] gate_create`). It
judges the **required structure** only — not whether the work is a good idea:

1. a non-empty, specific **title**;
2. a **clear goal** in the body (a blank or title-restating body fails);
3. at least one numbered, **testable** acceptance criterion.

This is the one opinionated thing satelle enforces; everything else (tags,
priority, category, style) is non-opinionated.

## Two gate kinds: LLM reviewers and functional checks

A gate is either:

- an **LLM reviewer** — the skill's markdown body rides as a fresh-context agent's
  system prompt and the agent returns the verdict (used for judgment: structure,
  intent, acceptance); or
- a **functional check** — the skill's frontmatter names a deterministic
  `check:` command. The gate runs it in the repo root; **exit 0 accepts,
  non-zero rejects** with the command's output tail as notes. No LLM — the command
  is the decision. This is how the integration and deploy gates work.

## Begin-work gate — `satelle-story-intent-review` (backlog → in_progress)

Judges readiness of **intent** before work starts: the title names a concrete
change, the body states a clear goal / what done looks like, and the acceptance
criteria list at least one numbered, testable item. Unclear intent is rejected
with notes; the story stays in backlog.

## In-progress tech-lead review — `satelle-story-code-review` (in_progress → reviewed)

An isolated, **read-only** LLM reviewer acting as a tech lead pre-reviewing the
PR. It reads the modified code in the working tree, judges it against the story's
acceptance criteria, and checks that the integration tests written for the work
actually align with the code — **without executing them** (that is the next gate).
A change with an unmet criterion, wrong code, or a missing/misaligned test is
rejected with specifics.

## Integration gate — `satelle-story-integration-review` (reviewed → integrated)

A **functional check** with a self-contained `check` script embedded in the skill.
It builds the binary and runs the full integration suite — the black-box CLI tests
plus the headless-Chrome browser e2e — and accepts only if **every** test passes.
Any failure rejects the transition with the failing output. An item cannot advance
past integration on a red suite.

## Deploy gate — `satelle-story-deploy-review` (integrated → deployed)

A **functional check** with a self-contained `check` script embedded in the skill.
It deploys the service locally and validates it with a **health check on both
surfaces**: the web UI (`/healthz` returns ok and the project page renders its
tabs) and the CLI (`satelle status`), leaving it running. Local-first — the
service is a local systemd user unit, so the deploy has no production blast radius.

## Close gate — `satelle-story-done-review` (deployed → done)

An isolated, read-only reviewer that **reads the repository** to verify the work.
It works through each numbered acceptance criterion and looks for concrete
evidence it is satisfied (the file exists / contains the change, a test asserts
the behaviour). Unmet criteria are rejected with specifics; the story stays
in_progress.

## Cancel gate — `satelle-story-cancel-review` (any → cancelled)

Named by the baseline workflow to record why an item is abandoned. Until the
rubric ships it is advisory (the cancel enacts directly).

## Summariser — `satelle-step-summary`

Not a gate. After a transition is enacted, this read-only observer produces a
1–3 sentence prose recap of the step, recorded verbatim as a `step_summary`
ledger row.

## Where the rubrics live

The create-structure reviewer and the summariser are **embedded canonical
defaults** shipped in the binary (`internal/config/substrate/skills`). A repo MAY
override them — or add its own gates (like this repo's intent-plan and done
reviewers) — by authoring markdown under `.satelle/skills/`. The binary runs the
gates; the substrate defines them.

See also: `satelle help create-story`.
