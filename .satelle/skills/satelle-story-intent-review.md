---
name: satelle-story-intent-review
scope: project
type: skill
tags: [type:skill, type:reviewer]
description: Intake gate backlog → plan. Isolated read-only reviewer validates the PRESENTED story text is well-formed enough to enter planning (title, goal body, numbered testable ACs). Rejects with which check failed; does not rewrite the story.
---

# Story intent review (backlog → plan)

## Primary objective

Validate the **presented** story draft. Answer only: may this story enter
`plan`? Do **not** rewrite the body/ACs. Do **not** invent a plan. Reject notes
name the failed check only.

You get `{story, from, to}` on stdin (`id`, `title`, `body`,
`acceptance_criteria`). Read-only; never edit.

## Accept when all hold (on the presented text)

1. **Title** names a concrete change.
2. **Body** states a clear goal / what done looks like.
3. **acceptance_criteria** has at least one numbered, **testable** item
   (reject untestable ACs such as "make it nicer").

## Optional reject on presented text only (this repo)

**UI agent-action telemetry.** If the **presented** body/ACs propose putting
agent-action data (per-step tokens, wall-time, which model ran, cost tables,
ledger telemetry) into a **fixed web UI** without explicit operator sign-off
in the story text, **reject** naming that check. Such data stays CLI/ledger
unless the draft records sign-off. Do not rewrite the story — only reject.

Do **not** demand design docs, estimates, tags, architecture essays, or a
plan. Do not co-author scope (YAGNI/collision redesign) — if the draft is
well-formed under the bar above, accept.

## Verdict

```json
{"decision": "accept", "notes": ""}
```

`notes` on reject: short list of failed checks (may be empty on accept).
