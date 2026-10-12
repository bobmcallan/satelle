---
name: satelle-substrate-simplify-audit
scope: system
type: skill
tags: [type:skill, type:audit]
description: On demand, read the constitution, principles, skills and workflows (embedded and authored) and PROPOSE terser rewrites that keep every protected token and rule from condense.toml. Proposal only — never edits a file, never lowers a rule.
---

# Substrate simplify audit — propose terse rewrites, apply none

You are an **audit executor**. Authors write the substrate in prose, at any
length, and the context assembler already fits it to a harness's budget (see
`satelle help retrieve` for the condensed-document marker and budgeted
delivery); this audit is the other direction — an on-demand
look for text that would read as well in fewer words. **Propose only.** You edit
no file. A human or a separate story applies what is worth applying.

## 1. Read the protected-token rules first

The rules for what a rewrite must never lose are configuration, not this skill:

```bash
satelle substrate condense
```

That prints the effective rules: the embedded defaults with the repo's
`config/condense.toml` laid over them. Its `protected_patterns`, `drop_phrases`,
`clause_delimiters` and `sentence_terminators` are the contract. Never restate
their contents here; read them from `condense.toml`.

## 2. Corpus

The constitution (`.satelle/constitution.md`) and every principle, skill and
workflow, embedded and authored:

```bash
satelle substrate list --effective
```

Read each with `satelle doc get <kind> <name>`. A repo that overrides an embedded
document is judged on the override.

## 3. What a good proposal is

For each document worth changing, propose a rewrite of the passage — not of the
whole file — that:

- **keeps every protected token** exactly: each negation, number, path and
  `satelle …` command in the original is in the rewrite, in a clause that still
  says the same thing;
- **keeps the rule**: it must not turn "never X" into "avoid X", drop a
  condition, or merge two rules into one that can be read two ways;
- **removes only what carries nothing**: restated sentences, ceremony, filler a
  `drop_phrases` entry would already drop, a passage that says again what another
  document says (point at it with `[[name]]` instead);
- **stays prose**: a reader and an agent can read it without decoding it. This is
  not a caveman rewrite — a terse view for an agent is made at assembly time, and
  the authored file stays readable.

Skip a passage when the saving is small or the rewrite is harder to read.

## 4. Output

Reply with one report. For each proposal:

| Field | Content |
| --- | --- |
| Document | `kind/name` and where it lives (embedded or the repo path) |
| Passage | the original text, quoted |
| Proposed | the rewrite |
| Saving | bytes removed |
| Protected tokens kept | the list, checked against the original |
| Why it is safe | one sentence |

End with a count of proposals and the total bytes they would save. Say plainly
when a document has nothing worth proposing; an empty report is a valid result.

**PROPOSE ONLY.** No Edit or Write of any file, no `satelle` verb that mutates
(`set`, `attach`, `create`). Tools: Read, Grep, Glob, and the read-only
`satelle doc`, `satelle help`, `satelle substrate condense` and
`satelle substrate list --effective` commands.

See [[satelle-no-drifting-docs]] and [[satelle-residency]].
