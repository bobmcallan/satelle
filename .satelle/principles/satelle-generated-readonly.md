---
name: satelle-generated-readonly
type: principle
tags: [type:principle]
applies_to: ["*"]
description: Generated OKF reference files (reserved index.md/log.md a bundle regenerates, and any remaining backlog views) are READ-ONLY views of the store, not authored substrate. Story attachments live on the home-keyed runtime plane (~/.satelle/<repo-key>/stories/), not under in-repo .satelle/stories/. Never hand-edit generated views; they carry a generated marker, are written read-only (0o444), are overwritten on the next reindex. Mutate the underlying record via the verbs; reindex re-derives the view.
---

# Generated OKF views are read-only

> **Repo-local:** governs developing satelle / this dogfood repo's discipline — not product-canon for other repos. Pull on demand; not an embedded default.

Some files satelle materialises are **generated read-only views**, not authored
substrate: reserved `index.md` / `log.md` a bundle regenerates, and any backlog
OKF views. Story **attachments** (plan, step summaries, release summary) live on
the **home-keyed runtime plane** (`~/.satelle/<repo-key>/stories/<id>/` —
`satelle runtime path` names the dir), not under in-repo `.satelle/stories/`
(post-relocation, sty_4660bbe1 / sty_58fa970e). Generated views carry a
`generated: satelle` frontmatter marker and are written **read-only (`0o444`)**.

- **Do not hand-edit them.** The **store is the source of truth**; each file is a
  disposable view. An edit is overwritten on the next `satelle reindex`, and no
  control logic reads these files for a decision — so a stray edit changes
  nothing except wasting the turn (and the read-only mode makes the write fail).
- **Mutate the record, not the view.** Change a story with `satelle story set …`
  or attach with `satelle story attach`; never recreate or edit
  `.satelle/stories/<id>/…` by hand.
- **Authored substrate is the opposite.** `documents`, `skills`, `workflows`,
  `principles`, and `tasks` `.md` files ARE the source of truth — edit those
  freely; `reindex` indexes them.

See [[satelle-agent-goals]], [[satelle-constitution]].
