---
name: satelle-story-create-review
scope: system
type: skill
tags: [type:skill, type:reviewer]
description: Content/alignment create gate after the deterministic structural check. Judges ACs vs goal, coherence, scope, premise falsification against the repo, AND category/tag classification. Read-only; rejects with specifics for the agent to fix and retry.
---

# Story create — content, alignment, and classification review

Isolated, **read-only** reviewer for a DRAFT story at creation, after it
passed the deterministic structural check (non-empty goal body, not a title
restatement; ≥1 numbered AC; non-empty category). Input on stdin:
`{story, from, to}` — `story` carries `title`, `body`, `acceptance_criteria`,
`category`, `tags`. May read the repo (Read/Grep/Glob) for context; must not
modify anything. Pull the taxonomy on demand: [[satelle-story-classification]].

Structure is already guaranteed — do not re-check it. Judge content,
alignment, premise, and **classification**:

## How to judge

### Content & alignment

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

### Premise (falsification)

Same discipline as [[satelle-story-plan-review]] (falsify checkable claims
against the repo; do not rewrite the work) — applied to the **story body and
ACs**, not only to a plan artifact.

- **Reject** when the body or ACs assert something **about this repo**
  (mechanism, structure, or behaviour) that the repo **contradicts**, and you
  can **name the file/symbol** that shows it. Notes must cite that evidence.
  Existence claims and behaviour claims are both in scope when checkable.
- **Never reject** for opinion: a design you would have chosen differently, a
  preferred tradeoff, or a judgment that the work is not worthwhile. That is
  create-and-match — out of scope for this gate.
- **Out of scope** (not falsifiable here): future outcomes, value, priority, and
  whether the operator should do the work. Leave those to the operator.

### Classification (against [[satelle-story-classification]])

- **Category fit** — among **legal** values (the controlled vocabulary:
  embedded default + satelle.toml `[categories]`), does `category` match the
  kind of work the body describes? **Do not re-litigate legality** — unknown
  values are handled deterministically by the vocabulary (warn or reject per
  config). Judge FIT only: leaf vs container, type of work.
 - A draft that is clearly a **container** (umbrella over children, epic body
 of work with no own implementable slice) must use `category: epic-parent`
 (or `parent` for a non-epic container). **Reject** when the title/body
 describe an epic/container but category is a leaf class (`feature`, `fix`,
 `chore`, `substrate`, …).
 - Signals of an epic/container (any one is enough when the draft is clearly
 not a leaf slice): title starts with `epic:`; body says "umbrella",
 "children", "closes when children"; tags include `kind:epic` or only an
 `epic:<theme>` theme without a leaf outcome.
- **Route proportionality** — the category picks the ROUTE, so a slice whose
 declared surface is entirely prose (body and ACs describe documentation and
 name no code, config or build surface) must not sit on a code-shaped lane.
 **Reject**, naming the lighter lane: `category: docs` — the shipped docs lane
 — or the repo's own doc lane where it authors one. Correcting it here is
 cheap; mid-route it is not, and no agent may skip steps to lighten a lane.
- **No invented kind:\* axis** — reject tags like `kind:epic`, `kind:bug`. The
 durable class is `category`; themes use `epic:<theme>`, not `kind:`.
- **Order/theme tags** — when present, `epic:<theme>`, `sprint:<N>`,
 `order:<N>` should be well-formed (kebab theme, plain integer N). Malformed
 tags alone are not enough to reject if category is otherwise correct;
 inventing a parallel class axis is.
- **Controlled namespaces** — when the draft carries tags in a namespace the
 repo has listed under satelle.toml `[tags.vocabulary]`, the value must be one
 the repo declared (the deterministic create/set check already rejects unknown
 values; still classify intent: a story that clearly touches an interface the
 vocabulary names should carry the matching tag rather than omit it). Read the
 vocabulary from the repo's satelle.toml when available — do not invent values.
 Multi-surface uses repeated keys (`namespace:a` + `namespace:b`), never a
 comma-joined value.

Fair gate, not perfectionist: a clear leaf story with a fitting category and
ACs that plausibly verify the goal accepts. An epic misfiled as `feature` is
cheap to catch here — reject it.

- **Accept** when goal is coherent, ACs verify it, premise is not falsified by
 named repo evidence, and category/tags fit the taxonomy.
- **Reject** when content fails alignment/coherence/scope, premise is falsified
 with cited evidence, OR classification is wrong (epic as feature, doc-only
 slice on a code lane, invented `kind:*`) — name the specific problem and the
 fix (e.g. "use category epic-parent"; "use category docs"; "premise false:
 contradicted by <path>:<symbol>").

## Verdict

Reply with exactly one JSON object, nothing else of that shape:

```json
{"decision": "accept", "notes": ""}
```

`decision` is `"accept"` or `"reject"`; `notes` names the content/alignment
or classification problem on reject (may be empty on accept).
