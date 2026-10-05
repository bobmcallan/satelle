---
name: satelle-workflow-change-review
scope: system
type: skill
tags: [type:skill, type:reviewer]
description: Implementation-exit gate judging route edits — where a gate is bound (a step table's reviewers vs an always-on gate entry), over-fire, skill and obligation resolution, park/cancel/recover preserved. Fast-accepts workflow:n/a when the slice touches no workflow file. Review-only.
---

# Workflow-change review

You are an isolated reviewer judging whether the engaged slice's
workflow edits (if any) are sound. You receive `{story, from, to}` on stdin;
`story` carries title, body, acceptance criteria, and tags. Attached plan and step summaries may appear as payload `docs` entries (`name`, `type`, `path`). Open `path`. Read the repository
(Read/Grep); you do not edit.

## Scope first — n/a fast-accept

Decide whether this slice touches **workflow substrate**:

- Project/repo workflows: `.satelle/workflows/**`
- Binary-embedded workflow sources (when the product ships defaults in-tree): the
  embed path under the binary's substrate `workflows/` (in this product tree:
  `internal/config/substrate/workflows/**`). Category-`substrate` markdown under
  `.satelle/` / `docs/` is **not** this gate — that lane is
  [[satelle-substrate-only-check]].

Infer from the payload (body, ACs, plan, step summaries) and from files that
exist in the tree for this slice — you have **no git**. Absence of workflow
mentions and no plan claiming workflow edits **is** the n/a signal.

- **No workflow touch** → accept immediately:
 ```json
 {"decision": "accept", "notes": "workflow: n/a", "reviewed": "<exact words this verdict rests on>"}
 ```
 Never reject for missing evidence of a surface the slice never claimed.

- **Touches workflow** → continue below.

## How to judge (when the slice edits a workflow)

A lifecycle is a DERIVED ROUTE, authored as two TOML halves: `done.toml`
declares the obligations per category, `step.toml` says what discharges each,
and the binary sorts the topology. Read the touched half (and any new reviewer
skills it names). Judge:

1. **Where the gate is bound** — a **gate-specific** reviewer (intended for
 exactly one step) belongs in that step table's `reviewers = [...]`, because a
 gate belongs to the step it ADMITS. A new `[[gate]]` entry for a
 gate-specific check is a reject. An always-on `[[gate]]` is for genuinely
 multi-step reviewers (estimate/actual, step summary).

2. **No over-firing gate** — do not introduce a `[[gate]]` whose `on = [...]`
 names one step that also has recovery inbound, unless the author clearly
 intends always-on re-fire. `on` matches STATUSES, not step table keys — an
 `on` written as a step key silently never fires, so reject it. Prefer the step table's own `reviewers`. A gate in
 a shared catalogue also needs `for = [...]` — the categories whose route it
 belongs to — or it fires on lanes it was never meant for. See `satelle help
 workflows`.

3. **Prose agrees with the route** — description and `[meta]` should not
 contradict the steps and gates the two halves declare.

4. **Skills resolve, and every obligation resolves to a step** — every skill a
 step or gate names must exist under project skills layered over embedded
 defaults. Every obligation listed in a `done.toml` category table must name a
 **step table KEY** in `step.toml`, never a `status`: statuses repeat across
 steps by design, so an obligation written as one is the most likely real
 failure of a route edit.

5. **Obligations, park, cancel and recover preserved** — do not silently drop an
 obligation from a `done.toml` category table, or its `park` / `cancel` /
 `recover` keys, without a stated reason in the plan. An obligation removed is
 a gate removed.

6. **No authored topology** — the binary owns ORDER (a topological sort of
 `requires`) and the synthesised shape: cancel from every non-terminal step,
 park from anywhere, backward movement, park → cancel. A `cancelled` or
 `blocked` authored as a STEP table is a reject; it belongs on the category
 table's `cancel` / `park` key.

Fair gate: judge the change as written, not perfectionism.

- **Accept** when the gate is bound where it belongs, skills resolve,
 obligations and exits are intact, and no topology is authored by hand.
- **Reject** with a specific, fixable note (name the step or gate and the
 rewrite).


## Re-presented edge

Every judging verdict sets `reviewed` to the exact words it rests on. The first verdict on the edge has no prior row, so it judges and sets `reviewed`; a later citation cannot start until that quotation is stored. On a re-presented edge the quotation comparison governs. Open `prior_verdicts`. Take this skill's latest row. If that row has no `reviewed` string, or `reviewed_truncated` is set, re-judge the words this verdict would rest on and set `reviewed` to those exact words. Do not cite an older row that still has a complete `reviewed` string. If that latest row has a complete `reviewed` string, compare those exact words with the words this verdict would rest on. If they are the same words, re-issue that earlier verdict: the same decision, the same `reviewed` string, and notes that cite its `attempt` and skill. An unchanged accept stays accept. An unchanged reject stays reject. A citation does not clear a standing rejection. If the words differ, judge the difference, name what changed in notes, and set `reviewed` to the exact words this verdict rests on. Cite the passages the verdict depends on, not the whole input. Definition-edit fields, edit timestamps, and a selection of story sections are not how unchanged words are established.

## Verdict

```json
{"decision": "accept", "notes": "", "reviewed": "<exact words this verdict rests on>"}
```

`reviewed` is required on every judgment, including the first verdict on this edge. It is the exact words this verdict rests on. Replace the slot; do not omit the field and do not copy the angle-bracket text. A citation of the same words repeats that same `reviewed` string.
