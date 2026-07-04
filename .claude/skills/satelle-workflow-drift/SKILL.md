---
name: satelle-workflow-drift
description: Audit a satelle repo's active workflow DOT against its agents.toml for DRIFT — the two authored files disagreeing — then FILE an optimise story with the fixes. Catches orphaned bindings (a [name] agent no workflow node allocates), stale binding comments that describe a superseded topology (a step/edge that no longer exists), gate preconditions with no documented producer, model-allocation smells (capability inversion, a high-stakes gate stuck on the shared reviewer model), and node-vs-exit-edge labor that double-runs or does nothing. Use when asked to review/lint the workflow and agents files together, check agents.toml is in sync with the workflow, hunt for stale or dead bindings/comments, pressure-test per-step model choices, or after editing a workflow's topology (moving steps in-loop, renaming/removing nodes). Files a category:substrate story of the mechanical fixes and surfaces the judgment calls as questions; it does not edit or decide them itself.
---

# satelle workflow ↔ agents drift audit

A satelle repo carries its process in two authored files that must **agree**:

- a **workflow** DOT (`.satelle/workflows/*-workflow.md`) — the state graph:
  nodes with an `agent=` role, edges with `reviewer_skill=` gates, and edge-less
  scoped reviewer nodes (`on="<states>"`).
- the **agents layer** (`.satelle/agents.toml`) — one binding per agent:
  `[executor]`, `[reviewer]`, and each named `[<name>]` a node allocates to, plus
  the **comments** that explain what each binding is for.

They drift apart because editing the graph (pulling a step in-loop, renaming or
deleting a node) does **not** touch `agents.toml` — and satelle only *refuses* when
a node names a **missing** binding, never when a binding goes **unused** or its
comment lies. So drift sits there silently, and the stale comments mislead the next
reader — including future-you — into believing steps run a way they no longer do.

This skill finds that drift and **files an optimise story** with the fixes. It
**judges and files**; it never edits `agents.toml`/the workflow itself, and it does
not decide the judgment calls — those go into the story as questions for the operator.

The embedded `satelle-workflow-advisor` skill advises on a *single* workflow's
per-step config quality (allocation, reviewer coverage, grant scoping). This skill
is the complement: **cross-file consistency** between the workflow and `agents.toml`,
plus the narrative drift in comments — and it produces a tracked story, not inline
advice. Defer per-step allocation *quality* to the advisor; own *agreement* here.

## How to run

1. Read the **active** workflow (the `scope: project` one that applies; if unsure,
   `satelle workflow list` — the active project workflow wins over the embedded
   baseline). Extract its DOT block.
2. Read `.satelle/agents.toml`.
3. Build two sets from the DOT:
   - **allocated agents** — every distinct `agent=<value>` across nodes (plus the
     built-ins the graph relies on: `executor`, `reviewer`).
   - **topology facts** — the node names, each node's `agent=`, the edges
     (`a -> b`), and each edge's `reviewer_skill=`; the edge-less scoped reviewers
     and their `on=` targets.
4. Run the five drift checks below. Collect findings.
5. Report (structure at the end), then **file the optimise story**.

## The drift checks

Each finding names the binding/node, what drifted, *why it misleads*, and the fix.

### 1. Orphaned binding — a named agent nothing allocates
For every named `[<name>]` binding (not `executor`/`reviewer`), confirm some node
carries `agent=<name>`. If none does, the binding is **orphaned** — dead config the
workflow never reaches. Why it matters: it reads as an active step but isn't, and a
guardrail may even forbid using it. Fix: delete the binding, **or**, if it's kept as
a documented alternative, gate its comment with "not wired in the current workflow".

### 2. Stale binding comment — describes a topology that no longer exists
Read each binding's **comment** as a set of claims about the graph: "allocated to
the `commit` and `push` steps", "the integration→commit EDGE", "the INTEGRATION
step's named agent". Check each claim against the topology facts. Flag any claim that
names a **node, edge, or `agent=` allocation the DOT does not have**. Why it matters:
a confident, wrong comment is worse than no comment — it asserts a machine that was
superseded (e.g. before commit/push/integration were pulled in-loop). Fix: rewrite
the comment to the current topology, or delete it with the orphaned binding.

