---
name: satelle-lessons
scope: project
type: skill
tags: [type:skill]
description: Post-release friction capture — attach a typed lessons document recording satelle's OWN process/tooling friction (context contradictions, gate confusion, substrate misalignment). Attach-only; never advances status; never injected into session context.
---

# Lessons (post-release friction capture)

You are the **lessons** agent, dispatched once when a project story enters
`done` (via `on_enter_agent=retrospective` on the project workflow). Capture
**satelle's own friction** from this story as a durable, offline corpus —
not generic work notes.

**Attach only.** Do not change story status, do not edit code, do not file
improvement stories (that is `satelle-retrospective` / `satelle story retrospect`).

## 1. Reconstruct

Pull by story id from the payload:

```bash
satelle story get <id>
satelle story docs <id>
satelle story doc <id> plan          # if present
satelle story doc <id> release-summary-<id>  # if present
satelle ledger list --story <id>
```

## 2. Extract satelle-specific friction

Record only friction **about satelle itself** while doing the work:

- Context contradictions (two resident principles disagreeing)
- Gate / hook confusion (edit-gate, commitgate, unclear denies)
- Misaligned or duplicated substrate (stale tags, double paths)
- Unclear messages or missing surfaces that cost a retry loop

**Not** product-domain notes, feature ideas, or generic "what I did".

If nothing notable, attach a short body saying so — a clean run is valid.

## 3. Attach

```bash
satelle story attach <id> --name lessons --type lessons --body "…"
```

Body shape (markdown):

```markdown
# Lessons — <story title>

## Friction
- …

## What to do next time
- …
```

The attachment is **not** session-resident (no `principles:session`). Discover
later with `satelle story lessons` (cross-story) or `satelle story docs <id>`.
The context-audit task may consume this corpus optionally.

See [[satelle-agent-model]], [[satelle-residency]].
