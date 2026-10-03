---
name: satelle-story-classification
type: principle
tags: [type:principle]
applies_to: ["*"]
description: How stories are classified — category (selects the workflow; TYPE, never a surface), epic membership by the epic:<theme> tag, sprints (sprint:<index>), order:<N>, and multi-value tags by repeated keys. The category vocabulary is substrate/config/categories.toml.
---

# Story classification — category, epics, sprints, and order

A backlog is navigable only when its stories are grouped and sequenced. Classify
each story by **category** (selects the workflow), a **theme** (its epic), a
**time-box** (its sprint) and an **order** within that sprint.

## Category — selects the workflow

`category` is a first-class field, not a free-form tag, and it decides which
workflow governs the item. It is a controlled **TYPE** vocabulary: the embedded
default is `substrate/config/categories.toml` (its header carries the synonym
collapses, e.g. `bug` → `fix`), and a repo extends or replaces it with
`[categories] extra` / `vocabulary` in satelle.toml — never a Go literal.
`[categories] enforce` is `off`, `warn` (default: advise, still create) or
`reject` (an opt-in hard fail). Existing stories are never rewritten when a
vocabulary is introduced.

- **Category is TYPE, not a surface.** It is single-valued. A story touching two
  interfaces (`surface:ui` + `surface:cli`) still has exactly one category;
  surface is a `surface:` tag, and names like `frontend` / `web` / `ui` / `cli`
  are not categories. A category per interface would fork the lifecycle.
- **Containers.** File an epic as `category: epic-parent` (or `parent` for a
  non-epic container whose members are the stories with that `parent_id`). Do not
  invent a `kind:epic` / `kind:bug` tag axis; the durable class is `category`.
  The parent workflow's `applies_to: ["epic-parent", "parent"]` is how a
  category-specific workflow beats the wildcard one.

## Epics — a theme, with a parent

An **epic** is a themed body of work that spans several stories and outlives any
single sprint. Its membership is ONE rule:

- **The set is every story carrying `epic:<theme>`**, where `<theme>` is a short
  kebab-case name (`epic:release-hygiene`, `epic:substrate-structure`).
- **The parent is the single story in that set whose category is `epic-parent`**,
  and it carries the same tag. Every other story in the set is a **child** and
  keeps its own work category (`fix`, `substrate`, `docs`, …) — there is no
  `epic-child` category.
- **`parent_id` is not membership.** A story with the tag and no `parent_id` is a
  child; a story with a `parent_id` and no `epic:` tag is in no epic's set.

The epic closes only when Go re-reads that set from the store at the commit and
every child is `done` or `cancelled`; the `children` list handed to the close
gate's reviewer is a snapshot taken when it was prepared — a hint, not the check.
If the set cannot be determined — the `epic-parent` has no `epic:<theme>` tag, or
more than one `epic-parent` carries the tag — the container does not close, and
the refusal says why. A proposal filed with an `epic:<theme>` tag whose container
is already `done` or `cancelled` is filed without that tag, with a body note
naming the container. Scheduling the children is `satelle help epic-wave`.

## Sprints and order

- **Sprint.** Tag every story in a time-box `sprint:<index>`. The index form is a
  repo choice (integer, date, month-plus-name), kept consistent within the repo
  and fixed in the repo's own substrate, not here. A bare `sprint` tag with no
  index is incomplete.
- **Order.** Within a sprint, tag each member `order:<N>`: a plain integer from 1,
  not zero-padded, not duplicated. It is position in the sprint, not priority;
  dependency is `depends-on:`, never inferred from order. A cancelled or
  superseded story drops its `order` but keeps its `sprint:`.
- **The sprint owns `order:`.** It means nothing without `sprint:`, and unlike
  `epic:` and `sprint:` it is **not durable** — renumbered freely as the sprint
  is re-planned.
- **A fixed-order epic** keeps its members consecutive in the sprint sequence and
  states the hard dependency in the story body. One story carries one `order:`;
  the `epic-parent` carries none, since it is driven by its children.

## Tags — multi-value namespaces use repeated keys

Tags are a set of strings, often `namespace:value`. Multiple values in one
namespace are **separate entries**, not a comma-joined value: `epic:this` +
`epic:that`, never `epic:this,that` (one tag; it fights CLI `StringSlice` parsing
and loses round-trip fidelity). This matches the store (`[]string`), additive
mutation (`--add-tags` / `--remove-tags`, including group remove like `sprint:*`)
and display. `--tag <tag>` on `story list` / `task list` matches an item that
holds that exact tag among its set (ANY-match) and composes with `--status` and
`--parent`.

## Controlled tag namespaces — `tags.vocabulary`

A repo MAY declare, in satelle.toml, that a namespace accepts only a fixed set of
values — e.g. `[tags.vocabulary]` with `surface = ["ui", "cli"]`. It is **repo
config, not compiled in** (another repo declares its own namespaces).

- A tag in a listed namespace must use a declared value; an unknown value is
  rejected at create and set with an error naming the allowed set. Matching is
  case-insensitive and the stored form uses the declared casing.
- Namespaces absent from the table stay free-form (`epic:`, `sprint:`, `order:`).
  A story with no controlled-namespace tag is always valid.

See [[satelle-done-is-last]], [[satelle-constitution]].
