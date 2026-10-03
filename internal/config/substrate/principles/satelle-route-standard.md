---
name: satelle-route-standard
type: principle
tags: [type:principle]
applies_to: ["*"]
description: The rules an author holds while writing a lifecycle — done.toml declares the obligations, step.toml says what discharges each, and the BINARY derives the topology. THE single reference other workflow tools cite. Grammar, keys and samples are in `satelle help workflows`.
---

# A lifecycle is a derived route

A satelle lifecycle is **two authored TOML files** under `.satelle/workflows/` —
`done.toml` (what DONE means, per category) and `step.toml` (what discharges
each obligation) — and the graph between their states is **derived by the
binary**, never authored.

The key-by-key grammar, the file samples, park/cancel/recover, `for`, scoped
obligations and the shared-catalogue rules are in `satelle help workflows` and
`satelle help workflow-convert`. This principle states only the rules an author
must hold in mind; run `satelle workflow validate` after an edit to see what it
did to the route.

## An obligation names a step by its TABLE KEY

The usual mistake. A step's `status` is the **stage name** an item holds there,
and several steps deliberately share one; identity is the table key alone. An
obligation in done.toml names a step's key, never its status.

**An always-on `[[gate]]`'s `on` is the one exception — it matches STATUSES**, not
step keys. An `on` written as a step key silently never fires; `["*"]` fires on
every step.

## The binary owns TOPOLOGY; the author owns OBLIGATION

An author says what must be true before work closes (done.toml) and what
discharges it (step.toml). The binary sorts the steps and synthesises cancel from
every non-terminal step, park from anywhere, backward movement and park → cancel.

Writing those edges as steps is the most common authoring mistake. If you find
yourself declaring a `cancelled` or `blocked` step table, delete it — done.toml's
`cancel` and `park` keys are where those belong.

**Resume is not an edge.** The engine stores the origin status when an item parks
and enforces resume only to that origin, so parking from one step cannot wormhole
to another.

## A step names an AGENT, never a harness

A step's `agent` and a gate's `agent` name the `.satelle/workflows/agents.toml`
binding that runs it. The agents layer owns harness, tools, model and effort —
the route owns *who*, never *how*. To review a step on a different model, define
a second `role = "reviewer"` binding and name it as `reviewer_agent`
(`satelle help agent-dispatch`).

A step with no `agent` is performed in-loop by the orchestrating session; a
reviewer with no `reviewer_agent` runs under `[reviewer]`.

## Gates belong to the step they ADMIT

A gate is not a property of an edge. Every reviewer that must pass before work
enters a step is that step's `reviewers = [...]`. An always-on `[[gate]]` is for
a gate that genuinely fires on entry to several steps; authoring a
gate-specific check as a single-step `[[gate]]` is the common misuse
(`satelle help workflows`).

## Coded-check gates name no agent

A skill whose body carries a coded `check` fence is a **functional check**: the
script *is* the decision. The engine runs it and returns before any binding
lookup, tools grant, role check or agent process. Naming an agent on that path is
inert and misleading — it reads as a dispatch that never happens.

**Convention:** a coded-check gate names no `agent`. Then every `agent` on a gate
means a real LLM dispatch, readable at a glance. If the skill later stops being a
coded check, the omitted agent degrades to `[reviewer]` rather than erroring.

## Scoped work and shared catalogues

Tag-scoped obligations and gates (`tag_obligation`, `applies_to`) match **tags**
only — never category, never kind. A story with two matching tags picks up
**both** scoped items: a plain filter, with no override and no tie-break.

One step catalogue serves every lane, so an always-on `[[gate]]` needs `for`.
`for = ["*"]` means the *wildcard category table*, not "everything": a gate
scoped that way never fires on a lane that has a category table of its own.

See [[satelle-agent-model]].
