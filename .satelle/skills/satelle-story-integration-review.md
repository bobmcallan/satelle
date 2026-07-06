---
name: satelle-story-integration-review
scope: project
type: skill
tags: [type:skill, type:reviewer, type:functional-check]
description: Functional-check gate on in_progress → integrated. Runs the FULL integration suite (black-box CLI + headless-browser e2e); accepts only if EVERY test passes, else rejects with the failing output. Deterministic — no LLM judgment. Self-contained (see satelle-reviewer-self-contained).
---

# Integration gate (functional check)

**Functional-check** gate. The check is the embedded ```check script below —
self-contained (references no external script or Makefile target). satelle
runs it in the repo root on `in_progress → integrated`; exit 0 accepts,
non-zero rejects with the failing output as notes. An item cannot advance
past integration on a red suite.

Builds the binary and runs the `integration`-tagged suite under `./tests`
(black-box CLI tests plus headless-Chrome browser e2e driving the real web
UI), passing the built binary via `SATELLE_BIN`. Needs a Chrome/Chromium
binary — local-only.

```check
#!/usr/bin/env bash
set -euo pipefail
bin="$(mktemp -d)/satelle"
go build -o "${bin}" ./cmd/satelle
SATELLE_BIN="${bin}" go test -tags integration ./tests/...
```
