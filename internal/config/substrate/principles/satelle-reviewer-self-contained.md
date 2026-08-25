---
name: satelle-reviewer-self-contained
type: principle
tags: [type:principle]
applies_to: ["*"]
description: When authoring or auditing a reviewer skill: everything the gate needs — rubric and functional check alike — lives ENTIRELY inside the skill artifact, never in an external script or sibling file.
---

# A reviewer is self-contained

A reviewer skill carries **everything it needs inside itself**. There are two
parts a reviewer may have, and both live in the skill:

- the **rubric** — the markdown body that rides as the LLM reviewer's prompt; and
- the **functional check** — the deterministic command, embedded as a fenced
 ```check script block in the skill body (a multi-line, self-contained script),
 or a single-line `check:` in frontmatter.

A reviewer must **never depend on an external object** — not a script under
`scripts/`, not a sibling file, not another artifact it reads at run time. A check
that shells out to `bash scripts/deploy-check.sh` is the violation: the gate's
logic has leaked out of the skill, so the gate can break when that file moves,
and the skill no longer describes what it enforces. Inline the logic instead.

## What a self-contained check MAY do

Self-contained is about the gate's *definition*, not its *reach*. The embedded
check operates on the repository under review and MAY invoke a binary **mechanism**
(`go build`, `go test`, the satelle binary's own `service install`) — running
mechanism is the binary's job (see [[satelle-constitution]]). What it must not do
is depend on a separate authored helper to hold the gate's own logic. The decision
and the steps that reach it belong in the skill.

## The test

Read the skill alone. If you can see exactly what the gate judges and how —
without opening another file — it is self-contained. If the gate's behaviour lives
in a script the skill merely names, move that script's body into the skill.

See [[satelle-constitution]], [[satelle-agent-model]].
