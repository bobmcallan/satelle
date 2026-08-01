# Workflows — how a story's lifecycle is chosen

A **workflow** is a story's lifecycle: the states it moves through and the
reviewer that gates each edge, authored in DOT under `.satelle/workflows`. satelle
does not hardcode a lifecycle — the operator authors it as substrate, and satelle
enforces it.

## The ideal: one governing workflow per story type

A workflow declares, in its frontmatter, **what it governs** and **its purpose**:

```yaml
applies_to: ["feature"]      # the story categories this workflow governs ("*" = any)
description: ...             # what this lifecycle is for — read by the agent
create_review: my-rubric    # (optional) the content/alignment reviewer for `story create`
```

The ideal is **one workflow per category**. Selection is then unambiguous and
deterministic.

## Multiple candidates → the agent chooses by content

A repo MAY have more than one workflow that could apply (e.g. a category-specific
one and a wildcard). satelle resolves a single **active** workflow by precedence —
a category-specific match beats a wildcard, and a repo workflow beats the embedded
default. To see the candidates and their purpose for a story's category:

```
satelle workflow list --category <category>
```

The head of that list is the active choice. When several genuinely fit, the agent
picks by the story's content and **records the choice** by adding a
`workflow:<name>` tag at create (otherwise satelle stamps the resolved active one
automatically).

## The choice is stamped and stable

At create, the governing workflow is **stamped** on the story — a `workflow:<name>`
tag plus a `workflow_stamped` ledger entry. Every gate thereafter reads the
**stamped** workflow, not a freshly re-derived one, so a story's lifecycle is
fixed once it begins (deterministic after create).

## Avoiding misconfiguration

Flexibility is not a licence to over-configure. `satelle workflow validate` flags
inconsistencies the operator should fix, and the agent should advise on them:

- **Ambiguous `applies_to`** — two repo workflows that claim the same category (or
  the wildcard) at the same precedence, so the tiebreak is arbitrary.
- **An unresolved reviewer skill** — a workflow names a gate (an `@skill:NAME`
  node or edge, or the legacy `reviewer_skill=` attribute) that does not resolve
  in the substrate.

Run `satelle workflow validate` to surface these before they bite. That command
also prints each gate/node's **effective model** (binding model, or a DOT
`satelle agent validate` prints the same surface under its grant listing.

gate/step while keeping the shared `[reviewer]` (or named) harness — see
`satelle help agent-dispatch` and the satelle-dot-standard principle.

## Binding a reviewer: edge CSV vs scoped on=

How a reviewer is **bound** to the graph is as important as which skill it runs.
Two forms exist; choosing wrong changes both the rendered flow and when the gate
fires.

### Edge CSV — gate-specific reviewers (prefer this)

Bind a reviewer to **exactly one transition** on the edge:

```dot
in_progress -> integration [agent=reviewer, prompt="@skill:satelle-code-ac-review"]
// multiple reviewers on one edge — list order = execution order:
in_progress -> done [agent=reviewer, prompt="@skill:satelle-workflow-change-review,satelle-story-done-review"]
```

- **List order = execution order** (and ledger order). By default reviewers run
  **sequentially**, **all-must-accept**, with **first-reject short-circuit**
  (later reviewers are not invoked once one rejects).
- **Parallel opt-in** (per edge): set `parallel=true` (cap 4) or `parallel=N`
  (N≥1) on the edge to run that edge's reviewer list **concurrently** with no
  short-circuit — every reviewer still runs even if one rejects, so a rejected
  round spends tokens on every reviewer. That is why parallel is **per-gate
  opt-in**, not the default. Aggregation stays all-must-accept in the binary;
  multi-reject refuse messages name every rejecting reviewer.
- **Edge wins:** when an edge carries an explicit `prompt="@skill:…"`, the
  target node's own `prompt` is **ignored for that edge**. If you add a CSV into
  a node that already had a gate (e.g. `done`), **include the prior gate skill
  in the CSV** or that gate silently drops.

### Scoped on= nodes — multi-state / always-on only

An edge-less reviewer node with `on="s1,s2"` (or `on="*"`) is an **always-on**
gate for every inbound edge into those states:

```dot
estimate [prompt="@skill:satelle-estimate-actual-review", on="in_progress,done"]
step     [agent=reviewer, prompt="@skill:satelle-step-summary", mandatory=true]
```

Use scoped `on=` **only** when the gate genuinely belongs on every entry into
the state (estimate at begin-work and close; step summaries; similar always-on
checks). Do **not** author a gate-specific check as `on="in_progress"` — that is
the common misuse.

### The on= over-fire trap

`on="in_progress"` matches **every** transition into `in_progress`, including
rework/recovery edges (`integration → in_progress`, `release → in_progress`).
Consequences:

1. The rendered graph hangs the gate off the **state** box, so the flow misreads
   as "always when entering in_progress" rather than "on this one transition".
