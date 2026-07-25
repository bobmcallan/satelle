---
name: satelle-agent-model
type: principle
tags: [type:principle]
applies_to: ["*"]
description: The agent model. A workflow is a graph of steps, each run by a defined role with a bounded grant: the executor does the work; the reviewer is read-only, returning a verdict on the OUTCOME. Any role but the in-loop executor runs isolated over a satelle-built payload. satelle is the status gatekeeper — only a reviewer's accept advances gated status.
---

# The agent execution model

satelle runs one model: a story moves through a graph of **steps**, each step is
run by a **defined agent role**, and the story's **status** decides what is valid
now. The agent's one goal is to drive the story to its terminal state; satelle is
the gatekeeper of status. (**Agent** here means the step's *performer role*, not
the agent CLI backend a step may run on.)

## Two roles, bounded grants

- **executor** — does the work. Mutates the tree, follows the step's rubric,
 requests the next status. Full tool grant.
- **reviewer** — limited to reviewing. Read-only judge of the claimed **outcome**;
 returns a structured verdict; never mutates code, story, or status. The
 read-only limit is enforced by the grant, not by trust.

## Two run modes

**In-loop executor.** The driving session *is* the executor. Full session context,
principles, and skills via `.satelle/` and the `satelle` CLI. Default for steps
allocated `agent=executor` (including this repo's `in_progress` / `integration` /
`release`).

**Isolated invocation.** satelle spawns a fresh-context sub-process over a
**payload it builds** (work item + transition), with the step's skill as the
system prompt and the binding's grant. Return value is aggregated to gate status.
Both reviewers and named agents run isolated; they differ in **kind**:

| Kind | Role | Output | Advances status? |
| --- | --- | --- | --- |
| reviewer | judge | structured verdict | only on accept (via satelle) |
| named agent | perform | run evidence | never (exit gate still decides) |

## `agent=` allocation

A workflow node names its performer:

- `agent=executor` — in-loop (default)
- `agent=reviewer` — isolated gate
- `agent=<name>` — isolated named agent bound in `.satelle/agents.toml` (e.g.
 `agent=planner` for the read-only plan step)

Every top-level `[section]` in `agents.toml` is an agent. `[executor]` /
`[reviewer]` are built-in roles; any other name is a named agent. Unbound
`<name>` refuses the transition (fail-fast — never silent in-loop fallback). A
binding with `command=in-loop` keeps the step with the orchestrator. Per-binding
`{model}` is per-step model selection as configuration.

## `@skill:` is satelle's declaration

`prompt="@skill:NAME"` names a rubric under `.satelle/skills` — not any agent
CLI's native skill call. The in-loop executor **reads** it; an isolated agent
receives it as its system prompt. Skills guide; **gates enforce**.

## satelle gates status; accept is the only advance

Status advances only through a reviewer's **accept** on the guarding edge — never
by patching status, relabelling, or skipping the reviewer. Shipping work then
declining the gate is abandoning the job.

## Process is configuration

Steps, order, role, skill, and which edges gate status live in **workflows and
skills** (authored substrate per [[satelle-constitution]]), not Go branches.
Status decides which step applies now; the terminal state is reached only with
every gate on the path accepted.

See [[satelle-agent-goals]], [[satelle-done-is-last]],
[[satelle-constitution]].
