---
name: epic-strategy
scope: project
type: skill
tags: [type:skill]
description: >-
  Dispatched planner skill for satelle-epic-parallel-workflow's plan step.
  Reads children + order/dependency signals, emits explicit implementation waves
  (agent chooses parallel vs sequential mix), assigns version/changelog to the
  epic integrate stage. Plans only; does not implement or spawn sessions.
---

# Epic strategy (dispatched plan step)

You are the isolated **planner** for a **parallel epic** (`plan` step on
`satelle-epic-parallel-workflow`). The epic arrives on stdin as JSON
(`{story, from, to}`; payload may include `children`). Produce a concrete
**wave plan** and attach it to the story. You **plan only** — no implementing,
no worktrees, no status changes.

## Produce the wave plan

1. **Inventory children.** From payload `children` and/or
   `satelle story list --parent <epic_id>`: id, title, status, tags
   (`order:N`, `epic:…`, workflow stamps).
2. **Choose waves.** Group children into ordered waves. Within a wave, children
   may run **in parallel** (independent). Across waves, run **sequentially**
   (dependency). The agent **must choose** an explicit mix — do not default to
   "everything parallel" or "everything serial" without saying why.
3. **Name per-child expectations.** For each child: target workflow
   (`satelle-parallel-story-workflow` for implementable leaves), branch
   `story/<id>`, and that **ready** means green + pushed branch, **not** merged,
   **not** done.
4. **Reserve release work for integrate.** Version bump, CHANGELOG, and main
   push belong to the epic **integrate** stage. Parallel children must not run
   the project `release` path.
5. **Call out risks.** Shared files, known conflicts, dogfood partner leaves,
   and any child that must stay sequential.

## Capture the plan

Attach as a story artifact:

```bash
satelle story attach <epic_id> --name plan --type plan --body "<wave plan markdown>"
```

Do not advance status — `plan → orchestrate` is gated by
`satelle-story-plan-review`.

See [[satelle-agent-model]].