2. On every fix-loop re-entry the gate re-fires — an extra reviewer invocation
   per rework cycle.

`satelle workflow validate` **WARNs** (non-fatal) when a **single-state** scoped
reviewer sits on a state that has multiple inbound edges including a rework/loop
edge: consider binding it to the edge instead. Multi-state `on=` and `on="*"` are
not warned — those are the documented always-on patterns.

See the embedded `satelle-workflow-change-review` gate (bound on implementation
exit edges in the baseline and project workflows) for a content judgment of
workflow edits; structural validate stays PASS/FAIL only.

## Four things a workflow governs — don't confuse them

A workflow file carries four distinct kinds of governance. They differ in WHERE
they are declared, WHEN they fire, and WHO decides:

| | Declared in | Fires | Decided by |
| --- | --- | --- | --- |
| **Transition nodes / edges** | the fenced `dot` block | on a status change | a reviewer skill's verdict (or a coded ```check) |
| **Lifecycle hooks** | frontmatter (`hooks:` / the `create_review:` shorthand) | outside the status graph — at story creation today | a reviewer skill's verdict |
| **Deterministic structure checks** | nowhere — they are the binary's contract | always, before anything else | code (`internal/structure`); no LLM, never flaky |
| **Agent judgments** | a reviewer skill's rubric | wherever a gate or hook names it | an isolated agent returning accept/reject |

**Transition gates** are the status graph. They can only judge a move from one
state to another, so anything that happens off the graph is out of their reach.

**Lifecycle hooks** exist for exactly that gap. Story creation has no `from`
state — the item does not exist yet — so it cannot be an edge. A hook declares
the operation, the skill that judges it, and the logical agent that runs the
skill:

```yaml
hooks:
  - operation: create_review
    skill: my-create-review
    agent: strict-reviewer     # optional; defaults to reviewer
```

The scalar `create_review: my-create-review` is the documented shorthand for the
same thing with the default agent. A hook declares **who** runs a skill, never
**how**: model, effort, command, transport and tool grant stay in
`.satelle/agents.toml`. `satelle workflow show <name>` prints each hook's full
resolved allocation; `satelle agent validate` refuses one whose agent is
missing, is not `role = "reviewer"`, or is `command = "in-loop"`.

**Deterministic structure checks** are not configuration at all — a story needs
a clear goal and at least one numbered acceptance criterion, judged by code so
the result is harness-independent and identical every run. A structural failure
pre-empts: no hook or gate reviewer is reached on a malformed draft.

**Agent judgments** are the rubrics themselves. A gate or hook says *which*
skill judges; the skill says *what* the judgment is. That split is why adding a
criterion is a markdown edit rather than a release.

## Beyond validate: the semantic review

`workflow validate` is deterministic structure only (plus the advisory over-fire
WARNING above). The judgment it deliberately does not make — is each performing
step's `agent=` allocation (and its binding's model) deliberate; does each
performing step carry a reviewer gate on its exit edge; is a dispatched
binding's grant scoped to its step; is a dispatched implementation step
self-sufficient (an isolated agent sees only the item and its rubric, never the
conversation); is **binding form** (edge CSV vs scoped on=) correct for each
gate — is the **agent's** review, guided by the embedded
`satelle-workflow-advisor` skill (`satelle doc get skills
satelle-workflow-advisor`). Its findings are ADVICE to the operator; the coded
structural check stays the only hard rule.

For the full contract a dispatched (`agent=<name>`) step runs under — how it is
briefed, how it pulls the story/documents/ledger by id, what makes it
self-sufficient, and why its EXIT edge must carry the review — see
`satelle help agent-dispatch`.

## Reading a story's route — `satelle story route <id>`

A workflow file answers "what is the lifecycle". It does **not** answer "what is
THIS story's lifecycle" — which tag-scoped gates are on for it, where it is now,
and why each gate it has already passed decided as it did.

`satelle story route <id>` is that answer, as one artifact:

- the **plan half** — the ordered steps between the story and done, each with the
  obligation it discharges, who performs it under which rubrics, and the
  reviewers gating entry, marked when a gate is present only because the story
  carries a tag (and marked `skipped` when it is absent for want of one);
- the **outcome half** — appended as each step resolves: every reviewer's
  verdict, its reasoning, and a pointer to the full output on the ledger.

They are deliberately the **same document**. A route read separately from the
verdicts is two things that drift.

The route renders live before a story has moved, so it is answerable from
backlog, and it never requires opening a workflow file. That matters more as the
graph stops being authored: a derived route has no file to open, and refusals
carry the same weight — an engine refusal names the **rule** that fired, why it
fired on this story, and the legal moves it leaves open.

A gated edge names its binding with `agent=<name>` (default `[reviewer]`). Models live in agents.toml only; DOT `model=` is superseded.
