---
type: document
title: Implementation-context audit — what satelle loads per story
description: 'Audit of the context satelle provides for story/task IMPLEMENTATION work — relevance and token weight — identifying the doc-list full-body dump as the excess to trim.'
tags:
- document
- audit
- substrate
- context
timestamp: '2026-07-01T02:22:18Z'
---

# Implementation-context audit — what satelle loads per story

Audit for `sty_0751e1a3` (order:6 of epic `sty_914dd538`). Question asked: for
story/task **implementation** work, what context does satelle provide, how
relevant is it, and what does it cost in tokens? Measured against the store at
`@9f67aba` / v0.0.33 (214 stories, 14 principles, 18 skills, 98 documents).

## What is loaded, by layer

| Layer | ~Tokens | Relevance to implementation | Verdict |
|---|---|---|---|
| SessionStart always-context (`hook context`) | ~380 | High — agent-goals body + 3 refs + discovery pointer | Minimal, correct |
| **`satelle doc list` (what the pointer resolves to)** | **~82,600** | Mixed | **The excess** |
| — 95 `commit-summary-*` docs (auto-generated, one per done story) | ~44,000 | ~zero for new work | Noise |
| — 13 principles (full bodies) | ~9,000 | High | carried as full text |
| — 16 skills (full bodies) | ~10,500 | Med (reviewer gates) | carried as full text |
| — 2 workflows (full bodies) | ~2,400 | High | carried as full text |
| Story fields (body / ACs / tags) | small | High | correct |
| Per-story attached docs (backlog stories) | 0 | — | none attached |

Per-story attached docs are empty for every backlog story, so the context is
**dominated by one shared discovery surface**, not per-story variance.

## The finding

The always-resident instruction says: *"run `satelle doc list` and load only
the ones the task needs — do not preload everything."* But `doc list` emits the
**full `body` of every indexed doc** (verified: a sampled entry's body is 1340
chars against a declared size of 1346 — full, not a preview). So following the
instruction literally is an **~82.6K-token dump**, of which **~53% (~44K tokens,
95 docs) is auto-generated commit-summaries** with near-zero relevance to
implementing the next story. The discovery step blows the budget before any
useful doc is read.

The always-context body itself (~380 tokens) is well-scoped and is **not** the
problem; trimming resident principles is the wrong lever (principles are ~9K and
relevant). The lever is the discovery surface.

## The trim (deferred to task execution)

- **Make `satelle doc list` a lightweight index: emit `name`/`kind`/`headline`
  only, drop `body`.** Headline-only weight is **~2,100 tokens — a 40x
  reduction** from ~82.6K. Bodies stay reachable on demand via `satelle doc
  <name>`, which is already how a doc is meant to be read.
- **Keep commit-summaries out of the discovery path** — they are generated
  process evidence, not authored substrate an implementer discovers.

Both are one concrete change to the discovery command; neither touches the
resident set or the reviewer gates. The change itself is captured as a task
(dogfooding the task primitive) and executed via the task machinery, not here.
