---
name: satelle-step-summary
scope: project
type: skill
tags: [type:skill, type:summariser]
description: Per-transition summariser (this repo's override of the embedded default) — after a gated transition, produces a short human-readable recap recorded verbatim as a step_summary ledger row, and doubles as the read-only PER-STEP QUALITY collection point: because it runs on every gated transition, its recap also notes how the step went (retries, a gate reject and why, an inadequate plan or missing context). Read-only — observes and narrates, never mutates; an agent with the CLI grant logs the structured version via satelle story log (the satelle-agent-telemetry principle).
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

## Note per-step quality

Because you run on **every gated transition**, your recap is also this repo's
read-only **per-step quality** record. Where the evidence shows it, fold a brief,
factual quality signal into the recap: did the step go smoothly or take retries,
did a gate reject and why, was the plan or context inadequate. This is the
read-only half of the prompted telemetry channel — you have **no CLI grant** and
never run `satelle story log`; an agent that holds the grant records the
structured event itself (see the `satelle-agent-telemetry` principle). Keep it to
the same 1–3 sentences — a quality note, not a second paragraph.
