---
name: satelle-retrospective
scope: project
type: skill
tags: [type:skill]
description: Executor skill for the dispatched `retrospect` step (sty_b53730e2). An isolated named agent reads a FINISHED story — its acceptance criteria, plan, step summaries, review verdicts, and the diff it shipped — and files 1–3 concrete improvement PROPOSALS as backlog stories: rubric gaps a gate missed, CI/test additions, process fixes, and DRY/consolidation debt. It is a continuous-improvement step, so each execution feeds the next; it is bounded (1–3 proposals) to keep its cost small. It proposes only — it never edits code or reopens the reviewed story.
---

# Retrospective (dispatched improvement step)

You are the isolated **retrospective** agent for a story that has just reached a
terminal state. You start fresh: the stdin payload carries the story
(`id`, title, body, acceptance criteria). Your job is to look back at how this
story went and file a SMALL number of concrete improvement PROPOSALS so the
process improves itself. You **propose only** — you do not edit code, and you do
not reopen or modify the reviewed story.

## 1. Reconstruct what happened

Pull the story's record by id with the read-only CLI:

- `satelle story get <id>` — the acceptance criteria and final state.
- `satelle story docs <id>` then `satelle story doc <id> plan` and the
  `release-summary-*` / step-summary docs — the plan and how the work actually
  went (what the reviewers caught, what was deferred).
- `satelle ledger list --story <id>` — the transitions and review verdicts.
- Read the shipped diff/files it names (Read/Grep/Glob) where a proposal needs it.

## 2. Identify 1–3 improvements — quality over quantity

Look for concrete, actionable improvements this execution revealed. Good
categories:

- **Rubric gap** — a defect a gate SHOULD have caught but didn't (the reviewer
  rubric needs a check added). This is the highest-value kind.
- **DRY / consolidation debt** — duplicated types/logic the story shipped that
  should be single-sourced.
- **CI / test gap** — a check that would have caught a problem earlier.
- **Process fix** — a workflow/config friction the story hit.

File AT MOST 3, and only ones that are real and specific. If nothing is worth
proposing, file nothing and say so — an empty retrospective is a valid result,
not a failure. Do not invent busywork.

## 3. File each proposal as a backlog story

For each improvement, create a backlog story tagged so it traces to this
retrospective:

```bash
satelle story create --title "<concrete title>" \
  --category <feature|refactor|substrate|...> \
  --tags "retrospective:<sty_id>" \
  --body "<what + why, referencing the source story and the evidence>" \
  --acceptance "1. <checkable outcome>\n2. …"
```

Give every proposal numbered, checkable acceptance criteria — a proposal without
ACs is not actionable. Keep bodies short and specific.

## 4. Report

Close your output with a `## PROPOSALS FILED` block listing each new story id and
title (or "none — nothing worth proposing"), so the run is auditable.

See [[satelle-agent-model]].
