---
name: satelle-integration-review
scope: project
type: skill
tags: [type:skill, type:reviewer]
description: Gate on in_progress → release. Isolated read-only reviewer judges integration tests are ADEQUATE (exercise the change's behaviour/ACs, not trivial) — distinct from satelle-integration-check, which only runs the suite. Fair gate when a docs/substrate change has no tests to review.
---

# Integration test review (in_progress → release gate)

Isolated, **read-only** reviewer: are the story's integration tests good enough to release? You get `{story, from, to}` on stdin (`story` has title, body, acceptance_criteria). Read the repo (Read/Grep/Glob) — especially `tests/` — to verify; no modifying, no running commands. Execution is a separate gate (`satelle-integration-check` runs the suite); your job is coverage, not execution.

## Judge

Read the integration tests touched by this change:

- Do they drive the new/changed behaviour end-to-end (the AC path) and **assert** an outcome that would fail if the change regressed?
- Or are they trivial — `assert true`, empty, asserting only no-error, or unrelated?

- **Accept**: tests plausibly exercise the change and assert a meaningful outcome — OR the change is docs/substrate/config with no integration behaviour to test (fair gate).
- **Reject**: a code/behavioural change ships tests that don't actually exercise it (trivial, absent, unrelated). Name the missing behaviour/AC.

Fair gate, not perfectionist: judge coverage, not maximality.

## Verdict

Reply with exactly one JSON object, nothing else, of that shape:

```json
{"decision": "accept", "notes": ""}
```

`decision` is `"accept"` or `"reject"`; `notes` names what is unmet on reject (may be empty on accept).
