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

## *
- raised
- coded
- closed
park: blocked @satelle-story-blocked-review advise blocked-triage @satelle-story-blocked-triage
cancel: cancelled @satelle-story-cancel-review

## epic-parent
- raised
- children-resolved
cancel: cancelled @satelle-story-cancel-review

## parent
- raised
- children-resolved
cancel: cancelled @satelle-story-cancel-review

## execution
- raised
- run
- run-verified
cancel: cancelled

## task
- raised
- run
- run-verified
cancel: cancelled
