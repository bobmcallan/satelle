---
name: satelle-dot-standard
scope: project
type: principle
tags: [type:principle]
applies_to: ["*"]
description: satelle workflows are authored and stored in the DOT standard (Graphviz) — node-centric steps with agents, gates named `prompt="@skill:NAME"` on reviewer nodes or edges. A reference pointer to the external standard, not a restatement of it.
---

# Workflows use the DOT standard

satelle workflows are authored and stored as **Graphviz DOT** graphs — the
node-centric form of the agent model: each node is a step carrying an
`agent`, and a gate is named `prompt="@skill:NAME"` on a reviewer node or an edge
(the legacy edge `reviewer_skill=` attribute still parses). DOT is the canonical
grammar; inline-YAML is legacy-compat input,
converted to DOT on ingest.

This is a **pointer**, not a tutorial — for the grammar itself see the Graphviz DOT
language: <https://graphviz.org/doc/info/lang.html>.

See [[satelle-agent-model]].
