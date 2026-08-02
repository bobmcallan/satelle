---
name: done
type: workflow
scope: system
tags: [type:workflow]
create_review: satelle-story-create-review
description: Order-zero declaration of done every repo inherits — one section per category, listing the obligations discharged before work closes, plus the park and cancel states the binary synthesises. Half of a derived route; step.md is the other half. Selected by category name; `*` governs the rest.
---

# Definition of done

Order zero: one working lane, plus containers and task runs. A repo edits this
file rather than authoring a lifecycle from scratch — add obligations, add
sections, and step.md says what discharges them.

<!-- HOW TO READ THIS FILE (and its other half). -->
<!-- -->
<!-- done.md  — WHAT must be true before work is closed, per category. -->
<!-- step.md  — WHICH step discharges each obligation, and who gates entry to it. -->
<!-- -->
<!-- An obligation here is a plain word (`coded`). It links to a step by that -->
<!-- step's `provides:` key, and NEVER by the step's heading — headings are stage -->
<!-- names (the status an item holds), and steps deliberately share them. To find -->
<!-- where `coded` is discharged, search step.md for `provides: coded`. -->
<!-- -->
<!-- ORDER is derived, not authored: the binary topologically sorts the selected -->
<!-- steps by their `requires:`. The list order here is for the reader. A category -->
<!-- selects only the steps whose `provides:` it names, so a short list is a short -->
<!-- route — that is how a category opts out of work it does not need. -->
<!-- -->
<!-- The binary SYNTHESISES the rest of the topology from the keys below — park -->
<!-- from every non-terminal step, cancel from every non-terminal step, and the -->
<!-- backward edges for recover — so no section draws them by hand: -->
<!-- -->
<!--   park:    <state> @<gate> [advise <agent> @<skill>] -->
<!--   cancel:  <state> @<gate> -->
<!--   recover: <step> [from <step>, <step>] -->
<!-- -->
<!-- `@<gate>` is the reviewer that judges ENTRY to that state. An advisor is -->
<!-- CONSULTED by the orchestrator; entry to a state never dispatches an agent. -->
<!-- A `+ <tag> <obligation>` line APPENDS an obligation when the item carries the -->
<!-- tag. Tags can only add; nothing removes an obligation from a route. -->

<!-- ================================================================ -->
<!-- THE WORKING LANE — governs every category with no section of its own -->
<!-- raised -> coded -> closed -->
<!-- ================================================================ -->

## *
<!-- The default shape for work: it is raised, it is done, it is closed. Add -->
<!-- obligations here (and a step providing each in step.md) to grow the lane — -->
<!-- a plan step, a review step, a release step — rather than authoring a second -->
<!-- lifecycle beside it. -->
- raised
- coded
- closed
park: blocked @satelle-story-blocked-review advise blocked-triage @satelle-story-blocked-triage
cancel: cancelled @satelle-story-cancel-review

<!-- ================================================================ -->
<!-- CONTAINERS — closed by their children, not by work of their own -->
<!-- raised -> children-resolved -->
<!-- ================================================================ -->

## epic-parent
<!-- A container holds other items; nothing is performed on it, which is why it -->
<!-- has two obligations and no working lane. `children-resolved` is discharged -->
<!-- by the step in step.md declaring `provides: children-resolved`, whose gate -->
<!-- is handed a snapshot of the children to judge from. -->
<!-- -->
<!-- DECISION, not omission: no `park:`. Nothing performs in a container, so -->
<!-- there is no work to block — a stuck container is a fact about its children, -->
<!-- and each of them can park on its own. -->
- raised
- children-resolved
cancel: cancelled @satelle-story-cancel-review

## parent
<!-- The same shape one level down, and deliberately its own section rather than -->
<!-- a shared one: a section is selected by exact category name before `*` is -->
<!-- consulted, so both container kinds must name themselves. Same no-park -->
<!-- decision applies. -->
- raised
- children-resolved
cancel: cancelled @satelle-story-cancel-review

<!-- ================================================================ -->
<!-- TASK RUNS — an action, then its verification -->
<!-- raised -> run -> run-verified -->
<!-- ================================================================ -->

## execution
<!-- A run, not a piece of work: something is executed and then checked. Both -->
<!-- obligations are discharged by steps gated with the task-validate reviewers — -->
<!-- one on entry to the run, one on entry to its close. -->
<!-- -->
<!-- DECISION, not omission: no `park:`, and `cancel:` names no gate. A run that -->
<!-- should stop simply stops; there is no slice of work to preserve, and nothing -->
<!-- for a reviewer to judge on the way out. -->
- raised
- run
- run-verified
cancel: cancelled

## task
<!-- Same shape as execution, kept separate so both kinds resolve by exact name -->
<!-- rather than one of them falling through to `*` — which would put a run on -->
<!-- the working lane and gate it as though it were work. -->
- raised
- run
- run-verified
cancel: cancelled
