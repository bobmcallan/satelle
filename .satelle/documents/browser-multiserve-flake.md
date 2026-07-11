---
type: document
title: Browser and multi-serve integration flake notes
description: Expectation when browser/multi-serve integration tests flake under load — re-run or isolate; not a product defect by default.
tags: [document, testing, integration, epic:workflow-review-followups]
timestamp: '2026-07-12T00:00:00Z'
---

# Browser / multi-serve integration flakiness

## What flakes

Integration tests under `tests/` that start `satelle serve` (browser e2e via
chromedp, multi-project serve, settings/login web) can fail health checks or port
binds when **many `satelle serve` processes** already share the machine — another
session's dogfood service, a leftover child from a killed suite, or parallel
packages racing the same high ports.

This is an environment interaction, not usually a product regression in the slice
under test.

## Expectation

1. **Re-run** the failed package once on a quieter machine (`go test -count=1 -tags=integration ./tests/ -run TestBrowser…` or the specific multi-serve test).
2. **Isolate** if it persists: stop leftover serves (`pkill -f 'satelle serve'` only when you own them), free the test ports, or run the suite with no concurrent dogfood service on the same ports.
3. Browser tests already isolate the machine-wide workspace registry via
   `SATELLE_HOME` tempdirs in helpers — prefer that pattern for new multi-serve
   tests; do not assume a clean global registry.
4. Agents.toml and other scaffold mutations in tests must **force-write** the file
   (`os.WriteFile` / test helper write), never fragile substring replace on
   scaffold text (already the tree convention).

Do not "fix" a green product by weakening assertions to silence a crowded-host
flake; re-run or isolate first.
