---
id: tsk_12c5ecb4
type: task
status: backlog
priority: high
category: substrate
tags: context, substrate, doc-list, cli, epic:session-context, workflow:satelle-project-workflow
created: 2026-07-01T02:22:53Z
updated: 2026-07-01T03:14:38Z
---

# Make 'satelle doc list' a lightweight headline-only index (drop bodies; exclude commit-summaries)

From the implementation-context audit (sty_0751e1a3 / .satelle/documents/implementation-context-audit.md): the always-context pointer sends implementers to 'satelle doc list', which emits the FULL body of every indexed doc (~82.6K tokens), 53% of it (~44K) auto-generated commit-summaries. Make discovery a lightweight index so the pointer resolves to ~2.1K tokens (40x smaller), keeping full bodies reachable on demand via 'satelle doc <name>'.

## Acceptance Criteria

1. 'satelle doc list' emits name/kind/headline only by default (no full body); bodies remain reachable via 'satelle doc <name>'. 2. Commit-summaries do not appear in the default discovery listing. 3. A test asserts the default listing carries no doc body and its size is a small fraction of the prior full-body output. 4. Hook/reviewer callers that relied on doc-list bodies are updated or confirmed unaffected.
