---
name: satelle-route-standard
type: principle
tags: [type:principle]
applies_to: ["*"]
description: Canonical authored form of a lifecycle — done.toml declares the obligations per category, step.toml says what discharges each, and the BINARY derives the topology. THE single reference other workflow tools cite. Full key-by-key grammar in `satelle help workflows`.
---

# A lifecycle is a derived route

A satelle lifecycle is **two authored files** under `.satelle/workflows/`, and
the graph between their states is **derived by the binary** — never authored.
Both are TOML.

| File | Declares |
| --- | --- |
| `done.toml` | What DONE means: one table per category, keyed by the category name, listing the obligations in order, plus the park, cancel and recover states |
| `step.toml` | What discharges each obligation: one table per step, keyed by the obligation it discharges, plus one `[[gate]]` entry per always-on gate |

`["*"]` in done.toml governs any category with no table of its own. Neither file
may declare `applies_to` at document level — done.toml's category tables are the
selector, and a second one would be a second precedence rule.

The full key-by-key reference is `satelle help workflows`, and `satelle help
workflow-convert` covers converting an older route source. This principle states
only the rules an author must hold in mind while writing.

## An obligation names a step by its TABLE KEY

This is the one rule the format does not make obvious, and the usual mistake. A
step's `status` is the **stage name** an item holds there, and several steps
deliberately share one; identity is the table key alone.

```toml
# done.toml — WHAT must be true, per category
["*"]
obligations = ["raised", "planned", "coded", "closed"]
park   = { state = "blocked", gate = "satelle-story-blocked-review" }
cancel = { state = "cancelled", gate = "satelle-story-cancel-review" }
recover = { step = "coded", from = ["released"] }
```

```toml
# step.toml — WHICH step discharges each obligation
[coded]                     # the KEY is the obligation done.toml named
status    = "in_progress"   # the stage name, not the identity
agent     = "executor"
skills    = ["code"]
reviewers = ["satelle-story-plan-review"]
requires  = ["planned"]

[[gate]]                    # an always-on gate occupies no stage
skill = "satelle-step-summary"
on    = ["*"]               # STATUSES it fires on entry to ("*" = every step)
for   = ["*"]
```

An unknown key is a construction ERROR naming the offending line, never a silent
drop: a typo'd `reviewrs =` that parsed as "no reviewers" would lose a gate, and
a route that quietly loses a gate is the one failure this representation must not
have.

## The binary owns TOPOLOGY; the author owns OBLIGATION

An author says what must be true before work closes and what discharges it. The
binary sorts the steps (a topological sort of `requires`) and synthesises the
shape: cancel from every non-terminal step, park from anywhere, backward
movement, park → cancel.

Writing those edges as steps is the most common authoring mistake. If you find
yourself declaring a `cancelled` or `blocked` step table, delete it —
done.toml's `cancel` and `park` keys are where those belong.

## A step names an AGENT, never a harness

A step's `agent` and a gate's `agent` name the `.satelle/workflows/agents.toml` binding
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

A step with no `agent` is performed in-loop by the orchestrating session. A
reviewer with no `reviewer_agent` runs under `[reviewer]`.

## Gates belong to the step they ADMIT

A gate is not a property of an edge. Every reviewer that must pass before work
enters a step is that step's `reviewers = [...]`, and `reviewer_agent` names the
binding that runs them. `parallel = 0` makes a multi-reviewer gate sequential;
leaving `parallel` unset is not the same as setting it to 0 — unset, two or more
reviewers run concurrently.

An always-on gate — one that fires on entry to several steps rather than one —
is a `[[gate]]` entry with `on = [...]`. **`on` matches STATUSES, not step table
keys** — it is the one place the table-key rule above does not apply, and an
`on` written as a step key silently never fires. `["*"]` fires on every step.

## Coded-check gates name no agent

A skill whose body carries a coded `check` fence is a **functional check**: the
script *is* the decision. The engine runs it and returns before any binding
lookup, tools grant, role check or agent process. Naming an agent on that path
is inert and misleading — it reads as a dispatch that never happens.

**Convention:** a coded-check gate names no `agent`. Then every `agent` on a
gate means a real LLM dispatch, readable at a glance. If the skill later stops
being a coded check, the omitted agent degrades to `[reviewer]` rather than
erroring.

## Tag-scoped obligations and gates

Two forms scope work to the stories that need it, both matching **tags** only —
never category, never kind:

- `[[<category>.tag_obligation]]` in done.toml, carrying `tag` and `obligation`,
  appends an obligation when the story carries the tag, so a `surface:ui` story
  acquires a design obligation the others do not have.
- `applies_to = ["<tag>"]` on a step table or a `[[gate]]` enqueues it only for a
  story holding a matching tag.

A story with two matching tags picks up **both** scoped items — a plain filter,
with no override and no tie-break.

## Gates in a shared catalogue need `for`

One step catalogue serves every lane. An always-on `[[gate]]` declares
`for = [...]` — the categories whose route it belongs to — or it fires on every
lane, including ones with no release to verify. Note that `for = ["*"]` means
the *wildcard category table*, not "everything": a gate scoped that way never
fires on a lane that has a category table of its own.

## Park, cancel and recover

`park` and `cancel` are inline tables on the category — `{ state = "…", gate =
"…" }`; omitting `gate` means that exit is ungated. `advisor` (with
`advisor_skill`) names an advisor the ORCHESTRATOR consults on that state — a
declaration, never a dispatch. A step declares its own advisor as
`advise = { agent = "…", skill = "…" }`.

**Resume is not an edge.** The engine stores the origin status when the item
parks and enforces resume only to that origin, so parking from one step cannot
wormhole to another.

`recover = { step = "…", from = [...] }` allows backward movement. Name only
steps the route actually declares — a stale name becomes an edge from a state
that does not exist.

Run `satelle workflow validate` after editing: it parses both halves and prints
the effective gate/model table, which is the fastest way to see what an edit did
to the route.

See [[satelle-agent-model]].
