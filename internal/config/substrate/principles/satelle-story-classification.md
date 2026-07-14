---
name: satelle-story-classification
type: principle
tags: [type:principle]
applies_to: ["*"]
description: How stories are classified — category (including epic-parent/parent containers), theme tags (epic:<theme>), sprints (sprint:<N>), and order:<N>; multi-value tags use repeated keys. Category selects the governing workflow; invented kind:* tags are not a taxonomy axis.
---

# Story classification — category, epics, sprints, and order

A story is one leaf of work, but a backlog of hundreds is only navigable when its
stories are grouped and sequenced. Classify each story along **category** (what
kind of work item it is — selects the workflow), a **theme** (the epic it belongs
to), and a **time-box** (the sprint it ships in), and give it an explicit **order**
within whichever grouping drives it.

## Category — selects the workflow

`category` is a first-class field on the story (not a free-form tag). It decides
which workflow governs the item:

| Category | Meaning | Typical workflow |
| --- | --- | --- |
| `feature` / `fix` / `chore` / … | Leaf work with a slice to build | project (or baseline) workflow |
| `substrate` | Markdown-only substrate change (no binary) | substrate workflow when authored |
| `parent` | Container whose work IS its children | parent workflow |
| `epic-parent` | Epic container — themed umbrella over children | parent workflow |

**File an epic as `category: epic-parent`** (or `parent` for a non-epic
container). Do **not** invent a `kind:epic` / `kind:bug` tag axis — those tags
are not taxonomy; the durable class is `category`. The parent workflow's
`applies_to: ["epic-parent", "parent"]` is how a category-specific workflow beats
the wildcard project workflow.

## Epics — a theme, with a parent

An **epic** is a themed body of work that spans several stories and outlives any
single sprint. Tag the epic story `epic:<theme>`, where `<theme>` is a short
kebab-case name for the theme (`epic:release-hygiene`, `epic:substrate-structure`).
Member stories join the epic through `parent` — set each child's parent to the
epic story's id. The `epic:<theme>` tag names the theme; the `parent` link is the
durable membership. The epic *item itself* carries `category: epic-parent`.

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

## Tags — multi-value namespaces (repeated keys)

Tags are a set of strings, often `namespace:value`. **Multiple values in one
namespace use repeated keys** — separate entries, not a comma-joined value:

- Canonical: `epic:this`, `epic:that` (two tags).
- Not canonical: `epic:this,that` (one tag) — it fights CLI `StringSlice` parsing
  and loses round-trip fidelity through create/set/get.

This matches the store (`[]string`), additive mutation (`--add-tags` /
`--remove-tags`, including group remove like `sprint:*`), and display. Filter
with `satelle story list --tag <tag>` (and the same flag on `task list`): an item
matches when it **holds that exact tag** among its set (ANY-match — a story with
both `epic:this` and `epic:that` matches `--tag epic:this`). The tag filter
composes with `--status` and `--parent`. Prefer plain integer sprints
(`sprint:1`, not date-shaped values) so consecutive increments stay enumerable.

See [[satelle-done-is-last]], [[satelle-constitution]].
