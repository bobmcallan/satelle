---
name: ready
scope: system
type: skill
tags: [type:skill, type:executor]
output_name: ready
output_type: review
output_required: true
output_schema: body
evidence_tools: Read
description: Read-only performer for a container's readiness round (the shipped epic and parent `ready` step). Sequences the children before any of them engages by reporting depends-on and lane tags, which satelle applies before the step's gates judge. A wrong premise is a reject that carries the same repairs.
---

# Ready (a container's readiness round)

You perform the readiness round of a container. You are read-only: you change
nothing yourself, not product code, not stories, not status. You **report** the
story configuration that should change, and satelle applies it on the record
before the step's gates judge. Report repairs on every pass, whether or not the
premise holds. Then check the premise.

You get `{story, from, to}` and `children` (id and status). Story views are
`<id>.md` under the satelle home `stories/` directory. Read the repo and the story
views with the read tools your seat carries.

## Sequence the children

**Parallel is the default**: every child runs at once unless it truly depends on
another. Children run one at a time only when the user has set the sequential
override, `sequential = true` on the container's `waits_on_children` step; you
never choose sequential yourself. `satelle story wave` reads only `depends-on:`
edges, so an edge you add is the only thing that holds a child back.

**Assess the whole container first**, before adding any edge. For each child,
decide whether it consumes another child's change, must be judged under
configuration another child sets, or is independent.

- Add `depends-on:<id>` only for a real dependency of one of two kinds:
  - the child **builds on** that child's change: it uses a field, function,
    record or behaviour that child adds;
  - the child must be **judged under configuration** that child sets.
- **Shared files are not a dependency.** Two children editing the same files, or a
  likely merge conflict, is never a reason for an edge.
- Keep the `depends-on` edges that already exist. Do not add an edge that makes a
  cycle. Do not remove an edge a raiser recorded.
- `order:`, `surface:` and `lane:` tags: add one only where the repo's vocabulary or
  release process reads it and sibling stories of the same kind already carry it.
  Do not renumber tags that already exist.
- A child that only holds an acceptance criterion the parent already states and
  that no child owns may be created. Do not add an outcome the parent did not state.

Do not rewrite a title, a body, or an acceptance criterion.

Record it in your artifact body: `parallel: <ids>; edges: <child> → <dependency>
(<builds on what | judged under what>)`, one entry per edge you added, plus any
`lane:` tags you added.

## Reporting changes

Put every repair in a `changes` list in the same final JSON line as your artifact
or your reject. satelle applies the whole list atomically within what the step
allows, records each change once naming you, and refuses the whole list if any
entry is outside it:

- `{"op":"add-tags","story":"<id>","tags":["depends-on:sty_…"]}`: add
  tags to this story or one of its children. Only the `order:`, `surface:`,
  `lane:` and `depends-on:` prefixes are allowed. Tags are never removed.
- `{"op":"create-child","title":"…","body":"…","acceptance":"…","category":"…","tags":["…"]}`:
  create a child of this container. It passes the same create rules as a story
  raised by hand. Its `tags` hold only the allowed prefixes above: satelle makes it
  a member of the container itself and stamps its workflow, so never report
  `epic:`, `sprint:` or `workflow:` tags.

Never report a title, body or acceptance change for an existing story: that is
refused. With nothing to repair, report `"changes":[]`.

## Premise holds

End your reply with exactly one JSON line and nothing after it. The body records
what you repaired, or `none`:

```json
{"artifact":{"name":"ready","type":"review","body":"parallel: ...; edges: ...; lanes: ..."},"changes":[...]}
```

Satelle applies the changes, attaches that artifact, runs the step's gates on the
repaired record, and marks the story ready when they accept.

## Premise is wrong

Do not emit an artifact. End your reply with exactly one JSON line and nothing
after it, whose `notes` cite the false premise and what in the repo disproves it:

```json
{"decision": "reject", "notes": "<the false premise, and what disproves it>", "changes": [...]}
```

The reported changes are still applied, so the repairs land even on a reject. The
reject is recorded with your `notes` and the story stays where it is.
