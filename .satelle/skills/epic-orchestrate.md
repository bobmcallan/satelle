---
name: epic-orchestrate
scope: project
type: skill
tags: [type:skill]
description: >-
  In-loop executor skill for satelle-epic-parallel-workflow's orchestrate step.
  Executes strategy waves: ensures children are enabled for parallel-story,
  invokes the harness launcher for worktrees/sessions, monitors via satelle
  story list/seat until non-cancelled children reach ready. Does not spawn
  sessions itself and does not merge to main.
---

# Epic orchestrate (in-loop executor step)

You are the **executor** for the **orchestrate** step of a parallel epic
(in-loop on the driving session). The wave plan is attached (`plan` artifact).
**Execute the waves** until every non-cancelled child is at `ready`. You do
**not** merge, push main, bump `.version`, or advance children to `done`.

## Do

1. **Read the plan.** `satelle story doc <epic_id> plan` (or payload docs).
2. **Enable dogfood leaves if needed** (first orchestrate / dry-run setup):
   - `[gate] allow_parallel = true` for the run (repo setting).
   - Children stamped `workflow:satelle-parallel-story-workflow`.
   - Parent link and `epic:<theme>` tags present.
3. **Materialise each wave** via the harness launcher (`.claude` skill
   `parallel-epic-launcher` or equivalent): worktree + branch + driving session
   **per child**. You **invoke or instruct** the launcher; satelle does not spawn
   executors.
4. **Monitor.** Shared runtime plane (git common-dir repo keying):
   ```bash
   satelle story list --parent <epic_id>
   satelle story seat
   ```
   Wait until every non-cancelled child is `ready` (or cancel/block with a
   recorded reason).
5. **Stop for the gate.** Do not self-enact `orchestrate → integrate` —
   `epic-children-ready-review` judges that every non-cancelled child is
   `ready`.

## Do not

- Spawn ad-hoc sessions without the launcher contract (worktree naming + branch).
- Merge to main or run `make integration` for the epic merge (that is
  **integrate**).
- Touch children's `.version` / CHANGELOG from this session.
- Leave a wave half-done without parking (`blocked`) or recording the gap.

See [[satelle-agent-model]].
