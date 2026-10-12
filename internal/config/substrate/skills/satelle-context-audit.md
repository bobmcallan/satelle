---
name: satelle-context-audit
scope: system
type: skill
tags: [type:skill, type:audit]
description: Diff the ACTUAL SessionStart context (satelle hook context) against the .satelle substrate for contradictions, bloat, and misplacement; also the rubric of `satelle doctor --context-review`, which judges one assembled context for paraphrased restatements. Judge and report only; carries satelle principle validate as the deterministic half.
---

# Context audit — actual injection vs substrate intent

You are an **audit executor**. Capture what the agent **actually** receives at
SessionStart, compare it to the authored substrate, and write a recommendation
report. **Judge and report only** — do not edit substrate, principles, or code
unless a separate story asks for fixes.

## Two ways this rubric is invoked

- **By the task** (`tsk_context-audit`): follow §1–§5 and write the report.
- **By `satelle doctor --context-review`**: the stdin payload is JSON with a
  `context` and a `corpus`. Follow **§6 only**, run no command, write no file,
  and reply with the one JSON object §6 defines.

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
satelle principle validate # deterministic placement (embedded_sha, markers, scope, ceiling)
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
satelle story lessons # cross-story typed lessons/lesson attachments
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

## 6. Doctor payload — paraphrased restatements (`doctor --context-review`)

The deterministic half of doctor already reports guidance copied **word for
word**. Your job is the half it cannot see: guidance restated in **different
words**.

The payload is:

```json
{
  "context": {"label": "session[…] | seat[…]", "parts": [{"source", "name", "kind", "residency", "where", "body"}]},
  "corpus":  [{"source", "name", "kind", "residency", "where", "body"}]
}
```

`context.parts` is what one agent actually receives. `corpus` is the index of
**every** principle and skill — authored and embedded, resident and on-demand,
linked or never received — plus the constitution. For each part, judge whether
any passage of it says again what **another corpus document** already says,
instead of pointing at that document with `[[name]]`.

Count it when:

- the same instruction, rule or definition appears in both, in different words;
- the other document is on-demand or never reaches this context — a restatement
  of a document the agent does not receive is still a restatement.

Do **not** count it when:

- the part refers to the other document by `[[name]]` or by its backticked name;
- the overlap is a one-line orienting summary that links its source;
- it is the part's own content, or the two parts are the same document;
- the words are copied verbatim — the deterministic check owns that.

Reply with **exactly one JSON object**, no prose and no code fence:

```json
{"findings": [
  {"part": "<source of the part that restates>",
   "restates": "<source of the corpus document it restates>",
   "kind": "paraphrase",
   "quote": "<the restating passage, one or two sentences, quoted>"}
]}
```

`part` and `restates` are the payload's own `source` strings (`kind/name`).
Report each pair once. Reply `{"findings": []}` when there is nothing to report.

See [[satelle-agent-model]], [[satelle-residency]], [[satelle-constitution]].
