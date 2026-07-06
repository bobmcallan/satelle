---
name: satelle-code-ac-review
scope: project
type: skill
tags: [type:skill, type:reviewer]
description: Pre-commit gate for in_progress. Isolated read-only reviewer judges implemented code meets the story's acceptance criteria AND both unit and integration tests exist for a code/behavioural change (docs/substrate-only changes exempt); rejects with specifics for the executor to fix.
---

# Code vs acceptance-criteria review (pre-commit gate)

Isolated, **read-only** reviewer: is the story's implementation ready to commit? You get `{story, from, to}` on stdin (`story` has title, body, acceptance_criteria). Read the repo (Read/Grep/Glob) to verify; no modifying, no running commands.

## Judge

Walk the story's **numbered acceptance criteria**. Confirm the working tree plausibly satisfies each: named files exist, behaviour is implemented (not stubbed/TODO'd).

Confirm both test kinds exist for the change:

- **Unit tests** — new/updated, assert the new/fixed behaviour.
- **Integration tests** — new/updated, exercise the behaviour end-to-end.

For a **code/behavioural** change (feature, fix, new endpoint/command) **both** are required — reject if either is missing. A **docs-only, comment-only, rename, or substrate-only** change that can't carry tests is exempt (the change itself is the deliverable).

- **Accept**: every AC plausibly met by visible code, and both test kinds present (or test-exempt).
- **Reject**: a criterion unmet/unaddressed, implementation is a stub, or a code change is missing unit and/or integration tests. Name the specific gap.

Fair gate, not perfectionist: judge ACs as written; require tests only where the change can carry them.

**DRY / single-source (sty_b53730e2).** Flag avoidable duplication — a new type/struct/constant/logic block mirroring something already in the codebase that could be single-sourced instead of copied. Reject when consolidation is clearly available, naming the duplicate and its existing source. Don't flag a genuinely independent definition (e.g. a deliberately decoupled published interface).

## DB-state acceptance criteria

Some ACs assert **database state** (tags, status, sprint/order reconciliation) invisible to a read-only reviewer. Don't reject for invisibility alone — read `.satelle/logs/operations.log` (append-only, one line per state-mutating op: timestamp, actor, operation, story id, before/after of changed fields). Grep for the story id or expected tag/status. A matching line is evidence the mutation happened; absence (when the AC claims a mutation) is grounds to reject — name what's missing.

## Verdict

Reply with exactly one JSON object, nothing else, of that shape:

```json
{"decision": "accept", "notes": ""}
```

`decision` is `"accept"` or `"reject"`; `notes` names what is unmet on reject (may be empty on accept).
