---
id: tsk_fa292e14
type: task
status: done
priority: medium
category: substrate
tags: context, substrate, principles, epic:session-context
created: 2026-07-01T03:14:09Z
updated: 2026-07-01T07:02:16Z
---

# Review all principles for relevance and token length; classify each session vs on-demand; trim

Depends on epic:session-context order:1 (the residency marker must exist) and is the dogfood target executed once the task machinery lands. ACTION: Audit every .satelle/principles/*.md; for each, record its token length and classify it session (injected every start; only what every session genuinely needs) or on-demand (the default; pulled when a skill or workflow references it). Retag each to the marker (principles:session for the session set; no marker for on-demand). Trim over-long principles; keep the session set minimal — ideally only the operating principle plus the project constitution. VERIFICATION: satelle validate passes for all principles; satelle hook context injects only the minimal session set and stays within alwaysContextCeiling; a per-principle table (name, tokens, tier) is recorded.

## Acceptance Criteria

1. Every principle is reviewed with its token length and a session/on-demand classification recorded (a per-principle table). 2. Principles are retagged to the residency marker; the injected session set is minimal (operating principle + project constitution only, unless a principle is justified as session). 3. Over-long principles are trimmed. 4. satelle validate passes and the live session injection stays within the context ceiling.
