---
name: satelle-agent-telemetry
type: principle
tags: [type:principle, principles:session]
applies_to: ["*"]
description: The PROMPTED telemetry channel — how an agent self-reports work quality. An agent holding the satelle CLI grant records what only it can judge (a try/fail/timeout of its OWN work, and per-step quality signals) as a typed event via `satelle story log <id> --kind <kind> --data k=v`. Complements the binary's CODED channel (which already captures dispatch-level agent-retry/failure/timeout), so log the subjective/decision signal — never a secret, never a duplicate of what the binary already sees.
---

# Agent telemetry — self-report what only you can judge

satelle measures agent quality on **two channels**. The **coded** channel is the
binary's own capture: a dispatched sub-process that is killed, times out, or
returns no verdict is already recorded as a structured `telemetry_event`
(`agent-retry` / `agent-failure` / `agent-timeout`) — you never log those. This
principle is the **prompted** channel: the quality only YOU, the agent, can judge.

If you hold the satelle CLI grant (the in-loop session, or a dispatched executor
with `Bash(satelle:*)`), record it as a typed event on the story:

```bash
satelle story log <sty_id> --kind <kind> --data key=value --data key=value
```

**What to record**
- A **try / fail / timeout of your OWN work** the binary can't see — a step you
  retried by hand, an approach you abandoned, a tool or permission wall you hit,
  why a gate rejected you.
- **Per-step quality** — whether a step went smoothly or fought you, a plan that
  was wrong, a rubric or context gap you had to work around.

**How to log well**
- Pick a clear `kind` (e.g. `step-quality`, `retry`, `blocked`, `plan-gap`) and
  put the specifics in `--data`; numbers stay numbers (`--data attempts=3`), keep
  values short.
- **Never** log a secret, token, or env value — the verb refuses the obvious
  cases, but the judgment is yours.
- **Don't duplicate the coded channel** (dispatch kills/timeouts already land) —
  log only the signal the binary cannot observe.

Read-only reviewer and summariser agents have **no CLI grant** and cannot log;
they surface per-step quality in their recap instead (see the
`satelle-step-summary` skill).

See [[satelle-constitution]], [[satelle-agent-goals]], [[satelle-agent-model]].
