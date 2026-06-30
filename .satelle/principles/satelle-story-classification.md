---
name: satelle-story-classification
scope: project
type: principle
tags: [type:principle, principles:global]
applies_to: ["*"]
description: How stories are classified and tagged — an epic carries a theme (epic:<theme>) and parents its members; a sprint carries an incremental number (sprint:<N>); and every story inside an epic or sprint carries order:<N> giving its drive sequence. The taxonomy that makes a backlog navigable, where a bare 'sprint' tag does not.
---

# Story classification — epics, sprints, and order

A story is one leaf of work, but a backlog of hundreds is only navigable when its
stories are grouped and sequenced. Classify each story along two independent axes
— a **theme** (the epic it belongs to) and a **time-box** (the sprint it ships in)
— and give it an explicit **order** within whichever grouping drives it. A tag that
only says "in a sprint" without saying which sprint, or that groups without
sequencing, leaves the backlog unsortable.

## Epics — a theme, with a parent

An **epic** is a themed body of work that spans several stories and outlives any
single sprint. Tag the epic story `epic:<theme>`, where `<theme>` is a short
kebab-case name for the theme (`epic:release-hygiene`, `epic:substrate-structure`).
Member stories join the epic through `parent` — set each child's parent to the
epic story's id. The `epic:<theme>` tag names the theme; the `parent` link is the
durable membership.

## Sprints — an incremental number

A **sprint** is a time-boxed increment of delivery. Tag every story in it
`sprint:<N>`, where `<N>` is a plain incrementing integer (`sprint:1`, `sprint:2`,
…). A bare `sprint` tag with no number is incomplete: it asserts "in a sprint" but
not which one, so consecutive increments cannot be told apart and the sprint
cannot be reviewed as a unit. Always carry the number.

## Order — the drive sequence within a grouping

Within an epic or a sprint the stories have a sequence — which is engaged first,
second, third. Tag each member `order:<N>` with a plain integer starting at 1
(`order:1`, `order:2`, …): not zero-padded, and not duplicated within the grouping.
Order encodes the operator's intended drive order; combined with engaging one story
at a time, it is how the next story to engage is chosen. A cancelled or superseded
story drops its `order` so the live sequence stays contiguous, but keeps its
`sprint:<N>` for the record.

A single story may carry all three at once: it sits under an epic
(`epic:<theme>` + `parent`), ships in a sprint (`sprint:<N>`), and holds a position
in that sprint (`order:<N>`).

See [[satelle-agile-increments]], [[satelle-done-is-last]], [[satelle-constitution]].
