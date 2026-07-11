---
name: satelle-code-ac-review
scope: project
type: skill
tags: [type:skill, type:reviewer]
description: Gate on in_progress → integration. Isolated read-only reviewer validates presented working-tree code and tests against the story's numbered ACs. Judges presented outcome only; never implements or redesigns.
---

# Code vs acceptance-criteria review (in_progress → integration)

## Primary objective

Validate the **presented** working tree (code + tests) against the **story**.
Answer only: may we advance to `integration`? Do **not** create-and-complete
this step. Do **not** invent an alternate implementation and match against it.

You get `{story, from, to}` on stdin. Read-only (Read/Grep/Glob); no modifying,
no running the suite (that is a later gate).

## Judge (presented code only)

1. **ACs vs code.** Walk the numbered ACs. Confirm visible code plausibly
   satisfies each (files exist, behaviour implemented, not stubbed/TODO).
2. **Tests present for behavioural change.** For a code/behavioural change,
   unit and integration tests that exercise the change must exist (new or
   updated). Docs/comment/rename/substrate-only changes are test-exempt.
3. **DB-state ACs.** If an AC asserts store/DB state, use
   `.satelle/logs/operations.log` (or equivalent evidence) — do not reject
   solely because state is invisible in the tree.
4. **Plan fidelity (when a plan attachment exists).** If `satelle story doc
   <id> plan` (or an attached plan) is present, the presented tree must
   implement the plan's **named slice** (files/approach it commits to), or the
   executor must have **noted a plan defect** instead of silently building a
   competing design. **Hard-reject** only when the tree clearly ignores the
   plan's named slice **and** no plan-defect note is visible (commit message,
   step output, or attached note). Do **not** invent a better design and reject
   against it. No plan attachment → skip this check (ACs alone).

- **Accept** when every AC is met by presented evidence, test requirements
  hold (or exempt), and plan fidelity holds when a plan exists.
- **Reject** when an AC is unmet/stubbed, required tests are missing, or plan
  fidelity fails — name the gap for the **executor** (use "plan fidelity" when
  that is the reason).

Fair gate: judge ACs **as written**. Do not add requirements the story never
stated. Do not redesign.

**DRY (presented only).** Reject only when the change **as written** clearly
duplicates an existing type/logic that the presented code could call instead —
name both. Not a bar on deliberate independent definitions.

### Worked example — plan fidelity

- **Accept:** plan names `internal/wfdot/refresh.go` + tests; tree changes those
  paths (and needed call sites) covering the ACs.
- **Accept:** plan is wrong about a path; executor notes "plan defect: X" and
  implements the AC-correct approach instead.
- **Reject (plan fidelity):** plan names a concrete slice; tree implements an
  unrelated redesign with no plan-defect note — even if ACs look greppable.

## Verdict

```json
{"decision": "accept", "notes": ""}
```
