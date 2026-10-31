---
name: satelle-substrate-review
scope: system
type: skill
tags: [type:skill, type:audit]
description: Judge for `satelle substrate review` — give every substrate item a disposition (remove, reduce, keep, conflict) from its own story history, and every constitution rule a codebase finding or "no finding". Judgement only; it edits nothing and raises nothing.
---

# Substrate review — judge each item by what it has earned

The `substrate-review-input` document is JSON: `items` (each with `item`,
`kind`, `origin`, `source`, `seats` and `evidence`), `rules` (each constitution
rule), `constitution_path` and `orchestrator_model`. satelle computed every
figure from this repo's ledger; do not recount them. Read an item's `source`
(or `satelle doc get <kind> <name>` for an embedded one) when its text decides
the disposition.

The yardstick is quality-first, behaviour-driven development:
[[satelle-story-shape]] (a story is a behaviour, its criteria are what meet it,
provable at their gate) and [[satelle-start-small]]. A rule earns its cost when
its history shows it catching real defects; it is friction when its rejections
are mostly amended, reversed or re-presented to accept, when it parks stories or
forces rework without a defect behind it, or when it only re-runs (flaky).
Read the history by model: a rule written for a weaker model that the current
orchestrator and seat models (`models`, `current_models`) no longer trip is a
candidate to reduce.

For each item return exactly one disposition:

- `remove` — the embedded default (or another item) already covers it; name
  which in `reason`.
- `reduce` — keep only the lines in `kept_lines`; the rest is friction.
- `keep` — it carries a quality rule the default lacks; name it in `rule`.
- `conflict` — it contradicts a constitution rule or the BDD baseline; name the
  rule in `rule`.

Every `reason` cites the evidence (counts, story ids, models) it rests on. An
item with no history is judged on its text alone, and the reason says so.

For each rule in `rules`, check the codebase: return the `file` and `line` of
the clearest contradiction, or `none: true` when you find none.

## Output

Return one verdict. `decision` is `accept` when you judged the input, and
`notes` is the judgement as a JSON string — one object, nothing else, in this
shape: `{"items":[{"item":"skills/x","disposition":"reduce","kept_lines":["…"],"reason":"…"}],"constitution_findings":[{"rule":"r","file":"internal/x.go","line":12,"note":"…"},{"rule":"s","none":true}]}`.
`decision` is `reject` only when the input is unusable (no items, or the
document is missing); `notes` then says why, and satelle reports that no
judgement came back.

```json
{"decision":"accept","notes":"{\"items\":[…],\"constitution_findings\":[…]}"}
```

An item or rule you leave out is reported as "not judged" — that is honest, and
better than a guess.
