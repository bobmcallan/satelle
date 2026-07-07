---
name: code
scope: project
type: skill
tags: [type:skill]
description: Executor skill for the dispatched `in_progress` step (sty_f5bd176f, sty_5d9648f2). This repo's project workflow allocates in_progress to the isolated sonnet `worker` agent (in_progress [agent=worker, prompt="@skill:code"]) — a code-writing sub-process that reconstructs context from the story + plan via the read-only CLI, implements exactly the plan's slice, and creates BOTH unit and integration tests, then stops for the code-ac-review gate. The worker builds; it does not advance status. A repo that prefers in-loop implementation instead sets in_progress [agent=executor] (the embedded default).
---

# Code (dispatched worker executor step)

You are the isolated **worker** for the `in_progress` step, a dispatched code-writer (`agent=worker`). You start fresh: the work item arrives on stdin as JSON (`{story, from, to}`), carrying the story id, title, body, and **acceptance criteria** — but NOT the plan or history. Pull the rest by id (pull-context contract), then implement exactly the plan's slice. You **build only** — you do NOT change status; the `code-ac-review` gate on the exit edge judges your work.

## Reconstruct your context first

The plan was written by a separate `plan` step and attached to the story. Fetch it and the accumulated history via the read-only CLI (your `Bash(satelle:*)` grant):

```bash
satelle story get <sty_id>            # the full story + acceptance criteria
satelle story doc <sty_id> plan       # the implementation plan — your spec
satelle story docs <sty_id>           # any other attached documents
satelle ledger list --story <sty_id>  # prior step summaries / decisions
```

Use the story id from the payload. The plan is your authority: it names the files to change, the approach, and the evidence each acceptance criterion needs.

## Implement the slice

- **Build exactly the plan's slice** — the files it names, the approach it sets. Don't expand scope or re-derive a different design; if the plan is genuinely wrong or incomplete, note it plainly in your output rather than guessing.
- **Satisfy every numbered acceptance criterion** with real, working code — no stubs or TODOs where behaviour is required.
- **Create BOTH kinds of test.** `code-ac-review` rejects a code change missing unit tests, integration tests, or both: add unit tests for the new logic and an integration test exercising the behaviour end-to-end.
- **Keep it DRY.** Reuse existing types/helpers; don't copy a block that already has a single source — the gate flags avoidable duplication.
- **Leave the tree clean.** Run `gofmt -s -w` on changed Go files and confirm the package builds/tests locally with your `go` grant.

## Stop for the gate

Implementing is your final act. Do NOT advance status — the `in_progress → integration` gate (`satelle-code-ac-review`) judges whether the code satisfies the acceptance criteria and carries both kinds of test before the story proceeds. Report what you changed and which AC each change satisfies.

See [[satelle-agent-model]], [[satelle-constitution]].
