---
name: satelle-route-standard
type: principle
tags: [type:principle]
applies_to: ["*"]
description: Canonical authored form of a lifecycle — done.md declares the obligations per category, step.md says what discharges each, and the BINARY derives the topology. THE single reference other workflow tools cite. Full conversion table in `satelle help workflow-convert`.
---

# A lifecycle is a derived route

A satelle lifecycle is **two authored files** under `.satelle/workflows/`, and
the graph between their states is **derived by the binary** — never authored.

| File | Declares |
| --- | --- |
| `done.md` | What DONE means: one `## <category>` section per category, an ordered list of obligations, plus the park and cancel states |
| `step.md` | What discharges each obligation: one `## <name>` step per step, one `## gate <skill>` per always-on gate |

`## *` in done.md governs any category with no section of its own. Neither file
may declare `applies_to` — done.md's sections are the selector, and a second one
would be a second precedence rule.

The full key-by-key reference, with worked examples, is `satelle help
workflow-convert`. This principle states only the rules an author must hold in
mind while writing.

## The binary owns TOPOLOGY; the author owns OBLIGATION

An author says what must be true before work closes and what discharges it. The
binary sorts the steps (a topological sort of `requires` / `provides`) and
synthesises the shape: cancel from every non-terminal step, park from anywhere,
backward movement, park → cancel.

Writing those edges as steps is the most common authoring mistake. If you find
yourself declaring a `cancelled` or `blocked` step, delete it — `done.md`'s
`cancel:` and `park:` lines are where those belong.

## A step names an AGENT, never a harness

A step's `agent:` and a gate's `agent:` name the `.satelle/workflows/agents.toml` binding
that runs it. The agents layer owns harness, tools, model and effort — the route
owns *who*, never *how*. To review a step on a different model, define a second
`role = "reviewer"` binding and allocate it by name:

```toml
[reviewer-deep]
role    = "reviewer"
command = "claude -p --output-format json --disallowedTools Write,Edit,NotebookEdit --append-system-prompt {system} --allowedTools {tools} --model {model} --effort {effort}"
tools   = "Read,Grep,Glob,Bash(satelle:*)"
model   = "opus"
```

A step with no `agent:` is performed in-loop by the orchestrating session. A
reviewer with no `reviewer_agent:` runs under `[reviewer]`.

## Gates belong to the step they ADMIT

A gate is not a property of an edge. Every reviewer that must pass before work
enters a step is that step's `reviewers:`, and `reviewer_agent:` names the
binding that runs them. `parallel: 0` makes a multi-reviewer gate sequential —
unset, two or more reviewers run concurrently.

An always-on gate — one that fires on entry to several steps rather than one —
is a `## gate <skill>` section with `on:` naming those steps.

## Coded-check gates name no agent

A skill whose body carries a coded `check` fence is a **functional check**: the
script *is* the decision. The engine runs it and returns before any binding
lookup, tools grant, role check or agent process. Naming an agent on that path
is inert and misleading — it reads as a dispatch that never happens.

**Convention:** a coded-check gate names no `agent:`. Then every `agent:` on a
gate means a real LLM dispatch, readable at a glance. If the skill later stops
being a coded check, the omitted agent degrades to `[reviewer]` rather than
erroring.

## Tag-scoped obligations and gates

Two forms scope work to the stories that need it, both matching **tags** only —
never category, never kind:

- `+ <tag> <obligation>` in done.md appends an obligation when the story carries
  the tag, so a `surface:ui` story acquires a design obligation the others do not
  have.
- `applies_to: <tag>` on a step or a gate enqueues it only for a story holding a
  matching tag.

A story with two matching tags picks up **both** scoped items — a plain filter,
with no override and no tie-break.

## Gates in a shared catalogue need `for:`

One step catalogue serves every lane. An always-on gate declares `for:` — the
categories whose route it belongs to — or it fires on every lane, including ones
with no release to verify.

## Park, cancel and recover

`park:` and `cancel:` are `<state> @<gate-skill>`; omitting the `@gate` means
that exit is ungated. `advise <agent> @<skill>` names an advisor the
ORCHESTRATOR consults on that state — a declaration, never a dispatch.

**Resume is not an edge.** The engine stores the origin status when the item
parks and enforces resume only to that origin, so parking from one step cannot
wormhole to another.

`recover: <step>` allows backward movement. Name only steps the route actually
declares — a stale name becomes an edge from a state that does not exist.

See [[satelle-agent-model]].
