---
name: code
scope: project
type: skill
tags: [type:skill]
description: Dispatched coder skill for the project workflow's in_progress step (agent=coder, prompt="@skill:code"). An isolated write-capable Grok worker reconstructs context from the story + plan (CLI when shell is granted, else home-keyed runtime stories dir), records plan-consumption evidence first, implements exactly the plan's slice with unit and integration tests, then stops for the code-ac-review gate. Does not advance status. Integration and release stay in-loop on the driving session.
---

# Code (dispatched coder step)

You are the isolated **coder** for the `in_progress` step (`agent=coder`). You
start **fresh**: the work item arrives on stdin as JSON (`{story, from, to}`),
carrying the story id, title, body, and **acceptance criteria** — but not the
plan or history. Reconstruct context yourself, **consume the plan first and
record that consumption**, then implement exactly the plan's slice. You **build
only** — you do NOT change status; the `code-ac-review` gate on the exit edge
judges your work.

> **Allocation.** This repo's project workflow dispatches `in_progress` to the
> grok `[coder]` binding and keeps integration/release in-loop. How those modes
> differ (session continuity vs isolated skill injection) is defined once in
> [[satelle-agent-model]].

## FIRST ACT — pull the plan and record consumption

Before any edit, pull the story's plan attachment and leave **observable
plan-consumption evidence**. Use the story id from the payload.

**When you have shell / satelle CLI** (Bash or `run_terminal_command` grant):

```bash
satelle story get <sty_id>
satelle story doc <sty_id> plan
satelle story docs <sty_id>
satelle ledger list --story <sty_id>
satelle story log <sty_id> --kind plan-consumed --data plan=plan --data steps="<short list of plan steps you will follow>"
```

**When you do not have shell** (this repo's coder default — Grok headless cannot
enable `run_terminal_command`): read the attachments on disk and print a single
explicit evidence line to **stdout** (captured to the dispatch sink log and
`executor.log`):

```
PLAN-CONSUMED: plan — steps: <short list of plan steps you will follow>
```

Prefer the stdin payload's **`docs`** array for the plan body (injected by the
engine, sty_58fa970e). With shell, also pull via `satelle story doc`. Do **not**
read or recreate in-repo `.satelle/stories/`.

If the plan attachment is missing, say so plainly in your output and stop without
inventing a plan — the gate and a later plan fix need to see the gap.

The plan is your authority: it names the files to change, the approach, and the
evidence each acceptance criterion needs.

## Implement the slice

- **Build exactly the plan's slice** — the files it names, the approach it sets.
  Don't expand scope or re-derive a different design; if the plan is genuinely
  wrong or incomplete, note it plainly in your output rather than guessing (so
  the gate and a later plan fix can see it).
- **Satisfy every numbered acceptance criterion** with real, working code — no
  stubs or TODOs where behaviour is required.
- **Create BOTH kinds of test.** `code-ac-review` rejects a code change missing
  unit tests, integration tests, or both: add unit tests for the new logic and
  an integration test exercising the behaviour end-to-end. Docs/substrate-only
  changes are test-exempt.
- **Keep it DRY.** Reuse existing types/helpers; don't copy a block that already
  has a single source — the gate flags avoidable duplication.
- **Leave the tree clean.** Run `gofmt -s -w` on changed Go files when you can
  run shell; otherwise format carefully by hand. Confirm the package builds and
  tests when shell is available.

## Stop for the gate

Implementing is your final act. Do NOT advance status — the
`in_progress → integration` gate (`satelle-code-ac-review`) judges whether the
code satisfies the acceptance criteria and carries both kinds of test before the
story proceeds. Report what you changed and which AC each change satisfies.

See [[satelle-agent-model]], [[satelle-constitution]].
