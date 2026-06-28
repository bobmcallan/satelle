---
name: satelle-rlm-standard
scope: system
kind: principle
tags: [kind:principle, principles:global]
applies_to: ["*"]
description: satelle's execution is grounded in Recursive Language Models (RLM) — an actor is a recursive LM call over a transformed context subset. A reference pointer to the RLM standard, not a restatement of the theory.
---

# Execution is RLM-grounded

satelle hosts each actor as a **recursive language-model call** over a transformed
context subset (the work item plus just the slice a step needs), aggregating the
structured return to gate status — the Recursive Language Models (RLM) pattern,
written natively in satelle (not an imported engine).

This is a **pointer**, not the theory — see the RLM writeup
(<https://alexzhang13.github.io/blog/2025/rlm/>) and the `rlm-go` reference
implementation (<https://github.com/XiaoConstantine/rlm-go>).

See [[satelle-recursive-actor-model]].
