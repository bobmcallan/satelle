---
name: satelle-recursive-actor-model
scope: system
kind: principle
tags: [kind:principle, principles:global]
applies_to: ["*"]
description: The recursive-actor execution model (supersedes the reviewer-only model). A workflow is a graph of steps, each run by a DEFINED actor with a bounded grant — the executor does the work (mutates the tree), the reviewer is LIMITED to reviewing (read-only, judges the OUTCOME not the procedure, returns a verdict, never mutates). Every actor invocation is a recursive language-model call satelle hosts: it injects the step's skill as the prompt over a transformed context subset and aggregates the structured return. satelle stays the status gatekeeper — a reviewer's accept is the only thing that advances a gated status. How and where an actor runs (in-loop, isolated `agent -p`, or another harness) is the actors layer; the model is satelle's own, modelled on recursive language models (RLM), not an imported engine.
---

# The recursive-actor execution model

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

## Every actor invocation is a recursive call satelle hosts

Roles are bounded; *execution* is recursive. Each actor invocation is a
recursive language-model call satelle hosts: it injects the step's **skill** as
the prompt over a **transformed context subset** (the work item plus just the
slice the step needs, not the whole repo), runs it through the actor's bound
backend, and **aggregates the structured return**. satelle already does this for
reviewers — `internal/reviewer` spawns an isolated `agent -p` over a transformed
payload and parses one `{decision, notes}`, a depth-1 recursion. The
recursive-actor model names that primitive and extends it to every actor.

Recursion lets an actor manage **unbounded context** — peek, grep, partition, map
over a large input set, spawn its own sub-calls — the recursive-language-model
(RLM) pattern. Decomposition is the actor's freedom; it **never widens the
actor's grant**. A reviewer that recurses to read more is still read-only.

This is satelle's **own** model, written in satelle's idiom (modelled on RLM, not
an imported engine), so the recursion, depth, and tracing are mechanism the binary
owns.

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
