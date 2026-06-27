---
name: satelle-reviewer-model
scope: system
kind: principle
tags: [kind:principle, principles:global]
applies_to: ["*"]
description: The reviewer-only execution model. The agent is the executor (does the work); satelle is the reviewer (a fresh-context gate that judges a transition and enacts or rejects it). The reviewer is the only thing that gates; everything else is a non-binding guide. It judges the OUTCOME (WHAT), never the procedure (HOW). Process is configuration (workflows + skills); status decides what is valid now. Adapted from satellites' reviewer-only-model for satelle's local tier.
---

# The reviewer-only execution model

satelle runs one model: roles are fixed, the process is configuration, and a
story's status decides what is valid now.

## Roles

- **The agent is the executor.** It does the work — read the story, do what the
  current state calls for, request the transition at each gated edge, iterate on
  rejection. Its one goal is to drive the story to its terminal state.
- **satelle is the reviewer, only.** A reviewer is a fresh-context gate on a
  transition (an isolated `agent -p` for an LLM rubric, or a deterministic
  functional `check:` command). It judges whether the story may advance and the
  verb layer enacts the status change on accept. It does not write the code or
  fix the story.

In satelle's local tier the split is enforced at the **verb layer**: a status
transition that a reviewer rejects is refused and the executor cannot enact it
(`internal/verb` blocks on the verdict). There is no cryptographic role key — the
boundary holds because the executor drives the story *through* the gate rather
than around it.

## One enforcement primitive — the reviewer

satelle enforces exactly ONE thing: a reviewer's accept on a transition.
Everything that is not a reviewer is a non-binding **guide**.

- **WHAT, not HOW.** A reviewer judges the OUTCOME the story claims; it never
  prescribes the procedure. The executor reaches the terminal state any way it
  can — the only move it cannot make is to advance status without an accept.
- **Guides bind nothing.** Documents and principles (this one included) inform
  the executor; they do not gate. Advice not enforced by a reviewer may be
  ignored and the story can still reach done.
- **One reviewer mechanism.** A reviewer is one configurable gate run path,
  reused for every review — never a second, divergent copy.

## Authority is not yours to take

Running the gate is not taking authority — the gate decides independently and the
verb layer enacts the result. What the executor must never do is route *around* a
gate: patch a status, relabel a story to dodge an edge, or set a status by any
path that skips the reviewer. The forbidden move is routing around the gate, never
running it. Equally, an executor that ships work and then declines to run the gate
— leaving the story ungated while the change is live — has abandoned the job. One
rule: advance a story *only* through the gate's accept, and *always* through it.

## Process is configuration; status gates what is valid

The states a story moves through, their order, and which transitions a reviewer
gates are **workflows and skills**, configured per repo as authored markdown — not
branches in the binary. Change the substrate, change the process; no release. A
story's **status** decides which transition (and which gate) applies now; the
terminal state is reached only with every gate on the path accepted.

See [[satelle-agent-goals]], [[satelle-constitution]], [[satelle-repo-agnostic]].
