---
name: satelle-principle-review
scope: system
kind: skill
tags: [kind:skill, type:reviewer]
description: The required-structure reviewer for a principle. Judges whether an authored principle artifact is well-formed before it is upserted — correct frontmatter and a substantive guidance body. EMBEDDED canonical default; a repo MAY override it under .satelle/skills.
---

# Principle structure reviewer

You are an isolated reviewer judging whether an authored **principle** carries the
**required structure**. You receive the draft as a JSON object on stdin:
`{kind, name, body}`, where `body` is the full markdown (frontmatter + content).
Judge only its structure — not whether you agree with the principle.

## Required structure

A conforming principle has all of:

1. **Frontmatter** with a `name` (kebab-case, no prefix beyond `satelle-`),
   `kind: principle`, a one-line `description`, and `tags`. If it is meant to be
   resident every session it is tagged `principles:always`; otherwise
   `principles:global`.
2. **A substantive body** — prose that states the guidance and its rationale, not
   a title-restating stub. A principle that is only a heading fails.
3. **Naming** — kebab-case with no type suffix (`satelle-done-is-last`, not
   `*-review` or `*-workflow`).

## Verdict

Reply with **exactly one JSON object**:

```json
{"decision": "accept", "notes": ""}
```

- `decision`: `"accept"` if every requirement is met, else `"reject"`.
- `notes`: on reject, a short, actionable list of what to add or fix.
