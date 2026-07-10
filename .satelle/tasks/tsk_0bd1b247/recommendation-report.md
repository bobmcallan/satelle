---
name: recommendation-report
type: audit
title: Reviewer skills — recommendation report (primary objective + DO/ACTIONS)
description: Recommendation report for tsk_0bd1b247. Corpus = name *review* OR tags type:reviewer. Scores OK|MISSING|MISALIGNED; ordered recommendations. Judge-and-report only.
timestamp: '2026-07-10T06:45:00Z'
implemented: '2026-07-10 recommendations applied (skill rewrites)'
tags: [audit, reviewer, recommendation, task:tsk_0bd1b247]
---

# Recommendation report — reviewer primary objective

| Field | Value |
|-------|--------|
| **Task** | `tsk_0bd1b247` |
| **Skill** | `@skill:satelle-reviewer-objective-audit` |
| **Report path** | `.satelle/tasks/tsk_0bd1b247/recommendation-report.md` |
| **Corpus rule** | basename contains `review` **OR** tags include `type:reviewer` |
| **Mode** | Judge and report only — **no** skill rewrites |

## Primary objective (required)

> **Given what was presented for this edge, may we advance?**

Bound by story + generated artifacts + updated code. Never create-and-complete;
never create-and-match a private plan/code.

---

## Summary

### Project `.satelle/skills/` (18 included)

| Status | Count |
|--------|------:|
| **OK** | 9 |
| **MISSING** | 3 |
| **MISALIGNED** | 4 |
| **Mechanism note** | 2 |
| With DO/ACTION (LLM “do”) | 0 |
| With DO/ACTION in ```check``` / reject guidance | 2–3 |

### Embedded `internal/config/substrate/skills/` (8 included, widen)

| Status | Count |
|--------|------:|
| **OK** | 6–7 (lean intent **OK**) |
| **MISSING** (partial) | 1 (done-review) |
| **MISALIGNED** | 0 on embedded intent (project override is the problem) |

### Included files (complete list)

**Project (18):**  
`satelle-code-ac-review`, `satelle-estimate-actual-review`, `satelle-integration-check` *(type:reviewer)*, `satelle-integration-review`, `satelle-plan-config-over-code-review`, `satelle-story-blocked-review`, `satelle-story-cancel-review`, `satelle-story-code-review`, `satelle-story-create-review`, `satelle-story-deploy-review`, `satelle-story-done-review`, `satelle-story-integration-review`, `satelle-story-intent-review`, `satelle-story-plan-review`, `satelle-story-release-review`, `satelle-substrate-only-check` *(type:reviewer)*, `satelle-task-validate-after-review`, `satelle-task-validate-before-review`

**Embedded (8):**  
`satelle-estimate-actual-review`, `satelle-story-blocked-review`, `satelle-story-cancel-review`, `satelle-story-done-review`, `satelle-story-intent-review`, `satelle-substrate-only-check`, `satelle-task-validate-after-review`, `satelle-task-validate-before-review`

---

## Per-file (project)

### OK

| Skill | Why | DO/ACTIONS |
|-------|-----|------------|
| `satelle-story-blocked-review` | Validates presented reason | none |
| `satelle-story-cancel-review` | Validates presented reason | none |
| `satelle-story-create-review` | Judges presented draft | none |
| `satelle-story-release-review` | Judges recorded evidence + ACs | none |
| `satelle-estimate-actual-review` | Tag presence (coded) | none (operator hint OK) |
| `satelle-task-validate-before-review` | Structural parent (coded) | none |
| `satelle-task-validate-after-review` | ACTION/VERIFICATION evidence | none |
| `satelle-integration-check` | Mechanism: suite exit code | ```check``` run only |
| `satelle-story-integration-review` | Mechanism: suite exit code | ```check``` run only |

### MISSING

| Skill | Why | DO/ACTIONS | Recommendation |
|-------|-----|------------|----------------|
| `satelle-story-code-review` | Tech-lead taste; not “presented vs ACs only” | none | Retarget or archive vs code-ac-review |
| `satelle-integration-review` (LLM) | Weak bind of tests to story/change | none | Lead with presented tests ↔ ACs |
| `satelle-story-done-review` | Partial: can re-open ACs without upstream artifacts | none | Residual AC + recorded evidence only |

### MISALIGNED

| Skill | Why | DO/ACTIONS | Recommendation |
|-------|-----|------------|----------------|
| **`satelle-story-plan-review`** | “Sound/plausible/coherence” → create-and-match risk; no ban on competing plan | none | Rewrite: locate plan → AC claims on **presented** → falsify only → verdict |
| `satelle-code-ac-review` | Partial: DRY / “ready to commit” overreach | none | Frame as presented code/tests vs ACs |
| `satelle-story-intent-review` (project) | Multi-axis co-authoring of draft | none | Slim toward embedded; reject = failed check only |
| `satelle-plan-config-over-code-review` | Re-design vs principle ideal | none | Quote plan lines only; no re-plan |

### Mechanism

| Skill | Why | DO/ACTIONS |
|-------|-----|------------|
| `satelle-story-deploy-review` | Functional check **is** the decision | ```check``` install/serve — document as mechanism |
| `satelle-substrate-only-check` | Path set for story commits | Reject text guides executor (OK) |

---

## Per-file (embedded) — deltas

| Skill | Objective | Note |
|-------|-----------|------|
| `satelle-story-intent-review` | **OK** | Lean base bar only — prefer over fat project override |
| `satelle-story-done-review` | **MISSING** (partial) | Same residual risk as project |
| Others listed | **OK** | Align with project OK set |

---

## Cross-cutting invariant (paste into every LLM reviewer)

```markdown
## Primary objective
Validate the **presented** outcome for this edge against the **story**
(and bound artifacts/code). Answer only: may we advance?
Do **not** create-and-complete this step. Do **not** invent a competing
plan/code/release and match against it.
```

---

## Ordered recommendations (separate work)

1. **Rewrite** `.satelle/skills/satelle-story-plan-review.md` (priority).  
2. Tighten `satelle-code-ac-review`; archive/retarget `satelle-story-code-review`.  
3. Slim project `satelle-story-intent-review` toward embedded system default.  
4. Bound or fold `satelle-plan-config-over-code-review`.  
5. Clarify `satelle-story-done-review` residual vs re-open.  
6. Optionally promote fixed plan-review into embedded substrate when stable.

---

## Out of scope

| Path | Why |
|------|-----|
| `satelle-reviewer-objective-audit` | `type:audit` (runner, not gate) |
| `code`, `plan`, `integrate`, `release`, … | Executor; no name/tag match |
| `satelle-workflow-advisor`, `satelle-step-summary` | Not reviewer gates |
| `skills/testdata/*` | Fixtures |
