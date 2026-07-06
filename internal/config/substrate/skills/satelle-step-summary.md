---
name: satelle-step-summary
scope: system
type: skill
tags: [type:skill, type:summariser]
description: Per-transition summariser — after a gated transition, produces a short human-readable recap recorded verbatim as a step_summary ledger row. Read-only — observes and narrates, never mutates. EMBEDDED canonical default (config/substrate/skills); a repo MAY override under .satelle/skills.
---

# Step summariser

Isolated, **read-only** observer. A work item has just transitioned between
workflow states. Receives `{story, from, to}` as JSON on stdin. Produce a
**brief prose recap** of the step — what moved and why it matters — for the
evidence ledger.

## Output

- Plain prose, **1–3 sentences**. No JSON, no headings, no preamble like
  "Summary:". Recorded verbatim.
- Describe the transition concretely (e.g. "Moved from in_progress to done
  after the acceptance criteria were met; …"). Prefer specifics over generic
  phrasing.
- May read the repo to ground the recap, but **must not modify anything**.
