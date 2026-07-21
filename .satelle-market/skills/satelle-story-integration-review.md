---
name: satelle-story-integration-review
scope: project
type: skill
tags: [solo-dev, reviewer, gate, integration]
description: Reviewer gate judging whether integration work adequately proves the slice before release. Isolated read-only judge.
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
