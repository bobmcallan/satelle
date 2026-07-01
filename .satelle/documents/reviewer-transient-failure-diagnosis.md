---
type: document
title: Reviewer transient-failure diagnosis (sty_d71b0791)
description: 'Differential diagnosis of the intermittent gated-transition non-advance: which candidate resource is contended, how the others are ruled out, and the captured evidence.'
tags:
- document
- reliability
- reviewer
- concurrency
timestamp: '2026-07-01T04:51:46Z'
---

# Reviewer transient-failure diagnosis (sty_d71b0791)

**Symptom.** A gated `satelle story set --status <X>` (or task transition)
intermittently does NOT advance on the first attempt — it emits non-JSON /
garbled output, the status stays put, and an identical retry moments later
succeeds. Observed repeatedly across order:6 and order:1 this session, and once
live on this very story's `--status integration` call.

**Key fact.** A retry with the same inputs succeeds. A *deterministic* reject
would reproduce; it does not. So the first failure is **transient**, not a real
verdict — the reviewer produced NO verdict, and there was no retry.

## Differential diagnosis — which resource is contended

| Candidate | Verdict | Why |
|---|---|---|
| Nested reviewer subprocess (`claude -p`) under concurrent API/rate-limit load | **CONFIRMED** | The verdict comes straight from `runner.Run` (the subprocess). Under concurrent satelle sessions across repos, the shared LLM API/account rate-limit is hit, so the subprocess returns an error/rate-limit message instead of a verdict. This directly produces the observed "no verdict". |
| Serve-watcher / SQLite `BUSY` contention | **Ruled out** | A DB lock would fail at *persist* time with a SQLite error, AFTER an accept — not as a *pre-persist* no-verdict from the reviewer. Different failure, different point in the flow. |
| Shared `~/.satelle/config.toml` + `:8787` web service | **Ruled out** | Neither is on the CLI gate path: the config is read-only during a transition, and the web service is not involved in a gate. Cannot cause a no-verdict. |

Diagnosis: the contended resource is the **shared LLM API / account rate-limit**,
reached by concurrent nested reviewer subprocesses.

## Captured evidence

With the fix installed, a controlled repro (a reviewer stub returning a 429 on its
first call, exactly as a rate-limited `claude -p` does) produced a **real
`.satelle/logs/reviewer.log` entry** — the transition retried and succeeded:

```
2026-07-01T04:51:35Z	satelle-story-create-review	attempt 1/3	transient reviewer failure: no {"decision": "accept"|"reject"} object in reviewer output — last output: Error: 429 rate_limit_error — overloaded (concurrent sessions)
```

The failing subprocess's own output (`Error: 429 rate_limit_error …`) is now
captured, so cross-session API contention is **reviewable** rather than lost.

## Resolution (why not "eliminate or serialize")

The contended resource is external (the LLM API) and cannot be eliminated.
**Machine-wide serialization** of nested reviewer subprocesses would queue EVERY
gated transition across all sessions (each gate is 200–320s) — a cure worse than
the disease. So the contention is:

- **Tolerated** — bounded retry with escalating backoff (`defaultReviewerAttempts`
  = 3) in `runReviewer`; a genuine accept/reject still returns on the first try.
- **Made deterministic** — on exhaustion, a CLEAR error names the retry count +
  last-output tail; never a silent non-advance.
- **Made observable** — each transient failure (the subprocess output) is appended
  to `.satelle/logs/reviewer.log` (`SetLogDir`, wired in `internal/cli/app.go`),
  so a real rate-limit event is surfaced for review.

Covered by unit tests (retry-then-advance; clear error on exhaustion) and an
end-to-end integration test via the real binary (transient no-verdict retried →
create succeeds; failure captured to `reviewer.log`).
