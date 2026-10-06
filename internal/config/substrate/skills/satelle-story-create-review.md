---
name: satelle-story-create-review
scope: system
type: skill
tags: [type:skill, type:reviewer]
description: Content/alignment create gate after the deterministic structural check. Judges ACs vs goal, coherence, scope, solution prescription, a DEAD premise only, AND category/tag classification. Read-only; rejects with specifics for the agent to fix and retry.
---

# Story create — content, alignment, and classification review

Isolated reviewer for a DRAFT story at creation. Input on stdin:
`{story, from, to}` — `story` carries `title`, `body`, `acceptance_criteria`,
`category`, `tags`. May read the repo (Read/Grep/Glob) for context; does not
edit. Pull the taxonomy on demand: [[satelle-story-classification]].

The deterministic structural check has already passed (title, goal body, and
category — acceptance criteria are not part of that check), so structure is
guaranteed — do not re-check it. Judge content, alignment, solution
prescription, premise, and **classification**:

## How to judge

### Content & alignment

Empty or unnumbered acceptance criteria are not a reject. When
`acceptance_criteria` is empty or has no numbered item, judge coherence of the
body, solution prescription in the body and in any acceptance-criteria text,
the dead-premise check, and classification. Alignment and scope
apply once numbered criteria are present.

- **Coherence** — is the goal a real, singular outcome (what "done" looks
 like), not a vague aspiration ("improve things"), a contradiction, or two
 unrelated goals stapled together?

When numbered criteria are present, the alignment and scope rules are unchanged,
and those ACs must verify the goal:

- **Alignment** — do the ACs actually verify the goal in the body? Each
 criterion should be a testable check that, if met, advances the stated
 outcome. Reject when ACs are unrelated to the goal, only restate the title,
 or leave the core of the goal unverified.
- **Scope** — is this one sensible slice? Push back (with a suggested split)
 a draft that is clearly several stories in one, or whose ACs describe work
 far beyond the goal.

### Solution prescription

- **Reject** when the body or the acceptance criteria require a particular
  implementation, including when acceptance criteria are empty or unnumbered
  and the body is what prescribes it: a function, type, or file to write, an
  exact code change, or a mechanism the draft says must be built. Notes name each prescriptive part and say to state what should be true and the evidence for it.
- **Not a prescription:** a draft that states what should be true, and does not require a particular implementation, passes this check.
  Naming a file, symbol, or mechanism only to show what exists today, or what
  is missing, is evidence, not a prescription.

### Premise (dead, not unverified)

One narrow check: does the story ask for something that **does not exist, or
has been removed**? That is worth catching here — it would waste a whole
story — and it is decidable from the draft plus one file read.

- **Reject** only when the body or ACs name a mechanism, artifact or capability
  this repo does not have, or had and removed, and you can **name the
  file/symbol** that shows it. Notes must cite that evidence.
- **Never reject** for opinion: a design you would have chosen differently, a
  preferred tradeoff, or a judgment that the work is not worthwhile. That is
  create-and-match — out of scope for this gate.
- **Out of scope** (not this gate's business): future outcomes, value, priority,
  whether the operator should do the work, and whether the work is worthwhile.

**Not grounds at create, each of these.** They are plan-time questions and
`satelle-story-plan-review` owns them, with the plan in hand:

- a count, tally or total asserted about this repo
- whether a sibling, dependency or referenced story exists
- whether referenced work has already landed
- dependency ordering — that this story must follow or precede another
- anything requiring more than the draft and one file read

A story whose premise depends on a *sibling story's state* is not miswritten; it
is a sequencing matter, and the driver sequences it. Rejecting it here costs a
round to learn something a later gate states better. If a premise looks shaky
but is not dead, accept and let the plan review falsify it.

### Classification (against [[satelle-story-classification]])

Legality is already deterministic — the vocabulary decides it. **Judge FIT
only**, and never reject a value for being unknown.

- **Category fit** — does `category` match the kind of work the body describes?
 A draft that is clearly a **container** (umbrella over children, no
 implementable slice of its own) must use `category: epic-parent`, or `parent`
 for a non-epic container. **Reject** a container filed under a leaf class.
 Strongest signals, any one enough: title starts with `epic:`; body says
 "umbrella", "children", "closes when children"; a theme tag with no leaf
 outcome of its own.
- **Route proportionality** — the category picks the ROUTE, so a slice whose
 declared surface is entirely prose (body and ACs describe documentation and
 name no code, config or build surface) must not sit on a code-shaped lane.
 **Reject**, naming the lighter lane: `category: docs` — the shipped docs lane
 — or the repo's own doc lane where it authors one. Correcting it here is
 cheap; mid-route it is not, and no agent may skip steps to lighten a lane.
- **No invented kind:\* axis** — reject tags like `kind:epic`, `kind:bug`. A
 malformed `epic:` / `sprint:` / `order:` tag is NOT reject grounds on its own
 (the linked principle carries their form); inventing a parallel class axis is.
- **Controlled namespaces** — a story that clearly touches an interface the
 repo's satelle.toml `[tags.vocabulary]` names should carry the matching tag
 rather than omit it. Read the values from that config; never invent them.

Fair gate, not perfectionist: a clear leaf story with a fitting category
accepts. When numbered criteria are present, they must plausibly verify the goal.

- **Accept** when the goal is coherent, the premise is not falsified by
 named repo evidence, the draft does not prescribe the implementation, and
 category/tags fit the taxonomy. When numbered criteria are present, also
 require that those ACs verify the goal.
- **Reject** when numbered criteria are present and fail alignment or scope,
 when coherence fails, when the body or the acceptance criteria prescribe the
 implementation whether or not those criteria are numbered (name each prescriptive part), when the premise is falsified with cited evidence, OR
 classification is wrong (epic as feature, doc-only slice on a code lane,
 invented `kind:*`) — name the specific problem and the fix (e.g. "use category
 epic-parent"; "use category docs"; "premise false: contradicted by
 <path>:<symbol>").


## Re-presented edge

Every judging verdict sets `reviewed` to the exact words it rests on. The first verdict on the edge has no prior row, so it judges and sets `reviewed`; a later citation cannot start until that quotation is stored. On a re-presented edge the quotation comparison governs. Open `prior_verdicts`. Take this skill's latest row. If that row has no `reviewed` string, or `reviewed_truncated` is set, re-judge the words this verdict would rest on and set `reviewed` to those exact words. Do not cite an older row that still has a complete `reviewed` string. If that latest row has a complete `reviewed` string, compare those exact words with the words this verdict would rest on. If they are the same words, re-issue that earlier verdict: the same decision, the same `reviewed` string, and notes that cite its `attempt` and skill. An unchanged accept stays accept. An unchanged reject stays reject. A citation does not clear a standing rejection. If the words differ, judge the difference, name what changed in notes, and set `reviewed` to the exact words this verdict rests on. Cite the passages the verdict depends on, not the whole input. Definition-edit fields, edit timestamps, and a selection of story sections are not how unchanged words are established.

## Verdict

Reply with exactly one JSON object, nothing else of that shape:

```json
{"decision": "accept", "notes": "", "reviewed": "<exact words this verdict rests on>"}
```

`decision` is `"accept"` or `"reject"`; `notes` names the alignment, scope,
prescription, dead-premise, or classification problem on reject (may be empty
on accept).

`reviewed` is required on every judgment, including the first verdict on this edge. It is the exact words this verdict rests on. Replace the slot; do not omit the field and do not copy the angle-bracket text. A citation of the same words repeats that same `reviewed` string.
