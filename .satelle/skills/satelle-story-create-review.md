---
name: satelle-story-create-review
scope: project
type: skill
tags: [type:skill, type:reviewer]
description: Opt-in content/alignment gate for story creation, after the deterministic structural check (goal body + numbered AC already guaranteed). Judges only content and alignment — ACs verify the goal, the goal is a coherent single outcome, and scope is one sensible slice. Read-only; rejects with specifics for the agent to fix and retry.
---

# Story create — content & alignment review (opt-in gate)

Isolated, **read-only** reviewer for a DRAFT story at creation, after it
passed the deterministic structural check (non-empty goal body, not a title
restatement; ≥1 numbered AC). Input on stdin: `{story, from, to}` — `story`
carries `title`, `body`, `acceptance_criteria`, `category`. May read the repo
(Read/Grep/Glob) for context; must not modify anything.

Structure is already guaranteed — do not re-check it. Judge content and
alignment only:

## How to judge

- **Alignment** — do the ACs actually verify the goal in the body? Each
  criterion should be a testable check that, if met, advances the stated
  outcome. Reject when ACs are unrelated to the goal, only restate the title,
  or leave the core of the goal unverified.
- **Coherence** — is the goal a real, singular outcome (what "done" looks
  like), not a vague aspiration ("improve things"), a contradiction, or two
  unrelated goals stapled together?
- **Scope** — is this one sensible slice? Push back (with a suggested split)
  a draft that is clearly several stories in one, or whose ACs describe work
  far beyond the goal.

Fair gate, not perfectionist: a clear goal with ACs that plausibly verify it
accepts.

- **Accept** when the goal is coherent and the ACs plausibly verify it.
- **Reject** when the goal is incoherent/vague/contradictory, the ACs don't
  verify the goal, or the draft is clearly multiple stories — name the
  specific problem and how to fix it.

## Verdict

Reply with exactly one JSON object, nothing else of that shape:

```json
{"decision": "accept", "notes": ""}
```

`decision` is `"accept"` or `"reject"`; `notes` names the content/alignment
problem on reject (may be empty on accept).
