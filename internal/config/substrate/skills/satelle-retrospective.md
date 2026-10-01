---
name: satelle-retrospective
scope: system
type: skill
tags: [type:skill]
description: Executor skill for the dispatched retrospective step — an isolated agent reads a FINISHED story (ACs, plan, step summaries, verdicts, shipped diff) and files 1–3 improvement PROPOSALS as backlog stories. Proposes only; never edits code or reopens the reviewed story. A proposal naming an epic that already closed is filed without the epic tag and with a body note.
---

# Retrospective (dispatched improvement step)

You are the isolated **retrospective** agent for a story that just reached a
terminal state. You start fresh: the stdin payload carries the story (`id`,
title, body, acceptance criteria). Look back at how the story went and file a
SMALL number of concrete improvement PROPOSALS so the process improves itself.
**Propose only** — do not edit code, do not reopen or modify the reviewed story.

## 1. Reconstruct what happened

Pull the story's record by id with the read-only CLI:

- `satelle story get <id>` — the acceptance criteria and final state.
- `satelle story docs <id>`, then `satelle story doc <id> <name>` for the plan
  and the step summaries — how the work actually went (what reviewers caught,
  what was deferred).
- `satelle ledger list --story <id>` — the transitions and review verdicts.
- Read the shipped diff/files (Read/Grep/Glob) where a proposal needs it.

`story docs` and `ledger list` may print a compact table instead of a JSON
array (`satelle help compact-output`); `--json` forces the plain form.

## 2. Identify 1–3 improvements — quality over quantity

Good categories:

- **Rubric gap** — a defect a gate SHOULD have caught but didn't (highest
  value — the reviewer rubric needs a check added).
- **DRY / consolidation debt** — duplicated types or logic the story shipped
  that should be single-sourced.
- **CI / test gap** — a check that would have caught a problem earlier.
- **Process fix** — a workflow or config friction the story hit.

File AT MOST 3, only ones that are real and specific. If nothing is worth
proposing, file nothing and say so — an empty retrospective is a valid result,
not a failure. Do not invent busywork.

## 3. File each proposal as a backlog story

Tag each so it traces to this retrospective:

```bash
satelle story create --title "<concrete title>" \
  --category <feature|refactor|substrate|...> \
  --tags "retrospective:<sty_id>" \
  --body "<what + why, referencing the source story and the evidence>" \
  --acceptance "1. <checkable outcome>\n2. …"
```

Give every proposal numbered, checkable acceptance criteria — a proposal without
ACs is not actionable. Keep bodies short and specific.

**Epic tag.** A proposal filed from a child of an epic may carry that epic's
`epic:<theme>` tag. A child retrospective usually runs while the epic-parent is
still open, and then create keeps the tag. If that parent is already done or cancelled,
`satelle story create` files the proposal WITHOUT the `epic:` tag
(every other tag is kept) and appends a note to the body naming the closed
container. It is
filed, not refused: the proposal is real work, and a closed epic is no home for
it. A proposal is never stamped onto a closed container, and never reopens one.
An epic still open keeps the tag.

## 4. Report

Close with a `## PROPOSALS FILED` block listing each new story id and title
(or "none — nothing worth proposing"), so the run is auditable.

See [[satelle-agent-model]] and [[satelle-story-classification]].
