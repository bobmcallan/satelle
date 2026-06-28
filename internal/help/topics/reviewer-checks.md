# Reviewer checks — the gates on a story

satelle runs the **actor model** (see the `satelle-actor-model`
principle): a story moves through a graph of **steps**, each run by a **defined
actor**, and the story's **status** decides what is valid now. The agent's goal is
to drive the story to `done`; satelle is the **gatekeeper of status** — a status
advances only through a reviewer's accept, and always through it.

- **executor** — does the work and mutates the tree.
- **reviewer** — is **limited to reviewing**: an isolated, fresh-context judge that
  reads the requested transition and returns one JSON verdict
  `{"decision":"accept"|"reject","notes":"…"}`. It is **read-only** and never
  mutates — a quality-management invariant, enforced by its grant, not by trust.

Each gate is an isolated, fresh-context call: satelle builds the payload (the work
item + the requested transition), spawns a fresh agent with the step's **skill** as
its prompt and a read-only grant, and aggregates the one verdict to gate the status.
satelle does the context selection; the reviewer reads what it needs through its
tools. This applies to **stories and tasks** alike — gating is by category,
kind-agnostic.

## Workflows are authored — YAML or DOT

The active workflow is authored substrate (the embedded `satelle-baseline-workflow`,
or a repo override under `.satelle/workflows`). Its lifecycle may be written two
ways, both parsed by the shared `wfdot`/web parser:

- an inline-YAML `states:`/`transitions:` block (transitions carry `reviewer_skill`); or
- a fenced ```dot graph (node-centric): each node is a step carrying an `actor`,
  a reviewer node names its gate as `prompt="@skill:NAME"`, and the edge **into**
  a reviewer node is the gated transition. `satelle validate` graph-checks a DOT
  workflow (structural soundness + the mandatory spine gate into `done`).

An edge is gated only when the workflow names a reviewer skill **and** that skill's
rubric is installed; a named-but-absent rubric is advisory, so a fresh repo keeps
working until the rubrics ship.

## The actors layer — how a step runs

*What* is injected (the skill + context subset) is satelle's; *how and where* an
actor runs is the **actors layer** (`.satelle/actors.toml`). It binds each actor to
a backend and grant, defaulting to today's behaviour — the executor runs in-loop,
the reviewer runs as an isolated `agent -p` with the read-only `Read,Grep,Glob`
grant. A repo may rebind a backend or grant without touching the workflow; the
read-only limit travels with the binding.

## Two gate kinds: LLM reviewers and functional checks

A gate is either:

- an **LLM reviewer** — the skill's markdown body rides as a fresh-context agent's
  system prompt and the agent returns the verdict (judgment: structure, intent,
  acceptance); or
- a **functional check** — a self-contained ```check script (or a `check:` in
  frontmatter). The gate runs it in the repo root; **exit 0 accepts, non-zero
  rejects** with the output tail as notes. No LLM — the command is the decision.
  Like the commit-push gate, a functional check may run real mechanism.

## Create gate — `satelle-story-review`

Runs when a draft is created (opt-in per repo via `[review] gate_create`). Judges
**required structure** only: a specific title, a clear goal in the body, and at
least one numbered, testable acceptance criterion.

## Begin-work gate — `satelle-story-intent-review` (→ in_progress)

Judges readiness of **intent** before work starts — concrete title, clear goal,
testable criteria. Unclear intent is rejected; the story stays in backlog.

## Commit-push step — `commit-push` (executor) + `satelle-commit-push-review` (gate)

The commit-push **executor** step stages and commits the slice (conventional
message, the story id, no AI attribution), pushes to `main` (trunk-based release),
and watches the GitHub Actions run to conclusion — recording the conclusion and run
URL as evidence. The commit happens **while the story is engaged** (an executor
state), so commits are always tracked. The **commit-push gate**
(`satelle-commit-push-review`, a functional check) then confirms the CI run for the
pushed commit concluded success — evidence the deployment worked — and emits a
PR-style commit-summary document under `.satelle/documents/`.

## Close gate — `satelle-story-done-review` (→ done)

An isolated, read-only reviewer that **reads the repository** to verify each
numbered acceptance criterion against concrete evidence. Unmet criteria are
rejected with specifics. `done` is always terminal (see `satelle-done-is-last`),
and the mandatory close gate is the spine a custom workflow cannot drop.

## Always-on system layer — estimate/actual + cancel

`satelle-estimate-actual-review` runs on every gated transition (after the
workflow-named reviewers) but only governs two edges: entry to `in_progress`
requires a recorded plan estimate, and entry to `done` requires the recorded
actual (`satelle story estimate` / `satelle story actual`).
`satelle-story-cancel-review` records why an item is abandoned.

## Summariser — `satelle-step-summary`

Not a gate. After a transition enacts, this read-only observer produces a 1–3
sentence recap recorded as a `step_summary` ledger row.

## Where the rubrics live

The create-structure reviewer and summariser are **embedded canonical defaults**
(`internal/config/substrate/skills`). A repo MAY override them — or add its own
gates (this repo's commit-push reviewer) — under `.satelle/skills/`. The binary
runs the gates; the substrate defines them.

See also: `satelle help create-story`.
