---
id: tsk_substrate-audit
type: task
status: done
priority: medium
category: substrate
tags: substrate-audit, quality
created: 2026-07-08T00:00:00Z
updated: 2026-07-08T00:00:00Z
---

# Audit this repo's skills and principles for focus, token economy, and fitness

A re-runnable, on-demand quality audit over THIS repo's authored skills and
principles. Judge and propose only — edit a file solely if separately asked. Run a
fresh execution each time (`satelle execution create --parent tsk_substrate-audit`);
the task sits at `done` (baseline established) and accepts a new run whenever you
re-run it.

## Corpus
`.satelle/skills/*.md` and `.satelle/principles/*.md` ONLY. Workflows are DAGs — out
of scope. (A repo that ships its own embedded substrate may widen the corpus to those
copies in its OWN authored override of this task; this embedded default stays
repo-agnostic.)

## The three axes (judge every file on all three)
1. FOCUS — one responsibility; no overlap with a sibling (two files doing the same
 job = merge or delineate).
2. TOKEN ECONOMY — the always-resident frontmatter `description` first (it taxes
 every session), then the body; cut ceremony, keep meaning.
3. FITNESS — correctly scoped to its kind; no dead tags, no overlap, prose that
 pulls its weight. Ground in `.satelle/principles/` (the constitution +
 `satelle-repo-agnostic` if present), and for reviewer files defer to the
 `reviewer-skill-author` skill's review-only contract rather than re-deriving.

## How to run
- Enumerate the corpus (`.satelle/skills/*.md`, `.satelle/principles/*.md`).
- Report findings per file: axis + severity (blocker breaks the product/a gate/
 repo-agnosticism · tighten wastes tokens · nit) + a concrete fix.
- JUDGE and PROPOSE only — edit a file solely if separately asked.

## Acceptance Criteria
1. Every authored skill AND principle is judged on all three axes: focus, token
 economy, fitness.
2. Findings are reported per file with the axis, a severity (blocker/tighten/nit),
 and a concrete fix; no file is edited unless separately requested.
3. Re-runnable from `done` (a new execution per run).
