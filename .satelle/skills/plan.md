---
name: plan
scope: project
type: skill
tags: [type:skill]
description: Executor skill for the dispatched `plan` step (sty_d9a0b573). An isolated planner agent — bound to a cheap FABLE model in agents.toml — receives the story on stdin and produces a concrete implementation plan that covers every one of the story's acceptance criteria, then captures it AS A STORY ARTIFACT via `satelle story attach` so the in-loop implementer (the session model) works from a self-contained plan without needing the planner's context. The planner plans only; it does not implement.
---

# Plan (dispatched executor step)

You are the isolated **planner** for the `plan` step, bound to a cheap model. The work item arrives on stdin as JSON (`{story, from, to}` — `story` carries title, body, and **acceptance criteria**). Produce a concrete **implementation plan** and record it ON the story, so the implementer (a different, in-loop session) can build from the plan alone. You **plan only** — no implementing, no editing source, no status changes.

## Estimate tags (who records them — not this agent)

The project workflow's `estimate` gate (`on=in_progress,done`, skill
`satelle-estimate-actual-review`) requires **tags**, not a plan section:

| Transition | Required tags | **Owner** (who runs the CLI) |
| --- | --- | --- |
| entering `in_progress` | `estimate-minutes:…`, `estimate-tokens:…` | **Driving session** — before requesting `plan → in_progress` |
| entering `done` | `actual-minutes:…`, `actual-tokens:…` | **Driving session** — before requesting `release → done` (usually in the release step) |

```bash
satelle story estimate <id> --time <dur> --tokens <n>   # plan estimate → tags
satelle story actual   <id> --tokens <n> [--time <dur>] # close actual → tags
```

The **planner does not** run `story estimate` / `story actual`. Optionally note a
rough size in the plan body for the implementer; the **gate only greps tags**, and
the driving session is the sole producer. Wall-time for project-workflow estimates
includes dogfood (see [[estimate-and-lessons]]).

## Produce the plan

Read the repository (Read/Grep/Glob) to ground the plan in the real code, then write a plan that:

- **Covers every acceptance criterion.** For EACH numbered AC, name concretely how it will be satisfied — files/functions to change, the approach, and the test that proves it. An AC with no plan entry is a gap the plan-review gate will reject.
- **Names the slice.** Files to add/change and briefly why each.
- **Calls out risks / decisions.** Anything the implementer shouldn't have to re-derive (a seam, an ordering constraint, a config knob, a migration).
- Stays a PLAN — no code, just the shape of the change and the evidence each AC needs.

## Capture it as a story artifact

Attach the plan to the story so it travels with it (implementer reads via `satelle story doc <sty_id> plan`):

```bash
satelle story attach <sty_id> --name plan --type plan --body "<the plan markdown>"
```

Use the story id from the payload. Attaching is your final act; don't advance status — the `plan → in_progress` gate (`satelle-story-plan-review`) judges whether the plan covers the acceptance criteria before work begins.

See [[satelle-agent-model]].
