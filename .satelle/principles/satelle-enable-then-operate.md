---
name: satelle-enable-then-operate
scope: system
type: principle
tags: [type:principle]
applies_to: ["*"]
description: satelle has two phases — ENABLE (install + `satelle init` scaffolds one `.satelle/` root into a repo, idempotently) and OPERATE (every command thereafter resolves that root and reads/writes only there). `.satelle/` is the single home for config, authored substrate, the database, the work items, and the evidence ledger; the binary is the load-and-run layer that operates ON that root, never a parallel store. Enabled is a precondition operating commands assume, never a side effect they cause.
---

# Enable, then operate from .satelle

satelle works in two phases, and the boundary between them is the whole structure:
a repo is **enabled** once, and every command **operates** from the single root
that enabling laid down.

## Enable — init scaffolds .satelle/

A repo is made ready for satelle by installing the binary and running
`satelle init`. Init scaffolds the one `.satelle/` root: the config
(`satelle.toml`, fully defaulted so the repo runs zero-config), the
authored-substrate dirs the indexer watches (`documents/ workflows/ principles/
skills/`), the per-repo database (`.satelle/satelle.db`), and a managed
`.gitignore` block that keeps the database out of git while committing the config
and substrate. Init is **idempotent**: re-running preserves what exists and
reports only what it added. Enabling is install + init — it is the one operation
that creates state.

## Operate — every command reads and writes .satelle/

After enable, **`.satelle/` is the single source and sink for all state.** Config,
the indexed substrate, the work items (stories and tasks), the database, and the
evidence ledger / op-log all live under that one root. An operating command
resolves the repo by walking up to the `.satelle/` it operates from, then reads
and writes only there. There is no second home — no global mutable store the repo
silently depends on, no out-of-tree scratch the operating model relies on.

## The structure this enforces

- **One root.** If it is satelle state, it is under `.satelle/`. A feature that
  must persist something persists it there, beside the rest — not in a parallel
  location the rest of the system has to know about.
- **The binary operates ON .satelle/; it is not a parallel store.** The binary is
  the load-and-run layer — it indexes the substrate, dispatches verbs, runs
  reviewers, and persists evidence. The durable substance is the files under
  `.satelle/`. Mechanism in the binary, state in `.satelle/`.
- **Enabled is a precondition, not a side effect.** An operating command assumes
  an enabled repo and surfaces a missing one; only `init` creates the root, so a
  command never silently scaffolds or writes state outside it.

See [[satelle-repo-agnostic]], [[satelle-configuration-over-code]],
[[satelle-constitution]].
