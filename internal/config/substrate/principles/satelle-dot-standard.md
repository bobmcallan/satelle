---
name: satelle-dot-standard
scope: system
kind: principle
tags: [kind:principle, principles:global]
applies_to: ["*"]
description: satelle workflows are authored and stored in the DOT standard (Graphviz) — node-centric steps with actors, reviewer nodes (or an edge reviewer_skill) as gates. A reference pointer to the external standard, not a restatement of it.
---

# Workflows use the DOT standard

satelle workflows are authored and stored as **Graphviz DOT** graphs — the
node-centric form of the recursive-actor model: each node is a step carrying an
`actor`, and a gate is a reviewer node (`prompt="@skill:NAME"`) or an edge
`reviewer_skill`. DOT is the canonical grammar; inline-YAML is legacy-compat input,
converted to DOT on ingest.

This is a **pointer**, not a tutorial — for the grammar itself see the Graphviz DOT
language: <https://graphviz.org/doc/info/lang.html>.

See [[satelle-recursive-actor-model]].
