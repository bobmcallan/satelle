---
name: step
type: workflow
scope: system
tags: [type:workflow]
description: Order-zero step catalogue every repo inherits — the steps and always-on gates a route selects from by obligation. A step declares what it provides, what it requires, who performs it and the reviewers gating ENTRY to it. Half of a derived route; done.md is the other half.
---

# Step catalogue

A step carries no executor rubric: the binary ships no opinion about how work is
done. A repo names one with `skills:` when it authors one.

<!-- HOW TO READ THIS FILE (and its other half). -->
<!-- -->
<!-- done.md  — WHAT must be true before work closes, per category. -->
<!-- step.md  — this file: WHICH step discharges each obligation, and who gates -->
<!--            entry to it. -->
<!-- -->
<!-- A `##` heading is a STAGE NAME — the status an item holds while in that -->
<!-- step — NOT the step's identity. Headings repeat on purpose: `done` appears -->
<!-- three times below and `in_progress` twice, because different routes reach -->
<!-- the same status by discharging different obligations. -->
<!-- -->
<!-- A step's identity is its `provides:` key. done.md lists obligations; the -->
<!-- binary selects the steps whose `provides:` matches and orders them by -->
<!-- `requires:`. So a section belongs to whichever done.md sections name its -->
<!-- `provides:` — which is what the comment on each record below records, so a -->
<!-- reader never has to derive it. -->
<!-- -->
<!--   provides:       the obligation this step discharges (its identity) -->
<!--   requires:       obligations discharged first — this is what sets ORDER -->
<!--   agent:          who performs it; `reviewer` marks a step that judges only -->
<!--   skills:         the executor rubric (none ship; a repo names its own) -->
<!--   reviewers:      gates on ENTRY to this step, all-must-accept -->
<!--   reviewer_agent: the agents.toml binding those gates run under -->
<!--   parallel:       entry-gate concurrency cap; 0 forces them to run serially -->
<!--   start:/terminal: route endpoints -->
<!-- -->
<!-- The catalogue is SHARED. A step listed here runs only for the categories -->
<!-- whose done.md section names its obligation, so adding a step here does not -->
<!-- put it on any route until an obligation asks for it. -->

<!-- ================================================================ -->
<!-- THE WORKING LANE — selected by done.md `## *` -->
<!-- raised -> coded -> closed -->
<!-- ================================================================ -->

## backlog
<!-- provides raised · the entry state for EVERY section: the working lane, both -->
<!-- container sections, and both task-run sections all open here. -->
start: true
provides: raised

## in_progress
<!-- provides coded · selected by `## *`. Carries no `skills:` — the binary ships -->
<!-- no executor rubric, so a repo names its own here when it authors one. -->
agent: executor
reviewers: satelle-story-intent-review
reviewer_agent: reviewer
provides: coded
requires: raised

## done
<!-- provides closed · selected by `## *`. Three reviewers judge entry, and -->
<!-- `parallel: 0` is authored deliberately: they run SERIALLY, against the -->
<!-- concurrent default for a multi-reviewer step. -->
reviewers: satelle-workflow-change-review, satelle-story-scope-review, satelle-story-done-review
reviewer_agent: reviewer
parallel: 0
terminal: true
provides: closed
requires: coded

<!-- ================================================================ -->
<!-- THE CONTAINER CLOSE — selected by `## epic-parent` and `## parent` -->
<!-- raised -> children-resolved -->
<!-- ================================================================ -->

## done
<!-- provides children-resolved · selected by both container sections. Nothing -->
<!-- performs on this lane, so `agent: reviewer` marks it as judging only; the -->
<!-- gate is handed a snapshot of the children to judge from. It requires just -->
<!-- `raised`, which is why a container route is two steps long. -->
agent: reviewer
reviewers: satelle-story-done-review
terminal: true
provides: children-resolved
requires: raised

<!-- ================================================================ -->
<!-- THE TASK RUN — selected by `## execution` and `## task` -->
<!-- raised -> run -> run-verified -->
<!-- ================================================================ -->

## in_progress
<!-- provides run · selected by both task-run sections. Its gate validates the -->
<!-- run BEFORE it begins. -->
agent: executor
reviewers: satelle-task-validate-before-review
reviewer_agent: reviewer
provides: run
requires: raised

## done
<!-- provides run-verified · selected by both task-run sections. Its gate -->
<!-- validates the run AFTER it has happened, which is what makes a run verified -->
<!-- rather than merely finished. -->
reviewers: satelle-task-validate-after-review
reviewer_agent: reviewer
terminal: true
provides: run-verified
requires: run

<!-- ================================================================ -->
<!-- ALWAYS-ON GATES -->
<!-- -->
<!-- A gate occupies no stage; it fires on entry to steps. Its three scoping keys -->
<!-- are different axes, and confusing them is the usual authoring mistake: -->
<!-- -->
<!--   on:         which STEPS it fires on (`*` = every step) -->
<!--   for:        which done.md SECTIONS it belongs to. `for: *` means the -->
<!--               WILDCARD SECTION — the working lane — not "everything". -->
<!--               A gate with no `for:` belongs to every route. -->
<!--   applies_to: which ITEMS, by tag. -->
<!-- ================================================================ -->

## gate satelle-step-summary
<!-- Summarises transitions on the working lane and both container lanes. -->
<!-- -->
<!-- DECISION, not omission: `for:` does not name the task-run sections, so a run -->
<!-- gets no step summary — its own before/after validators are the record. -->
agent: reviewer
mandatory: true
for: *, epic-parent, parent

## gate satelle-estimate-actual-review
<!-- Fences the estimate on entry to in_progress and the actual on entry to done. -->
<!-- -->
<!-- DECISION, not omission: `for: *` only. Containers perform no work to -->
<!-- estimate, and a run is not estimated. -->
on: in_progress, done
for: *
