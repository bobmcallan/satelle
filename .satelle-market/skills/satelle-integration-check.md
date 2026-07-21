---
name: satelle-integration-check
scope: project
type: skill
tags: [solo-dev, gate, functional-check, integration]
description: Functional-check gate that runs the integration suite (example: make integration) on entry to release. Exit 0 accepts, non-zero rejects with output tail as notes. Local-only; self-contained.
---

# Integration check (release-entry functional gate)

**Functional-check** gate on entry to `release`. Declared as a scoped reviewer
node (`on="release"`); on `in_progress → release` it runs alongside
`satelle-integration-review` (which judges the tests; this one EXECUTES them),
before the in-loop release step commits the slice.

The check is the embedded ```check script below — **self-contained**, no
external file reference (see [[satelle-reviewer-self-contained]]). Runs in the
repo root; exit 0 accepts, non-zero rejects with the output tail as notes.
Mechanism, not judgment — the deterministic gate path leaves the read-only
LLM-reviewer invariant untouched. See [[satelle-agent-model]].

The suite is the project's **local** "runs end-to-end" gate
(integration tests stay local-only in spirit), run here before the commit,
deliberately excluded from GitHub CI.

```check
#!/usr/bin/env bash
set -uo pipefail
make integration  # example: your local integration suite command
```
