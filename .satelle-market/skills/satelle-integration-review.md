---
name: satelle-integration-review
scope: project
type: skill
tags: [solo-dev, reviewer, gate, integration]
description: Reviewer gate for integration → release: tests adequately cover the slice and local suite results are credible. Isolated read-only judge.
---

# Integration test review (integration → release)

## Primary objective

Validate the **presented** integration tests against the **story** and the
change. Answer only: may we advance to `release` (from a test-adequacy view)?
Do **not** write tests. Do **not** run the suite (`satelle-integration-check`
does that). Do **not** invent a better test plan as the standard.

You get `{story, from, to}` on stdin. Read-only (especially `tests/`).

## Judge

1. **Locate presented tests** for this story's change (new/updated under
   `tests/` or co-located tests clearly for the slice).
2. For each **code/behavioural** AC path: do those tests drive the behaviour
   end-to-end and **assert** an outcome that would fail if the change
   regressed?
3. **Accept** when presented tests adequately cover the AC paths — **or** the
   change is docs/substrate/config with no integration behaviour to test.
4. **Reject** when a behavioural change has missing, trivial (`assert true`,
   empty, no-error-only), or unrelated tests — name the AC/behaviour gap.

Fair gate: coverage of stated ACs, not maximality or a private ideal suite.

## Verdict

```json
{"decision": "accept", "notes": ""}
```
