---
id: tsk_f6a98b87
type: task
status: backlog
priority: medium
category: substrate
tags: substrate-audit, quality
created: 2026-07-07T04:05:52Z
updated: 2026-07-07T04:05:52Z
---

# Audit every skill and principle for focus, token economy, and repo-agnostic fitness

Re-runnable substrate-quality audit institutionalizing the glm-maturation skill audit as a durable authored task, extended to cover PRINCIPLES as well as skills. Built on the satelle-substrate-audit approach (which audits the EMBEDDED defaults under internal/config/substrate); this task WIDENS the corpus to every skill AND principle, both AUTHORED (.satelle/skills, .satelle/principles) and EMBEDDED (internal/config/substrate/{skills,principles}), and judges each on the same three axes. Run it on demand (a new execution each time) — after adding/editing a skill or principle, or when a description feels bloated.

## The three axes (judge every file on all three)
1. FOCUS — one responsibility; no overlap with a sibling (two files doing the same job = merge or delineate).
2. TOKEN ECONOMY — the always-resident frontmatter description first (it taxes every session), then the body; cut ceremony, keep meaning.
3. REPO-AGNOSTIC FITNESS — the constitution's test: would this still make sense if ANOTHER repo installed satelle? Flag this-repo story ids, deploy mechanics, dead tags, and opinions that belong in .satelle/ rather than in an embedded default.

## How to run
- Enumerate the corpus: .satelle/skills/*.md, .satelle/principles/*.md, internal/config/substrate/skills/*.md, internal/config/substrate/principles/*.md (workflows are DAGs — out of scope).
- Ground the audit in [[satelle-constitution]] (session context), .satelle/principles/satelle-repo-agnostic.md, and — for reviewer files — the reviewer-skill-author skill's review-only contract (defer to it; don't re-derive).
- Report findings per file: axis + severity (blocker breaks the product/a gate/repo-agnosticism · tighten wastes tokens · nit) + a concrete fix. JUDGE and PROPOSE only — edit a file solely if separately asked.

## Acceptance Criteria

1. Every skill AND principle — authored (.satelle/skills, .satelle/principles) and embedded (internal/config/substrate/{skills,principles}) — is audited on all three axes: single-responsibility focus, token economy, repo-agnostic fitness.
2. Findings are reported per file with the axis, a severity (blocker/tighten/nit), and a concrete fix; no file is edited unless separately requested (judge-and-propose, per satelle-substrate-audit).
3. The task is authored substrate (file-primary under .satelle/tasks/), resolves via the task workflow, and is re-runnable from done (a new execution per run).
