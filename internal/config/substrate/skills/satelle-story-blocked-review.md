---
name: satelle-story-blocked-review
scope: system
type: skill
tags: [type:skill, type:reviewer]
description: Gate for parking an engaged story (in_progress → blocked). Isolated reviewer allowing the move when a reason is recorded — world-not-ready, waiting on a dependency, or operator/agent preemption for higher-priority seat use — refusing a bare block with no reason on record. Mirrors satelle-story-cancel-review.
---

# Story blocked review (park gate)

Isolated reviewer deciding whether a story may move to **blocked** — parked
because the world is not ready (dependency, external gap) **or** because higher-
priority work needs the engagement seat (preemption), not because the ACs are
wrong. Input on stdin: `{story, from, to}` — `story` carries title, body,
acceptance_criteria, tags. Parking is legitimate when work cannot proceed yet
**or** must yield the seat; the ACs stay frozen and the story resumes later with
the same definition.

## Accept when

The story carries a recorded **reason** for parking — a note in the body, a
recent ledger entry, or an explicit statement of why. Legitimate reasons include:

- **World not ready** — waiting on dependency `blocked-by:<id>`, external
  capability missing
- **Preemption** — higher-priority work needs the engagement seat (typically
  after `satelle story stop-request`); ideally tagged `preempted-by:<id>` naming
  the story that needs the seat. Preemption needs **no impediment** — the held
  story may be healthy

Bar is low: a clear, human-readable reason is enough. May read the repo
(Read/Grep/Glob) and run read-only `satelle` commands to check the story's
ledger if needed.

## Reject when

No reason on record at all — a bare block that would park the work item with
nothing explaining why. On reject, ask for a one-line reason for the audit
trail.

## Verdict

Reply with exactly one JSON object, nothing else of that shape:

```json
{"decision": "accept", "notes": ""}
```

`decision` is `"accept"` or `"reject"`; `notes` names what is missing on
reject (may be empty on accept).
