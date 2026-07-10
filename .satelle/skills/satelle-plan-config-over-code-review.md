---
name: satelle-plan-config-over-code-review
scope: project
type: skill
tags: [type:skill, type:reviewer]
description: Optional plan gate (not on the default project DOT). Validates PRESENTED plan/story text for configuration-over-code violations only — quotes claims, never re-plans. Judges, never writes.
---

# Plan review — configuration over code (presented text only)

## Primary objective

Validate the **presented** plan (or story body if no plan) against one rule:
process/gates/opinions stay in substrate, not binary code. Answer only: may we
advance under that rule? Do **not** invent a better plan. Do **not** redesign.

> **Not wired** on the default `satelle-project-workflow` DOT (plan → in_progress
> uses `satelle-story-plan-review`). Keep this skill for optional/extra edges or
> manual review; do not treat it as a second planner.

You get `{story, from, to}` on stdin. Prefer the attached `plan` if present;
else the story body/ACs as the presented intent. Read-only.

## Rule

- **Substrate:** process, gates, workflows, opinions → `.satelle/` markdown.
- **Mechanism:** load/run/index/dispatch/storage → may be binary.

## Accept when

The **presented text** does not propose deciding process/gate/opinion in Go, **or**
it only changes genuine mechanism, **or** it is ordinary product code with no
process-in-binary claim.

Default **accept** unless the presented text **explicitly** proposes shipping
process as code.

## Reject when

The presented text explicitly hardcodes a process/gate/verdict/opinion in the
binary where substrate would suffice. **Quote** the violating plan/story line
and name where it belongs (`.satelle/`). Do not propose an alternate design.

## Verdict

```json
{"decision": "accept", "notes": ""}
```
