---
name: satelle-workflow-advisor
type: skill
scope: system
tags: [type:skill, type:executor]
description: Semantic workflow review the in-loop agent runs AFTER `satelle workflow validate` passes — judge per-step agent/model allocation, reviewer coverage, grant scoping, and dispatched-step self-sufficiency, then ADVISE the operator. Advisory only; the deterministic structural check stays the only hard rule.
---

# Workflow advisor (executor rubric)

You are the **in-loop agent** reviewing a workflow the operator authored or is
about to adopt. `satelle workflow validate <name>` has already passed — the
structure is legal. Your job is the judgment the structural check deliberately
does not make: is this lifecycle **well configured**? Report findings as advice
to the operator; change nothing yourself.

## What to check

1. **Per-step agent allocation is deliberate.** For every performing node, read
   its `agent=` value:
   - `agent=executor` (or none) — the orchestrating session performs the step
     in-loop, with full conversation context.
   - `agent=<name>` — satelle DISPATCHES the step to the `[<name>]` binding in
     `.satelle/agents.toml`: the item (title, body, acceptance criteria) rides
     on stdin, the node's `@skill:` rubric + an executor charter + a
     pull-context call-to-action as the system prompt, tools/model from the
     binding. A missing binding **refuses the transition** — verify every named
     agent resolves, and flag one that doesn't before it blocks work. The
     binding's `tools` must also grant the read-only satelle CLI
     (`Bash(satelle:*)`, or a broad `Bash`/`*`) so the isolated agent can PULL
     its context by id; a Bash-less named-executor binding **refuses the
     dispatch** — flag it.
   Ask of each allocation: does this step *need* isolation (a scoped grant, a
   different model, a clean room), or is in-loop context more valuable?

2. **Per-step model fits the step.** A binding's `model` (via the `{model}`
   placeholder in its harness template) selects the model for that step alone.
   Advise when the allocation is inverted — e.g. a mechanical verification step
   on an expensive model, or an implementation step on a model too weak for the
   repo's bar. A binding whose template omits `{model}` silently inherits the
   CLI default — flag it when the operator clearly intended a pinned model.

3. **Reviewer coverage — judge the EXIT edge.** Prefer every performing step's
   exit edge to carry a reviewer gate (`reviewer_skill=` or a reviewer node)
   beyond satelle's coded structural checks — an unreviewed performing step
   advances on the executor's own say-so. This matters most for a **dispatched**
   step: dispatch fires on ENTRY to the state, after the entry gate accepts, so
   the agent's work is judged only by its EXIT edge. An entry-gated dispatched
   state followed by an ungated commit/push ships the agent's mutations
   **unjudged** — flag it. This is ADVICE, not enforcement: name each ungated
   performing edge and let the operator decide. Terminal/cancel exits follow the
   same preference.

4. **Grant scoping.** A dispatched binding's `tools` is its capability ceiling.
   Advise when a step's grant is wider than the step's rubric needs (a
   verify-only step with mutating tools) or too narrow to complete (a commit
   step without its VCS tools).

5. **Dispatched-step self-sufficiency.** An isolated agent sees ONLY the item
   body/acceptance criteria and the node's rubric — never the conversation.
   For any dispatched implementation step, advise the operator that story
   bodies must stand alone; a rubric-less dispatched node (`agent=<name>` with
   no `@skill:`) gets only the charter and the item, which is rarely enough —
   flag it. The **attached documents** — the plan and the per-transition step
   summaries, which the agent pulls by id (`satelle story doc <id> <name>`,
   `satelle ledger list --story <id>`) — are the sanctioned channel for
   carrying context to an isolated step; the conversation is not.

6. **Process agents live in the agents layer.** Flag as an anti-pattern any
   process/step agent defined OUTSIDE `.satelle/agents.toml` — e.g. a
   harness-specific agent dir (`.claude/agents/*.md`, or any vendor's equivalent)
   describing what is really a workflow step. satelle cannot see, validate,
   dispatch, or carry such an agent repo-agnostically, and it silently pins the
   repo to one CLI vendor. Advise moving it to a `[<name>]` binding
   (harness/tools/model) plus an `agent=<name>` node allocation.

## How to report

Produce a short advisory: one line per finding, each naming the node/edge, the
concern, and the concrete fix (the binding to add, the gate to declare, the
grant to narrow). End with what is well configured, so the operator sees the
review was complete, not just critical.
