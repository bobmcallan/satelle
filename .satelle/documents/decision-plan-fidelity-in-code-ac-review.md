---
type: document
title: Decision — plan fidelity in code-ac-review is a hard gate when a plan exists
description: sty_ca97c680 — hard-reject clear plan ignore without a plan-defect note; never invent a competing design.
tags: [document, decision, epic:workflow-review-followups, reviewer]
timestamp: '2026-07-12T00:00:00Z'
---

# Decision: plan fidelity in `satelle-code-ac-review`

**Story:** `sty_ca97c680` (order:4 of epic:workflow-review-followups)  
**Date:** 2026-07-12  
**Choice:** **Hard-reject** when a plan attachment exists and the presented tree clearly ignores the plan's named slice **without** a plan-defect note.

## Why hard (not advisory-only)

- `plan → in_progress` already gates plan-vs-ACs; without a later check, plan fidelity is prompt-only and silently optional.
- Hard reject is still **fair**: only the plan's named slice is in scope; the reviewer must not invent a better design.
- Escape hatch: if the plan is wrong, the executor **notes the plan defect** and implements the AC-correct approach — that accepts.

## Bar (shared by implementers and reviewers)

| Situation | Verdict |
| --- | --- |
| No plan attachment | Skip plan check; ACs + tests only |
| Tree matches plan's named slice (+ ACs) | Accept (plan fidelity) |
| Plan wrong; executor notes defect; ACs met | Accept |
| Tree ignores plan's named slice; no defect note | **Reject** — name `plan fidelity` |

Implemented in `.satelle/skills/satelle-code-ac-review.md` (including a short worked example). No binary change.
