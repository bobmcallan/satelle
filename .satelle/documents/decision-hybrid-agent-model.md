---
type: document
title: Decision — keep hybrid agent allocation (in-loop perform; dispatch plan/reviewers)
description: Recorded choice (A) for epic:workflow-review-followups / sty_e3687ec4. In-loop executor for implement/integrate/release; isolated dispatch for plan and reviewers.
tags: [document, decision, epic:workflow-review-followups, agent-model]
timestamp: '2026-07-12T00:00:00Z'
---

# Decision (A): keep the hybrid agent allocation

**Story:** `sty_e3687ec4` (order:1 of `sty_4603db29` / epic:workflow-review-followups)  
**Date:** 2026-07-12  
**Choice:** **(A) KEEP HYBRID** — already enacted; this document records it.

## Decision

| Step class | Allocation | Why |
| --- | --- | --- |
| **Perform** (`in_progress`, `integration`, `release`) | **In-loop** `agent=executor` on the driving session | Full session context, principles, and tools; cheaper orchestration; no pull-context reconstruction |
| **Plan** | **Dispatched** named agent (`agent=planner`) | Fresh context, skill forced as system prompt, read-only grant; entered from non-performing `backlog` |
| **Review gates** | **Dispatched** `agent=reviewer` (or named reviewer binding) | Clean-room judge; verdict contract; never mutates |

No rewiring story is filed. A repo that wants every performing step as a subprocess can still rebind via workflow `agent=` + `agents.toml` — that is configuration, not the default for this project.

## Trade-offs weighed

| | In-loop executor | All-subprocess perform |
| --- | --- | --- |
| Context | Full driving session (history, prior steps, operator intent) | Isolated; must pull-context by id |
| Skill injection | Rubric is a **declaration** the session follows | Rubric forced as system prompt |
| Cost / latency | One session continues | Spawn + payload + grant per step |
| Isolation | Weaker (session can drift from rubric) | Stronger; gates still enforce outcome |
| Orchestration | Simple status transitions from the same session | Heavier dispatch for every stage |

Choice (A) keeps isolation where it pays (plan + reviewers) and keeps perform steps continuous so implement → integrate → release share context without re-deriving the slice each time.

## Sources of truth (do not duplicate)

1. **Workflow allocation** — `.satelle/workflows/satelle-project-workflow.md`  
   Prose and DOT: `plan [agent=planner, …]`; `in_progress` / `integration` / `release` are `[agent=executor, prompt="@skill:…"]` with explicit “in-loop / no isolated worker” wording.

2. **Run modes** — principle `satelle-agent-model`  
   Defines both modes (in-loop executor vs isolated invocation), role grants, and `@skill:` as a declaration — not a claim that this repo dispatches every perform step.

Adjacent locked decisions (role contract, verdict contract, no config policing of grants/models) live under epic:agent-invoke-unify / `agent-invoke-unify-role-contract.md`.

## Non-goals

- No change to the binary or default embedded workflows for this decision alone.
- No requirement that every repo keep this hybrid; the model permits `agent=<name>` on perform steps.
- No second agent-model essay — point at the principle and the project workflow.
