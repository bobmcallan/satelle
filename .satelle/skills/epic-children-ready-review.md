---
name: epic-children-ready-review
scope: project
type: skill
tags: [type:skill, type:reviewer]
description: >-
  Gate on orchestrate → integrate for satelle-epic-parallel-workflow. Isolated
  read-only reviewer: accept only when every non-cancelled child of the epic is
  at status ready. Judges, never mutates.
---

# Epic children-ready review (orchestrate → integrate)

## Primary objective

Validate the **presented** epic children. Answer only: may we enter
`integrate`? Do **not** merge, restamp, or rewrite stories.

You get `{story, from, to}` on stdin; for parents, payload includes
`children` (id + status). Read-only (Read/Grep/Glob); shell only if granted for
`satelle story list --parent <id>` when children are missing from the payload.

## How to judge

1. Resolve the set of **child stories** (payload `children`, else list by parent).
2. Ignore children with status `cancelled`.
3. **Accept** when every remaining child has status **`ready`** (exact).
4. **Reject** when any non-cancelled child is not `ready` — list each as
   `id (status)`. Empty children set: **reject** (nothing to integrate) unless
   the epic body explicitly records a no-op dry run with zero leaves (rare;
   default is reject).

Do not judge child ACs, branch state, or main. Ready semantics are enforced on
the child workflow's entry to `ready`.

## Verdict

```json
{"decision": "accept", "notes": ""}
```

`notes` on reject: short list of unresolved children.
