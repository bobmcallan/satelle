---
name: satelle-generated-readonly
scope: system
type: principle
tags: [type:principle, principles:session]
applies_to: ["*"]
description: The generated OKF reference files under .satelle/ — the stories backlog, the story-implementation-summary sub-bundle, and every reserved index.md/log.md a bundle regenerates — are READ-ONLY views of the store, not authored substrate. Never hand-edit them; they carry a generated marker, are written read-only (0o444), are overwritten on the next reindex, and nothing reads them for a decision. Mutate the underlying record via the verbs; reindex re-derives the view.
---

# Generated OKF views are read-only

Some files under `.satelle/` are **generated read-only views**, not authored
substrate: the stories backlog (`.satelle/stories/*`), the
`story-implementation-summary/` sub-bundle, and every reserved `index.md` /
`log.md` a bundle regenerates. They carry a `generated: satelle` frontmatter
marker and are written **read-only (`0o444`)**.

- **Do not hand-edit them.** The **store is the source of truth**; each file is a
  disposable view. An edit is overwritten on the next `satelle reindex`, and no
  control logic reads these files for a decision — so a stray edit changes
  nothing except wasting the turn (and the read-only mode makes the write fail).
- **Mutate the record, not the view.** Change a story with `satelle story set …`,
  not by editing `.satelle/stories/<id>.md`; `reindex` re-derives the view.
- **Authored substrate is the opposite.** `documents`, `skills`, `workflows`,
  `principles`, and `tasks` `.md` files ARE the source of truth — edit those
  freely; `reindex` indexes them.

See [[satelle-agent-goals]], [[satelle-constitution]].
