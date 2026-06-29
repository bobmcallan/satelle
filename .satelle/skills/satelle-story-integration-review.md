---
name: satelle-story-integration-review
scope: project
type: skill
tags: [type:skill, type:reviewer, type:functional-check]
description: Functional-check gate on in_progress → integrated. Runs the FULL integration suite (the black-box CLI + headless-browser e2e) and accepts only if EVERY test passes, rejecting (with the failing output) if any test fails. A deterministic gate — no LLM judgment. Self-contained — the check is embedded below and depends on nothing outside this skill (see satelle-reviewer-self-contained).
---

# Integration gate (functional check)

This is a **functional-check** gate. The check is the embedded ```check script
below — **self-contained** (it references no external script or Makefile target).
satelle runs it in the repo root on `in_progress → integrated`; exit 0 accepts,
non-zero rejects with the failing output as notes. An item cannot advance past
integration on a red suite.

The check builds the binary and runs the `integration`-tagged suite under
`./tests` (the black-box CLI tests plus the headless-Chrome browser e2e that
drives the real web UI), passing the built binary via `SATELLE_BIN`. It needs a
Chrome/Chromium binary, so it is local-only.

```check
#!/usr/bin/env bash
set -euo pipefail
bin="$(mktemp -d)/satelle"
go build -o "${bin}" ./cmd/satelle
SATELLE_BIN="${bin}" go test -tags integration ./tests/...
```
