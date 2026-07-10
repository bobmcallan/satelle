---
id: tsk_reviewer-objective-audit
type: task
status: done
priority: medium
category: substrate
tags: reviewer-audit, quality, substrate
created: 2026-07-10T00:00:00Z
updated: 2026-07-10T00:00:00Z
---

# Audit all reviewer skills: primary objective + strip DO/ACTIONS

Re-runnable audit of every satelle **reviewer** skill for primary-objective
alignment and DO/ACTION drift. Seeded by `satelle init`. Judge and report only —
edit a skill solely if separately asked. New execution each run:

```bash
satelle execution create --parent tsk_reviewer-objective-audit --title "Run: reviewer objective audit"
```

## Skill

Follow **@skill:satelle-reviewer-objective-audit** as the sole rubric.

## Corpus

**All** skills under `.satelle/skills/` that match **either**:

- basename contains `review` (`*-review`, `*_review`, …), **or**
- frontmatter tags include `type:reviewer`

Exclude this audit skill (`type:audit`). Workflows out of scope.

## Action

1. Enumerate the corpus (name **or** `type:reviewer`).
2. Score each: principle objective **OK | MISSING | MISALIGNED**; list **DO/ACTIONS
   to remove**; bound-input notes.
3. Write the **recommendation report** to:
   **`.satelle/tasks/tsk_reviewer-objective-audit/recommendation-report.md`**
4. JUDGE AND REPORT ONLY — do not edit reviewer skills unless separately asked.

## Acceptance Criteria

1. Skill `satelle-reviewer-objective-audit` is present and used as the rubric.
2. Every skill matching name-`*review*` or `type:reviewer` appears in the report
   with OK|MISSING|MISALIGNED and DO/ACTIONS (or none).
3. Recommendation report is under
   `.satelle/tasks/tsk_reviewer-objective-audit/recommendation-report.md`
   with summary counts and ordered recommendations.
4. Re-runnable from `done` (new execution per run).
