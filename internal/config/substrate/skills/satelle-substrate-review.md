---
name: satelle-substrate-review
scope: system
type: skill
tags: [type:skill, type:audit]
description: Judge for `satelle substrate review` — read every item in one area's manifest against its embedded default, the code and the BDD/start-small baseline, and give each a disposition (remove, reduce, keep, conflict) with its reason and evidence, plus a codebase finding per constitution rule. Judgement only; it edits nothing and raises nothing.
---

# Substrate review — read each item, cut what a careful reviewer would

The `substrate-review-manifest` document names one area (principles, skills,
workflows, agents or tasks) and carries pointers only. Each entry has `item`,
`kind`, `name`, `origin`, `seats`, `stories`, history counts, and three
absolute files you open with your read tools:

- `path` — the item's text. Read all of it; never judge an item unread.
- `default_path` (edited and seeded copies) — the current embedded default.
- `history_path` — the item's ledger history: rejection reasons, dissent
  notes, per-model figures.

The principles area also carries `rules` (name and constitution line) and
`constitution_path`. Grep the repo to check what an item claims.

## Directives

The baseline is [[satelle-story-shape]] and [[satelle-start-small]]: substrate
applies a practice, it does not describe one. For each item:

- **remove** when an embedded default covers it — name the default — or when
  nothing reads it: grep the loaders, skills, principles and workflows for its
  name, and cite the search that found no reader.
- **reduce** to the lines no default carries. Cut history narration ("we used
  to", story-id chronicles, how a rule came about) and methodology description
  (prose explaining a practice instead of applying it). Give the full text the
  item is reduced to in `replacement`; a reduce without it is not counted.
- **conflict** when the text contradicts the code or a default: cite the
  `file:line` in `evidence`.
- **keep** only when the item carries a named rule no default has: name it in
  `rule`.

For an edited copy, compare `path` with `default_path` and list each
improvement the default carries that the copy lacks in `missed_defaults`.

Read the history by model: a rule written for a weaker model that the current
seat models no longer trip is a candidate to reduce.

Every disposition needs `reason` and `evidence` (a `file:line`, a story id, or
the default's name). A disposition without them is reported as "not judged",
never as keep.

For each rule in `rules`, check the codebase: return the `file` and `line` of
the clearest contradiction, or `none: true` when you find none.

## Output

Return one verdict. `decision` is `accept` when you judged the manifest, and
`notes` is the judgement as a JSON string, one object and nothing else, in this
shape:
`{"items":[{"item":"skills/x","disposition":"reduce","reason":"…","evidence":"skills/x.md:12 narrates sty_…","replacement":"…","missed_defaults":["…"]}],"constitution_findings":[{"rule":"r","file":"internal/x.go","line":12,"note":"…"},{"rule":"s","none":true}]}`.
`decision` is `reject` only when the manifest is unusable (missing, or its
files cannot be read); `notes` then says why.

```json
{"decision":"accept","notes":"{\"items\":[…],\"constitution_findings\":[…]}"}
```

An item or rule you leave out is reported as "not judged". That is honest, and
better than a guess.
