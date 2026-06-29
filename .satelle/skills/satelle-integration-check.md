---
name: satelle-integration-check
scope: project
type: skill
tags: [type:skill, type:reviewer, type:functional-check, reviewer:always]
on: [commit_push]
description: Functional-check gate that runs the integration suite (make integration) before a slice is committed. An always-on system reviewer scoped to the commit_push edge (on: [commit_push]), so it runs on the integration → commit_push transition alongside satelle-integration-review (which judges the tests) — exit 0 accepts, non-zero rejects with the output tail as notes. Local-only (the suite is the project's local gate, never GitHub CI). Self-contained, per satelle-reviewer-self-contained.
---

# Integration check (pre-commit functional gate)

This is a **functional-check** gate on the path into `commit_push`. It is an
always-on system reviewer (`reviewer:always`) scoped with `on: [commit_push]`, so
on the `integration → commit_push` transition it runs alongside the edge's
`satelle-integration-review` reviewer (which judges the tests, while this one
EXECUTES them) and **before** the slice is committed.

The check is the embedded ```check script below — **self-contained**, referencing
no external file (see [[satelle-reviewer-self-contained]]). satelle runs it in the
repo root; exit 0 accepts, a non-zero exit rejects with the output tail as the
notes the executor fixes. It is **mechanism, not judgment** — the deterministic
gate path, like the commit-push CI check — so the read-only LLM-reviewer invariant
is untouched. See [[satelle-actor-model]].

The integration suite is the project's **local** gate for "it runs end-to-end"
([[integration-tests-local-only]] in spirit): it is run here, before the commit,
and is deliberately not part of GitHub CI.

```check
#!/usr/bin/env bash
set -uo pipefail
make integration
```
