---
id: tsk_b9ec013a
type: task
status: backlog
priority: medium
category: process
tags: audit
created: 2026-06-28T22:27:18Z
updated: 2026-06-28T22:27:18Z
---

# Audit code for hardcoded process; apply configuration-over-code; output an OKF document

Review the codebase for hardcoded process — logic, lifecycle steps, gate names, thresholds, paths, or policy baked into code that should instead be authored configuration — measured against the satelle-configuration-over-code principle. Produce the findings as an OKF-conformant document under .satelle/documents: each hardcoded-process instance with its location, why it violates configuration-over-code, and the recommended config-driven alternative.

## Acceptance Criteria

1. The codebase is reviewed for hardcoded process against the satelle-configuration-over-code principle, covering at least the workflow/gate/lifecycle and document/skill resolution paths.
2. Each finding records file:line, the hardcoded element, the principle it violates, and a recommended configuration-driven fix.
3. An output document is created in .satelle/documents capturing the findings, OKF-conformant (non-empty type e.g. 'audit', title, description, ISO-8601 timestamp).
4. The document validates under OKF conformance.
