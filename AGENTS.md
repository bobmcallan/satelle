# Agent instructions (this repo)

Harness-facing guidance for coding agents working in satelle. For process detail,
prefer `satelle help` and `satelle help <topic>` over restating docs here.

## Complete stories and epics — do not stop after stage 1

Drive every **engaged** story (and epic) through its full configured workflow to a
**terminal status**: `done`, `cancelled`, or `blocked`.

- **Do not stop** at intermediate stages — `plan`, `in_progress`, `integration`,
  or `release` — and hand control back as if the work were finished. Those are
  waypoints; status is the only proof of done.
- **Stop only when blocked**: a real gap that prevents following the workflow
  (missing gate skill, human-only decision, unmet external dependency). Park with
  a structured reason via the workflow's `blocked` path — never silently abandon
  after stage 1.
- **Epics**: an epic is complete only when its child stories are terminal. Keep
  driving children until the epic can close.

One engaged story at a time. See also the session-injected agent-goals principle.

## Dogfood as you progress

Dogfood is part of each stage, not optional cleanup after "code is done".

- Run tests and verify behaviour while implementing.
- On **release**: install the **published** binary (`satelle update`), confirm
  `satelle version`, verify the live web footer, and keep the service under a
  **persistent** supervisor — the dogfood triad is release work, not a later
  nicety.

## Process detail

- Create and advance work with `satelle story create` / `satelle story set … --status …`.
- Bind agents in `.satelle/agents.toml`; allocate them from workflow nodes
  (`agent=<name>`). See `satelle help agent-dispatch`.
- Workflows, principles, skills, and gates live under `.satelle/` — consult
  `satelle help` rather than copying them into this file.
