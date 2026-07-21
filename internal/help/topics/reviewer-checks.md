# Reviewer checks — the gates on a story

satelle runs the **agent model** (see the `satelle-agent-model`
principle): a story moves through a graph of **steps**, each run by a **defined
agent role**, and the story's **status** decides what is valid now. The agent's goal is
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

The active workflow is authored substrate (the default set `satelle init` seeds
under `.satelle/workflows`, a repo's own workflow, or the embedded
`satelle-baseline-workflow` as the order-zero fallback). Its lifecycle may be
written two ways, both parsed by the shared `wfdot`/web parser:

- an inline-YAML `states:`/`transitions:` block (transitions carry `reviewer_skill`); or
- a fenced ```dot graph (node-centric): each node is a step carrying an `agent`,
  a reviewer node names its gate as `prompt="@skill:NAME"`, and the edge **into**
  a reviewer node is the gated transition.

The per-noun `satelle <noun> validate` runs a DETERMINISTIC structure check on
every authored doc — frontmatter (OKF `type`), naming, a usable definition, and
for a workflow the graph (connected, a terminal `done`, a `backlog` start,
resolvable executor skills). The structure check is code, not an LLM rubric, so
it is harness-independent and never flaky. The done gate is **not** mandated —
it is whatever the workflow declares (the author's choice).

An edge is gated only when the workflow names a reviewer skill **and** that skill's
rubric is installed; a named-but-absent rubric is advisory, so a fresh repo keeps
working until the rubrics ship.

## The agents layer — how a step runs

*What* is injected (the skill + context subset) is satelle's; *how and where* an
agent role runs is the **agents layer** (`.satelle/agents.toml`). It binds each agent role to
a backend and grant, defaulting to today's behaviour — the executor runs in-loop,
the reviewer runs as an isolated `agent -p` with the read-only `Read,Grep,Glob`
grant. A repo may rebind a backend or grant without touching the workflow; the
read-only limit travels with the binding.

## Engagement baseline and `satelle story diff` (scope gates)

On first entry into a performing/engaging state, satelle ledgers an
`engagement_baseline` row (git HEAD + dirty flag). Scope judges **enumerate**
via:

```
satelle story diff <id>
# or from a functional check (payload on stdin, no argv id):
satelle story diff   # reads story.id from {story, from, to} on stdin
```

Output is JSON: `files` (sorted, includes untracked), `stat`, optional `patch`.
**Report only** — no pass/fail. The gate skill decides. Missing baseline → clear
error (pre-feature stories degrade gracefully).

## Two gate kinds: LLM reviewers and functional checks

A gate is either:

- an **LLM reviewer** — the skill's markdown body rides as a fresh-context agent's
  system prompt and the agent returns the verdict (judgment: structure, intent,
  acceptance); or
- a **functional check** — a self-contained ```check script (or a `check:` in
  frontmatter). The gate runs it in the repo root; **exit 0 accepts, non-zero
  rejects** with the output tail as notes. No LLM — the command is the decision.
  Like the push gate, a functional check may run real mechanism.

## Create gate — deterministic story structure (code)

When a draft is created (opt-in per repo via `[review] gate_create`), satelle
checks **required structure** deterministically in code (no LLM): a specific
title, a clear goal in the body, and at least one numbered, testable acceptance
criterion. The structure reviewers for skills/workflows/principles are likewise
deterministic code (`internal/structure`), not LLM rubrics — conformance is
mechanical, so a swapped harness can never change what "valid" means.

## Begin-work gate — `satelle-story-intent-review` (→ in_progress)

Judges readiness of **intent** before work starts — concrete title, clear goal,
testable criteria. Unclear intent is rejected; the story stays in backlog.

## Release step — `release` (in-loop executor)

One in-loop **executor** step (the driving session, not a dispatched sub-process).
It formats and stages the slice, **bumps `satelle.version` (patch) and stamps
`satelle.build` in `.version`** — mandatory, because `.version` is the single
source the release tag and build identity derive from — makes a conventional
commit ending in the story id (no AI attribution), and pushes to `main`
(trunk-based release). Pushing triggers the GitHub Actions `test` run and, on its
success, the version-gated `release` run that publishes `v<version>`. Rather than
block watching both runs, the step refreshes the local service during the CI
window and then **records** the `test` + `release` run URLs, their conclusions, and
the published tag as a PR-style summary **with the story** — an attachment via
`satelle story attach … --file` (stored on the home-keyed runtime plane, readable
via `satelle story docs <id>`). The `satelle-story-release-review` gate is the authority
on "CI is green": it judges that recorded evidence and rejects a failing, absent,
or unconcluded run.

## Close gate — `satelle-story-done-review` (→ done)

An isolated, read-only reviewer that **reads the repository** to verify each
numbered acceptance criterion against concrete evidence. Unmet criteria are
rejected with specifics. `done` is always terminal (see `satelle-done-is-last`).
The close gate is **declared by the workflow**, not mandated by the binary — a
workflow may name it, name another, or drop it: if the user breaks their own
process, so be it. The reviewer's grant is read-only (`Read,Grep,Glob`); it reads
the substrate it reasons about as markdown under `.satelle/` (no shell, no CLI).

## Declared scoped gates — estimate/actual + integration check

Always-on gates are **declared in the workflow DOT**, not injected by a skill tag
— the DOT is the sole gating authority (no hidden `reviewer:always` layer). A
reviewer node carries an `on="<states>"` (or `on="*"`) attribute and runs on the
transitions into those target states, after the edge-named reviewers.
`satelle-estimate-actual-review` (`on="in_progress,done"`) requires a recorded plan
estimate entering `in_progress` and the recorded actual entering `done`
(`satelle story estimate` / `satelle story actual`); `satelle-integration-check`
(`on="commit"`) runs `make integration` before a commit. An edge may also name
multiple reviewers directly (`prompt="@skill:a,b"`; the legacy `reviewer_skill="a,b"`
attribute still parses). `satelle-story-cancel-review`
records why an item is abandoned.

## Step summary — `satelle-step-summary` (transparent, opt-in)

Not a gate. The step summary is **declared by the workflow**, not a hidden
always-on behaviour: a workflow opts in by declaring an edge-less `step` node
(`prompt="@skill:satelle-step-summary"`), optionally `mandatory=true`. Where
declared, after each transition this read-only observer records a 1–3 sentence
`step_summary` ledger row; a `mandatory` summary failure is surfaced on the
ledger rather than swallowed. A workflow without the node records no summaries.

## Where the rubrics live

The summariser is an **embedded canonical default** (`internal/config/substrate/
skills`) and is materialised into `.satelle/skills` by `satelle init`. The
deterministic structure checks (skills/workflows/principles/story drafts) are
**code** (`internal/structure`), not rubrics. A repo MAY override a materialised
skill — or add its own gates (this repo's push reviewer) — under
`.satelle/skills/`. The binary runs the gates; the substrate declares them.

See also: `satelle help create-story`.
