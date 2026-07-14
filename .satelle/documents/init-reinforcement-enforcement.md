---
type: document
title: Harness enforcement verification for satelle reinforcement hooks
description: AC4 evidence for sty_0699637c — which init-reinforced hooks map to harness events and how exit-2 blocking is proven.
---

# Harness enforcement verification (init reinforcement hooks)

## Hook map (after `satelle init` heal)

| Harness event | Satelle command | Block on deny |
| --- | --- | --- |
| SessionStart | `satelle reindex` + `satelle hook context` | fail-open (inject only) |
| PreToolUse (edit) | `satelle hook gate` via script-file form | exit 2 → harness blocks tool |
| PreToolUse (bash) | `satelle hook commitgate` via script-file form | exit 2 → harness blocks tool |
| UserPromptSubmit | `satelle hook prompt` | fail-open |
| Stop | `satelle hook stopcheck` | exit 2 → harness blocks stop |

## Proof in this repo

- PreToolUse gate/commitgate deny shape and exit 2: `tests/hook_enforcement_test.go`
  (gate/commitgate deny-shape tests; fail-visible script form).
- Stop exit 2: `TestHookStopcheck` in `tests/hook_enforcement_test.go`.
- Init append-if-missing + incomplete WARN: `internal/cli/cmd_init_hooks_test.go`
  (`TestReinforceSessionStartAndPreToolUse`, `TestReinforceWarnsOnUnparseableSettings`).

Claude Code / Grok treat PreToolUse and Stop command hooks that exit 2 as hard
blocks of the tool / stop action. The satelle side emits exit 2 on policy deny;
the harness side is the load-and-enforce contract documented above and exercised
by the hook enforcement suite.
