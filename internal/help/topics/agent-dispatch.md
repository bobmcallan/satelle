# Named-agent dispatch — how an isolated step receives its instructions

A workflow node can allocate a step to a **named agent** instead of the in-loop
session. When a state carries `agent=<name>` (any name other than `executor` or
`reviewer`), satelle **dispatches** that step: it spawns the agent CLI configured
in `.satelle/agents.toml` under `[<name>]`, hands it the work, and folds the
result back in. The agent runs with a **fresh context** — it never sees the
conversation — so the contract below is how it learns what to do and how it gets
the rest of the story.

## The dispatch contract

Dispatch fires **on entry** to the state, after that state's entry gate accepts.
The agent receives:

- **System prompt**, assembled in this order:
  1. the session-resident principles (unless the binding sets
     `inject_principles = false`; default on),
  2. an **executor charter** — you are performing this step of this workflow;
     do the step's work, but **never change the item's status** (the workflow's
     gates govern every advance),
  3. the **pull-context call-to-action** (see below),
  4. the node's `@skill:<name>` **rubric** — the instructions for the step.
- **stdin**: the work item as JSON — `{story, from, to, review_skill}`. `story`
  carries the id, title, body, and acceptance criteria; `from`/`to` are the
  transition being performed.
- **Capabilities**: exactly the binding's `tools` and `model` grant, nothing
  more.

**Refusals (fail loud, never silent):**

- A node names `agent=<name>` but `.satelle/agents.toml` defines no `[<name>]`
  binding → the transition is **refused** (there is no silent in-loop fallback).
- A binding's `tools` grant does not include the read-only satelle CLI
  (`Bash(satelle:*)`, or a broad `Bash` / `*`) → the dispatch is **refused**,
  because the agent could not pull its context (below).
- A binding whose harness is `in-loop` keeps the step with the orchestrating
  session — not dispatched.

The agent's output is captured to the executor log (and, for a task execution, a
run-output document), so an isolated step's work stays reviewable.

## The pull contract — reconstruct context by id, don't wait to be told

Because a dispatched agent starts fresh, satelle does **not** cram documents or
history into the payload. The payload is a **handle** — the item and its id — and
the agent **pulls** everything else itself, by id, with the read-only satelle CLI:

- `satelle story get <id>` — the full current record.
- `satelle story docs <id>`, then `satelle story doc <id> <name>` — the attached
  documents: the implementation **plan** and every prior **step summary** (each
  gated transition deposits one), which narrate the work so far.
- `satelle ledger list --story <id>` — the evidence ledger (transitions, review
  verdicts, summaries).

A read-only reviewer whose grant excludes Bash reads the same attachments on disk
under `.satelle/stories/<id>/` (tasks under `.satelle/tasks/<id>/`). Either way:
**fetch before concluding a document or a prior step is missing.** This is why a
dispatched binding must grant the satelle CLI — it is the agent's context channel.

## What makes a step safe to dispatch (sufficiency)

- **Give the node a rubric.** A dispatched node needs `prompt="@skill:<name>"`.
  A rubric-less dispatched node (`agent=<name>` with no `@skill:`) receives only
  the charter and the item — rarely enough to perform a real step.
- **Make the item self-sufficient.** An isolated agent never sees the
  conversation, so the story's body, acceptance criteria, and attached docs must
  **stand alone**. Anything the step needs that lives only in the chat is lost.
  The plan and step-summary documents (pulled by id) are the sanctioned channel
  for carrying context forward — not the conversation.

## Gate/dispatch sequencing — judge the EXIT edge

Dispatch fires **on entry to the target state, after the entry gate accepts** —
so a dispatched step's work must be judged by its **exit edge's** gates. An
entry-gated state followed by an ungated commit/push ships the dispatched agent's
mutations **unjudged**. When you allocate a step to a named agent, make sure the
edge *out* of that state carries the review that vets what the agent did.

See also: `satelle help workflows` (choosing a lifecycle) and
`satelle help reviewer-checks` (gate skills).
