---
name: code
scope: project
type: skill
tags: [type:skill]
description: In-loop executor skill for the project workflow's in_progress step (agent=executor, prompt="@skill:code"). The driving session implements exactly the plan's slice with full session context, creates unit and integration tests, then stops for the code-ac-review gate. Does not advance status. The in-loop vs isolated-dispatch rule lives in satelle-agent-model — this rubric assumes the project workflow's hybrid allocation (perform in-loop; plan/reviewers dispatched).
---

# Code (in-loop executor step)

You are the **executor** for the `in_progress` step — **in-loop on the driving session** (`agent=executor`). You already have the full session context (story, plan, principles, prior steps). Read the `@skill:code` rubric and implement the plan's slice. You **build only** — you do NOT change status; the `code-ac-review` gate on the exit edge judges your work.

> **Allocation.** This repo's project workflow keeps implement/integrate/release in-loop and dispatches plan + reviewers. How those modes differ (session continuity vs isolated skill injection) is defined once in [[satelle-agent-model]] and recorded in the hybrid decision document — do not re-derive it here. A different repo may rebind `in_progress` to a named agent; then the same skill name can ride as a system prompt on that dispatch.

## Orient from the story and plan

The plan was written by the `plan` step and attached to the story. Use the story id in focus:

```bash
satelle story get <sty_id>            # full story + acceptance criteria
satelle story doc <sty_id> plan       # the implementation plan — your spec
satelle story docs <sty_id>           # any other attached documents
satelle ledger list --story <sty_id>  # prior step summaries / decisions
```

The plan is your authority: it names the files to change, the approach, and the evidence each acceptance criterion needs. You are not a fresh subprocess — prefer session knowledge, and re-fetch when the plan or ACs may have changed.

## Implement the slice

- **Build exactly the plan's slice** — the files it names, the approach it sets. Don't expand scope or re-derive a different design; if the plan is genuinely wrong or incomplete, note it plainly in your output rather than guessing (so the gate and a later plan fix can see it).
- **Satisfy every numbered acceptance criterion** with real, working code — no stubs or TODOs where behaviour is required.
- **Create BOTH kinds of test.** `code-ac-review` rejects a code change missing unit tests, integration tests, or both: add unit tests for the new logic and an integration test exercising the behaviour end-to-end. Docs/substrate-only changes are test-exempt.
- **Keep it DRY.** Reuse existing types/helpers; don't copy a block that already has a single source — the gate flags avoidable duplication.
- **Leave the tree clean.** Run `gofmt -s -w` on changed Go files and confirm the package builds/tests locally.

## Stop for the gate

Implementing is your final act. Do NOT advance status — the `in_progress → integration` gate (`satelle-code-ac-review`) judges whether the code satisfies the acceptance criteria and carries both kinds of test before the story proceeds. Report what you changed and which AC each change satisfies.

See [[satelle-agent-model]], [[satelle-constitution]].
