---
name: satelle-actor-model
scope: system
type: principle
tags: [type:principle, principles:global]
applies_to: ["*"]
description: The actor execution model (supersedes the reviewer-only model). A workflow is a graph of steps, each run by a DEFINED actor with a bounded grant — the executor does the work (mutates the tree), the reviewer is LIMITED to reviewing (read-only, judges the OUTCOME not the procedure, returns a verdict, never mutates). Each actor invocation is isolated: satelle spawns a fresh-context agent with the step's skill as the prompt over a payload it builds (the work item + the transition) and aggregates the structured return. satelle stays the status gatekeeper — a reviewer's accept is the only thing that advances a gated status. How and where an actor runs (in-loop, isolated `agent -p`, or another harness) is the actors layer. The model is structural — agents gate agents — not a claim about recursive context decomposition.
---

# The actor execution model

satelle runs one model: a story moves through a graph of **steps**, each step is
run by a **defined actor**, and the story's **status** decides what is valid now.
The agent's one goal is to drive the story to its terminal state; satelle is the
gatekeeper of status.

## Actors are defined, and their grant is bounded

A step names its **actor**, and every actor is a first-class, *defined* role with
a bounded grant — not an unenforced guide. There are two:

- **executor** — does the work. It reads the story, does what the step calls for,
  mutates the working tree, and requests the next status. Full tool grant.
- **reviewer** — is **limited to reviewing**. It is a read-only judge: it reads
  the outcome the story claims, decides accept/reject against the step's rubric,
  and returns a structured verdict. It **never mutates** — not the code, not the
  story, not the status. This review-only limit is a **quality-management
  invariant**, enforced by the reviewer's grant, never by trusting the actor.

The old reviewer-only model made only the *reviewer* a first-class enforced
thing and left the *executor* an unenforced guide — so nothing bounded each actor
at execution time, and an executor that drifted into the reviewer's lane (acting
where it should only judge) broke the model. Defining **both** actors, each with
its grant, is the fix.

## Two ways an actor runs — in-loop, or an isolated invocation

The **orchestrator** is the default driving session — the agent the operator runs
(this session). It IS the in-loop **executor**: by default the executor is not a
separate process but the orchestrator itself, a FULL session with the normal
context, principles, and skills available through the substrate (the `satelle`
CLI and `.satelle/`). At an executor step it reads the step's rubric from
`.satelle/skills` and follows it; the step's `@skill:NAME` is a **declaration** of
which rubric to follow, NOT an invocation of any agent CLI's native skill
mechanism (see below).

A **reviewer**, by contrast, runs as an **isolated, fresh-context invocation**:
satelle spawns a fresh agent with the step's **skill as its system prompt**, a
**payload satelle builds** (the work item plus the requested transition — not the
whole repo), and the actor's bound read-only grant; it returns a structured
`{decision, notes}` satelle **aggregates** to gate status. `internal/reviewer`
spawns this isolated `agent -p`. **satelle does the context selection** (the
payload); the reviewer reads what it needs under its grant. There is no shared
state — each gate is a clean room. Any actor OTHER than the in-loop executor
(the reviewer, or an optional extra agent a repo binds) runs this isolated way.

## @skill: is an agent-agnostic declaration — the process never locks to a CLI

A step's `@skill:NAME` (a node's `prompt="@skill:…"` or an edge's
`reviewer_skill`) names the rubric that governs the step. It is **satelle's own
declaration**, resolved against `.satelle/skills` — it is **not** any particular
agent CLI's native skill call (it is not Claude's `SKILL()`; satelle uses no
`.claude/skills`). The in-loop executor **reads** that rubric from the substrate
and follows it; a reviewer receives the rubric **as its system prompt**. Either
way the rubric is plain authored markdown delivered through satelle, so the same
workflow drives any configured agent CLI — the process is never coded to one
agent's skill mechanism. And skills only **guide** the executor; the **gates
enforce** the outcome — an executor that ignores its rubric is still judged by the
reviewer on the edge, so correctness never depends on a CLI honouring a skill.

The model is **structural**: agents gate agents — the executor's progress
advances only when satelle invokes an isolated reviewer to judge it. It is *not*
a claim that an actor recursively decomposes context; the context a reviewer sees
is the payload satelle hands it plus what it reads under its grant. A reviewer
reading more is still read-only.

## The actors layer binds how a step runs

*What* is injected (the skill + the context subset) is satelle's; *how and where*
an actor runs is the **actors layer** — a binding from each defined actor to a
backend and its grant. By default it is today's behaviour: the executor runs
in-loop (the driving agent), the reviewer runs as an isolated `agent -p` with a
read-only tool grant. A repo may rebind — run the executor as `agent -p`, or point
an actor at another harness entirely — without touching the workflow. The grant
travels with the binding, so the reviewer's read-only limit holds whatever the
backend.

## satelle gates status; the accept is the only advance

satelle enforces exactly one thing: a story's **status** advances only through a
reviewer's **accept** on the step that guards it, and *always* through it. The
forbidden move is routing *around* a gate — patching a status, relabelling a story
to dodge a step, or reaching a status by any path that skips the reviewer. Running
the gate is never taking authority; the gate decides independently and the verb
layer enacts the result. An executor that ships work and then declines to run the
gate has abandoned the job just as surely as one that routes around it.

## Process is configuration; status gates what is valid

The steps a story moves through, their order, each step's actor and skill, and
which steps gate status are **workflows and skills** — authored substrate (the
workflow's step graph and the skills it names), configured per repo, not branches
in the binary. Change the substrate, change the process; no release. A story's
**status** decides which step (and which gate) applies now; the terminal state is
reached only with every gate on the path accepted.

See [[satelle-agent-goals]], [[satelle-done-is-last]], [[satelle-configuration-over-code]], [[satelle-constitution]].
