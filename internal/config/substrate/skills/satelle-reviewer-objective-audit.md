---
name: satelle-reviewer-objective-audit
scope: system
type: skill
tags: [type:skill, type:audit]
description: Audit ALL reviewer skills (name *review* or tag type:reviewer) for primary-objective alignment (validate presented outcome — never create-and-complete) and DO/ACTIONS to remove. Writes a recommendation report under the running task's folder. Seeded by init.
---

# Reviewer primary-objective audit

Audit every **reviewer** skill against the process model: **reviewer-first gates
validate the step's presented outcome; they do not create-and-complete the step.**

A task **runs** this skill and writes a **recommendation report** under that task's
folder. Do not implement product code or rewrite reviewers mid-audit unless a
separate story/task asks for fixes.

## Primary objective (required of every reviewer)

A reviewer answers only:

> **Given what was presented for this edge, may we advance?**

Bound inputs (as relevant to the edge):

1. **Story** — title, body, acceptance criteria (and tags/parent when needed)
2. **Generated artifacts** — plan attach, step summaries, release summary, ledger evidence
3. **Updated code** — working tree / tests when the edge is a code or test gate

A reviewer **MUST NOT**:

- Create-and-complete the step (write a plan, implement code, run a release, invent tests as the standard)
- Use a **private re-plan / re-design / re-implement** as the acceptance standard (create-and-match)
- Instruct the agent to Edit/Write/fix/format/stage/commit/push or otherwise **do** the work

**Mental simulation** is allowed. Turning it into a competing artifact or the bar
for accept is **MISALIGNED**.

## Corpus (include rule)

Enumerate skills under `.satelle/skills/` (default). **Include** a file if **either**:

1. **Name** matches a reviewer skill — basename contains `review` (e.g. `*-review`,
 `*_review`, `satelle-*-review`), **or**
2. Frontmatter **tags** include `type:reviewer`

**Exclude:**

- This audit skill (`type:audit` / `satelle-reviewer-objective-audit`)
- Fixtures under `skills/testdata/`
- Pure executor skills that match neither rule (`code`, `plan`, `integrate`, …)

A repo task **may** widen roots (e.g. also scan embedded sources in a satelle
dev tree). The embedded default corpus is **`.satelle/skills` only** (repo-agnostic).

## How to audit each included file

For **each** file, record:

### 1. Principle objective — `OK` | `MISSING` | `MISALIGNED`

| Status | When |
|--------|------|
| **OK** | Validates presented outcome vs story/bound evidence → verdict only |
| **MISSING** | No clear primary objective; vague quality/tech-lead taste |
| **MISALIGNED** | Create-and-complete or create-and-match implied |

### 2. DO / ACTIONS — list to **remove** (or rewrite to pure judgment)

Flag instructions to create/write/edit/fix/commit/push/implement/ship. 
Allowed: Read/Grep/Glob; non-mutating or declared functional ```check``` that only
**decides**; reject notes for the **executor**.

### 3. Bound-input discipline

Must require locating the **presented** artifact for the edge when one exists.

### 4. Fair gate

No requirements beyond stated ACs / step outcome (except explicit intake rules).

## Recommendation report (required output)

Write **one** recommendation report **under the task folder of the run**:

```text
.satelle/tasks/<task_id>/recommendation-report.md
```

Examples:

- parent `<task_id>` → `.satelle/tasks/<task_id>/recommendation-report.md`
- default seed task → `.satelle/tasks/tsk_reviewer-objective-audit/recommendation-report.md`

Do **not** put the report under `.satelle/documents/` unless a repo override says so.

Report contents:

1. **Summary** — counts OK / MISSING / MISALIGNED; DO/ACTION file count 
2. **Per-file** — path, edge, objective status + why, DO/ACTIONS to remove, proposed rewrite 
3. **Cross-cutting** — shared primary-objective invariant for all reviewers 
4. **Recommendations** — ordered fix list (separate work; this run does not edit skills) 
5. **Out of scope** — skipped files and why 

**Judge and report only** unless a separate request asks to edit skills.

## Verification for the run

- Every corpus file appears in the report 
- Each has OK|MISSING|MISALIGNED and DO/ACTIONS (or none) 
- Report path is under `.satelle/tasks/<task_id>/` 

See [[satelle-agent-model]] and the review-only contract for gate skills.
