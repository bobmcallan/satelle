---
id: tsk_substrate-simplify-audit
type: task
status: done
priority: low
category: substrate
tags: substrate-audit, quality
created: 2026-10-12T00:00:00Z
updated: 2026-10-12T00:00:00Z
---

# Propose terser rewrites for the constitution, principles, skills and workflows

A re-runnable, on-demand pass that reads the substrate this repo runs (embedded
and authored) and PROPOSES terser rewrites that keep every protected token and
rule. Proposal only: nothing is edited, and the report is for a human or a
separate story to apply. Run a fresh execution each time
(`satelle execution create --parent tsk_substrate-simplify-audit`); the task sits
at `done` and accepts a new run whenever you want one.

## Skill

Follow **@skill:satelle-substrate-simplify-audit** as the sole rubric.

## Corpus

The constitution, principles, skills and workflows `satelle substrate list
--effective` shows, judged against the protected-token rules
`satelle substrate condense` prints.

## Acceptance Criteria

1. Every proposal quotes the original passage, gives the rewrite and its saving,
 and lists the protected tokens kept.
2. No file is edited and no mutating `satelle` verb is run.
3. Re-runnable from `done` (a new execution per run).
