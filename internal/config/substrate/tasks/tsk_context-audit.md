---
id: tsk_context-audit
type: task
status: done
priority: medium
category: substrate
tags: context-audit, quality, substrate
created: 2026-07-12T00:00:00Z
updated: 2026-07-12T00:00:00Z
---

# Audit session context against the substrate (contradictions, bloat, misplacement)

A re-runnable audit that compares the context the agent **actually** receives at
SessionStart (`satelle hook context`) against what the `.satelle` substrate
intends. Catches resident-set contradictions, bloat over the SessionStart
ceiling, placement drift, and mis-tagged principles. Seeded by `satelle init`.

Judge and report only — edit substrate only if separately asked. New execution
each run:

```bash
satelle execution create --parent tsk_context-audit --title "Run: context audit"
```

## Skill

Follow **@skill:satelle-context-audit** as the sole rubric.

## Corpus

- **Actual injection** — output of `satelle hook context` (constitution + every
  `principles:session` body + on-demand pointer).
- **Intended substrate** — `.satelle/principles/*.md` (and related residency
  markers), plus the deterministic placement surface `satelle principle validate`.
- **Optional** — lessons corpus when present (typed lessons attachments /
  documents); absence is not a failure.

## Action

1. Capture `satelle hook context`.
2. Run the functional check: `satelle principle validate`.
3. Semantic pairwise pass over resident principle bodies (contradiction, bloat,
   misplacement) and coverage diff vs substrate.
4. Write the recommendation report under
   `.satelle/tasks/tsk_context-audit/recommendation-report.md` (or the running
   execution's task folder when executed as a child run).
5. JUDGE AND REPORT ONLY.

## Acceptance Criteria

1. The run captures real `satelle hook context` output and diffs it against the
   substrate (coverage + semantic classes).
2. A seeded resident-set contradiction (conflicting session-tagged principles)
   is flagged; a fixed pair is clean.
3. Deterministic findings (missing `embedded_sha`, over-ceiling resident set,
   scope/marker misplacement) surface via `satelle principle validate`.
4. Task and skill ship as repo-agnostic embedded defaults; resolve via the task
   workflow; no dogfood-repo story ids in the rubric.
5. Lessons corpus is optional input when present — not required for a green run.
