---
name: satelle-integration-review
scope: project
kind: skill
tags: [kind:skill, type:reviewer, type:functional-check]
check: "make integration"
description: Functional-check gate on in_progress → integrated. Runs the full integration suite (make integration — the black-box CLI + headless-browser e2e); the gate accepts only if EVERY test passes, and rejects (with the failing output) if any test fails. A deterministic gate — no LLM judgment — so an item cannot advance past integration on a red suite.
---

# Integration gate (functional check)

This is a **functional-check** gate, not an LLM reviewer. The gate carries a
deterministic command in its frontmatter:

```
check: make integration
```

satelle runs that command in the repo root when an item transitions
`in_progress → integrated`. The exit code is the verdict:

- **All integration tests pass (exit 0)** → accept; the item advances to
  `integrated`.
- **Any test fails (non-zero exit)** → reject; the transition is blocked and the
  failing output tail is recorded as the reject notes.

`make integration` builds the binary once and runs the black-box suite — the CLI
end-to-end tests plus the headless-Chrome browser tests that drive the real web
UI (tab switching, inline expand, live filtering, realtime). It is local-only (it
needs a Chrome/Chromium binary); reaching `integrated` is the proof the whole
suite is green.
