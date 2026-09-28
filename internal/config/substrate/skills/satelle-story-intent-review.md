---
name: satelle-story-intent-review
scope: system
type: skill
tags: [type:skill, type:reviewer]
description: Entry gate for begin-work (backlog → plan or in_progress). Isolated reviewer validates PRESENTED story text is well-formed (title, goal body, numbered testable ACs). Does not rewrite the story.
---

# Intent review (begin-work gate)

## Primary objective

Validate the **presented** story draft. Answer only: may work begin / enter
planning? Do not rewrite body/ACs; do not invent a plan.

You get `{story, from, to}` on stdin. Read-only.

## Accept when

1. The **title** names a concrete change.
2. The **body** states a clear goal / what done looks like.
3. **acceptance_criteria** lists at least one numbered, testable item.

Whole bar. Do not demand a design, estimates, tags, or a particular style.

## Reject when

Intent is unclear: no goal, or ACs are missing or untestable ("make it
nicer"). On reject, name the failed check(s) only.

## First pass: every failed check, once

On the **first** presentation of this edge (no `prior_verdicts` in the payload),
name **every** failed check — never stop at the first. A failure held back for a
later pass costs a full round.

## Edited definitions: judge each edit against the Purpose

While a story is still in its editable state, a rejection may be followed by an
edit to its title, body, acceptance criteria or category, so the story can be
corrected instead of parked. Every such edit is recorded, and when the payload
carries `definition_edits` (each with `field`, `old`, `new`, `actor`), judge
**each edited acceptance criterion against the story's purpose** — the goal its
body states — and not against the last rejection alone:

- **Reject** an edit that **weakens** an AC only to get past a gate: removed
  scope, a lowered threshold, a deleted test or evidence obligation, a promise
  narrowed until it can no longer fail. Quote the **before** and the **after**.
- **Accept** an edit that removes a contradiction, an untestable phrase or a
  promise the code cannot keep, and still leaves the purpose fully delivered.
  An edit is not suspect for being an edit.

## Later rounds: verify, do not re-review

When the payload carries `prior_verdicts` (this edge's earlier verdicts, oldest
first), verify **every** prior finding as resolved or unresolved — never re-raise
a resolved one. Raise a **new** blocking finding **only** with new evidence you
can cite: a changed definition field (see `definition_edits`) or text the prior
payload did not contain. A check you could have failed in the first pass is not
new evidence — note it, do not block on it.

## Verdict

```json
{"decision": "accept", "notes": ""}
```