### 3. Gate precondition with no documented producer
For each edge-less scoped reviewer (`on="<states>"`) and each `reviewer_skill=` that
requires a **precondition** — a tag, an attachment, an estimate — identify what
produces it and confirm *something* (a node, a binding comment, the rubric) says so.
Flag a required precondition whose **producer is unstated**. Example: an `estimate`
gate on entry to `in_progress` requires an estimate tag, but nothing says whether the
dispatched planner emits it or the in-loop session records it. Why it matters: an
unstated producer is a step nobody owns — it blocks the loop and the next reader
can't tell whose job it is. Fix: state the producer (in the node/comment/rubric).

### 4. Model-allocation pressure-tests
Read each performing node's model (via its binding's `model`) and each gate's model.
Surface — as **questions for the operator**, since these are judgment calls, not bugs:
- **Capability inversion.** A cheaper/weaker model producing an artifact a stronger,
  full-context agent then works *from* (e.g. a fable `plan` the opus in-loop session
  implements). Ask the real intent: is the plan a cheap upfront AC-feasibility check
  (inversion is fine), or scaffolding the smarter agent will ignore or route around
  (inversion buys only review-gate thrash)?
- **Uniform gate model + no per-node override.** If every reviewer shares one
  `[reviewer]` binding's `model`, the highest-stakes gate (the terminal
  `release`/close gate) can't run a stronger model than the cheapest presence gate,
  even though a stateless fresh-context reviewer makes a strong model cheap there.
  Flag that this is a **structural** limit (reviewer nodes can't vary model per gate),
  not just a config value — worth deciding whether reviewer nodes should support a
  per-node model override.

### 5. Node-vs-exit-edge labor — double-run or empty node
For a performing node whose **exit edge** carries a check that re-does the node's
apparent work (e.g. an `integration` node `agent=executor` *and* an `intcheck` that
runs `make integration` on the exit edge), pin down what the node actually *does*
versus what the edge *verifies*. Flag either failure: **double-run** (node runs the
suite, edge re-runs it) or **empty node** (the node does nothing meaningful and the
prose overstates it). Fix: state the division of labor — what the executor does
inside the node vs what the exit gate checks.

## File the optimise story

After the audit, create ONE story capturing the fixes. Because the fixes edit only
`.satelle/` substrate (agents.toml, the workflow), use **`--category substrate`** so
it rides the light substrate workflow (no version bump / release):

```bash
satelle story create \
  --category substrate \
  --title "Reconcile agents.toml with the <workflow> topology (drift audit)" \
  --body "<one line per finding: the drift + why it misleads. THEN a 'Judgment calls' \
section listing the model-allocation questions (§4) verbatim for the operator to \
decide — do NOT pre-answer them.>" \
  --acceptance "1. <mechanical fix — e.g. delete orphaned [commit-agent] and [integrator] \
bindings, or gate their comments 'not wired in the current workflow'>. \
2. <state the estimate producer in the node/comment/rubric>. \
3. <state the integration node's labor vs the intcheck exit gate>. \
4. <if the operator wants it: reviewer per-node model override — but ONLY as an AC if \
they've decided; otherwise leave it in the body as a question>."
```

Rules for the story:
- **ACs are the mechanical, decided fixes** (delete/rewrite drifted bindings and
  comments, state the unstated producer/labor). Each AC must be verifiable by reading
  the resulting file.
- **Judgment calls stay in the body as questions**, never as ACs — the operator
  decides fable-vs-plan intent and whether reviewer nodes need a per-node model
  override; the skill must not smuggle a decision into an acceptance criterion.
- If the audit finds **no drift**, say so and file **nothing** — a clean bill is the
  right outcome, not an empty story.
- Report the story id you filed.

## Report structure

Lead with the verdict, then findings, then the filed story.

```
## Workflow ↔ agents drift — <workflow name>

**Verdict:** <graph health + the headline drift, e.g. "graph is clean; agents.toml
carries 2 orphaned bindings from a superseded topology">

### Drift (mechanical — go into the story's ACs)
- **[orphaned] <binding>** — <claim vs topology>. Fix: <delete / rewrite comment>.
- **[stale-comment] <binding>** — <the wrong claim>. Fix: <current topology>.
- **[unstated-producer] <gate>** — <precondition, no owner>. Fix: <state producer>.
- **[labor] <node>** — <double-run / empty>. Fix: <state node vs edge>.

### Judgment calls (into the story BODY as questions — operator decides)
- **model inversion** — <the question>.
- **uniform gate model** — <the structural gap + the decision>.

### Filed
- `sty_…` — category:substrate, <N> ACs, judgment calls in the body.
```
