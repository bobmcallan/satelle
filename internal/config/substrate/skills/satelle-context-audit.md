---
name: satelle-context-audit
scope: system
type: skill
tags: [type:skill, type:audit]
description: Diff the ACTUAL SessionStart context (satelle hook context) against the .satelle substrate for contradictions, bloat, and misplacement. Judge and report only; carries satelle principle validate as the deterministic half.
---

# Context audit — actual injection vs substrate intent

You are an **audit executor**. Capture what the agent **actually** receives at
SessionStart, compare it to the authored substrate, and write a recommendation
report. **Judge and report only** — do not edit substrate, principles, or code
unless a separate story asks for fixes.

## 1. Capture (ground truth)

Run:

```bash
satelle hook context
```

The JSON `additionalContext` (or the stderr-prefixed payload the hook emits) is
the **real injected context**: project constitution + every `principles:session`
body + the on-demand pointer. That set is ground truth for "what the agent
receives" — not what the substrate *intends*.

Also list intended residency:

```bash
# every principle carrying principles:session is system-resident
grep -l 'principles:session' .satelle/principles/*.md 2>/dev/null
satelle principle validate   # deterministic placement (embedded_sha, markers, scope, ceiling)
```

## 2. Functional check (deterministic — order:6)

Run `satelle principle validate` and fold its PASS/FAIL lines into the report
under **Deterministic findings**. That command already flags:

- embedded-owned copies missing `embedded_sha`
- illegal `principles:*` tags (only `principles:session` is legal)
- inert `scope:` on principles
- resident set over the SessionStart byte ceiling

Do **not** re-implement those checks. Surface them.

## 3. Semantic pass (LLM-only half)

Read every **resident** principle body (session-tagged and present in the
captured context). Pairwise judge:

| Class | Meaning | Flag when |
| --- | --- | --- |
| **Contradiction** | Two resident principles give conflicting instructions | e.g. one says "park when nothing engaged" while another says "engage and proceed" |
| **Bloat / redundancy** | Same guidance duplicated, or a resident body adds no per-session value | resident file should be ondemand |
| **Misplacement** | Content's *intent* does not match its marker | system-level content not session-tagged, or ondemand content forced resident |

Ground judgments in [[satelle-residency]] and [[satelle-constitution]].

### Coverage diff

- Every `.satelle/principles/*.md` with `principles:session` **must** appear in
  the captured hook context.
- Everything resident in the capture **must** exist as a substrate principle.
- Report gaps either way.

## 4. Optional lessons corpus

If a lessons corpus exists, fold it in as **extra contradiction signal**:

```bash
satelle story lessons          # cross-story typed lessons/lesson attachments
```

**Absence is not a failure** — green without lessons is valid.

## 5. Report

Write **one** report to:

```
.satelle/tasks/<running-task-id>/recommendation-report.md
```

Sections:

1. **Summary** — counts (contradictions / bloat / misplacement / deterministic fails)
2. **Deterministic findings** — `satelle principle validate` output (verbatim fold)
3. **Contradictions** — semantic pairwise flags (or "none")
4. **Bloat** — redundant / low-value residents (or "none")
5. **Misplacement** — marker vs intent (or "none")
6. **Coverage** — capture vs substrate gaps

**JUDGE AND REPORT ONLY.** No Edit/Write of product code or substrate unless
separately asked. Tools: Read, Grep, Glob, Bash for `satelle hook context` /
`satelle principle validate` / `satelle doc list`.

See [[satelle-agent-model]], [[satelle-residency]], [[satelle-constitution]].
