---
name: satelle-epic-integration-check
scope: project
type: skill
tags: [type:skill, type:reviewer, type:functional-check]
description: >-
  Functional-check gate on entry to done for satelle-epic-parallel-workflow —
  runs make integration on the merged tree. Exit 0 accepts, non-zero rejects
  with output tail as notes. Self-contained; no LLM.
---

# Epic integration check (done-entry functional gate)

**Functional-check** on the parallel epic's close. Declared as a scoped
reviewer node (`on="done"`). After epic integrate merged children and pushed,
this gate re-runs the repo integration suite so close fails closed if main is
not green.

Self-contained ```check``` (see [[satelle-reviewer-self-contained]]). Runs in
the repo root; exit 0 accepts, non-zero rejects. Mechanism, not judgment.

```check
#!/usr/bin/env bash
set -uo pipefail
make integration
```
