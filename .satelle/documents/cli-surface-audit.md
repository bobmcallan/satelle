---
type: document
title: CLI surface audit — verb and alias keep/rename/remove
description: Enumerates every registered verb and cobra alias with a keep/rename/remove decision for the breaking CLI surface simplification (epic agent-operability order:5).
---

# CLI surface audit

One spelling per action. Breaking removals fail closed naming the replacement;
binary/repo drift with a changelog `### Breaking` entry requires `satelle init`.

## A. Registered verbs (internal/verb) — default KEEP

| Verb | Decision | Rationale |
| --- | --- | --- |
| story-create/get/list/set | KEEP | Primary story surface |
| story-estimate/actual | KEEP | Cost telemetry |
| story-resummarise/retrospect/restamp | KEEP | Story maintenance |
| story-stop-request | KEEP | Engagement arbitration |
| story-seat-list / story-seat-release | KEEP | Seat surface (agent-operability) |
| story-doc-attach / story-docs / story-doc / story-lessons-list | KEEP | Attachments |
| story-log / story-cost / story-sync | KEEP | Telemetry / reconcile |
| task-* / execution-* | KEEP | Task/execution surface |
| task-archive / execution-record | KEEP | Task lifecycle |
| ledger-append / ledger-list | KEEP | Evidence |
| changelog | KEEP | Agent-reviewable release delta |
| version | KEEP | Build identity |
| doc-list / doc-get | KEEP | Authored substrate |

CLI groups map 1:1 via `dispatch` — no rename of primary verb names.

## B. Cobra aliases — REMOVE (breaking)

| Alias path | Replacement | Decision | Rationale |
| --- | --- | --- | --- |
| `satelle install` | `satelle init` | **REMOVE** | Collides with `satelle service install`; one enable verb |
| `satelle workspace rm` | `satelle workspace remove` | **REMOVE** | Prefer full word; low harm |
| `satelle sync config pull` | `satelle sync config deploy` | **REMOVE** | One spelling for personal config deploy |

## C. Collision spellings (record only — out of scope unless noted)

| Spelling | Notes |
| --- | --- |
| `status` vs `service status` | Different groups; KEEP both |
| top `sync` vs `story sync` | Different objects; KEEP both |
| `push` under sync/publish | Different groups; KEEP |
| multiple `validate` spellings | Kind-scoped + top-level; KEEP |
| `story sync` vs top `sync` | Not aliases; no change |

## D. Legacy dual-reads / field aliases — REMOVE

| Surface | Decision | Replacement / heal |
| --- | --- | --- |
| `actors.toml` as agents file | Already refuse | Rename to `agents.toml` or `satelle init` |
| `harness=` field | **REMOVE** fallback | Use `command=`; `MigrateAgents` renames |
| `inject_principles=` | **REMOVE** fallback | Use `principles=`; migrate on init |
| Nested `[agents.NAME]` | **REMOVE** load path after migrate | Flat `[NAME]`; MigrateAgents flattens |

## E. Drift require-init

| Mechanism | Decision |
| --- | --- |
| `.satelle/deployed.version` | Written by init/rebase/restore |
| Breaking gap (deployed, binary] | Fail closed → `satelle init` |
| Dev builds (`0.0.0-dev` / empty) | Never gate (dogfood) |
