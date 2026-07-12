---
name: satelle-substrate-audit
description: Audit satelle's EMBEDDED system substrate — the skills and principles under internal/config/substrate that ship inside the binary as canonical defaults — for three qualities every embedded default must earn: focus (one responsibility, no overlap with a sibling), token economy (the always-resident frontmatter description especially, then the body), and repo-agnostic fitness (the constitution's test — it must make sense for ANY repo, with no this-repo story ids, deploy mechanics, dead tags, or opinions that belong in .satelle/). Use when asked to review, tighten, optimize, slim, or lint satelle's system/inbuilt skills or principles, when a substrate description feels bloated, when checking an embedded default for repo leakage, or after adding/editing a file under internal/config/substrate. Reports findings + fixes; does not edit unless asked.
---

# satelle substrate audit

satelle ships a small set of **canonical defaults** embedded in the binary
(`internal/config/substrate/{skills,principles}`). `satelle init` seeds them into
a fresh repo as editable substrate. Because they travel to *every* repo and are
injected into agent context, each one must be **focused, lean, and
repo-agnostic** — a bloated description taxes every session; a this-repo opinion
baked into a default breaks the product's separability.

This skill audits that corpus against three axes and reports concrete fixes. It
**judges and proposes**; it edits files only when the user asks it to apply fixes.

The **review-only invariant** for reviewer skills (a gate must judge, never
mutate) is owned by the `reviewer-skill-author` skill — defer to it for that
check and don't re-derive it here. This skill owns what that one doesn't: focus,
token cost, and embedded-default fitness across the whole corpus.

## Scope

Audit these, in order of leverage:

```
internal/config/substrate/skills/*.md        # gates, summariser, advisor
internal/config/substrate/principles/*.md     # agent-model, agent-goals, …
```

Workflows (`*-workflow.md`) are DAGs, not prose — out of scope here unless asked.

Ground the audit in three references (read the two files; the constitution is
already in session context):
- **`[[satelle-constitution]]`** (session context) — configuration-over-code, the
  mechanism-vs-substrate split, and the substrate-naming rules.
- **`.satelle/principles/satelle-repo-agnostic.md`** — the order-zero test:
  *if another repo installs satelle, does this default still make sense?*
- **`reviewer-skill-author` skill** — the review-only contract, for reviewer files.

## The three axes

Judge every file on all three. A finding names the file, the axis, a severity
(**blocker** breaks the product / a gate / repo-agnosticism · **tighten** wastes
tokens or focus · **nit** cosmetic), the concrete problem, *why it matters*, and
the specific fix.

### 1. Focus — one artifact, one responsibility

An embedded default earns its place by doing exactly one thing the corpus needs.

- **A gate gates one edge.** A reviewer skill judges a single transition against a
  single rubric. Flag a skill that bundles two edges, or narrates how the
  *executor* should do the work (that belongs in an executor rubric, not a gate).
- **A principle states one idea.** Flag a principle that re-explains what a
  sibling or the constitution already owns instead of linking it — restated
  doctrine is both a focus miss and a token cost. (`satelle-agent-model` is the
  one to watch: at ~1500 words it re-derives config-over-code that the
  constitution already states — prefer a `[[link]]` over a re-explanation.)
- **No two files cover the same ground.** Scan for overlap across the corpus;
  when two artifacts say the same thing, merge or cross-link — never duplicate a
  paragraph that will now drift out of sync in two places.
- **The verdict contract is stated once.** Reviewer skills share one
  `{"decision","notes"}` shape; a file re-explaining it at length past the single
  canonical block is unfocused.

### 2. Token economy — earn every token, the description most of all

Two tiers cost differently:
- The frontmatter **`description` is always-resident** — it is injected as
  metadata for triggering (skills) and into session context (session-tagged
  principles). It is paid on *every* turn, so it must be the leanest thing in the
  file.
- The **body loads on trigger** — it can be longer, but every paragraph must add
  a distinct load-bearing idea.

Flag, on the description:
- **Restating the body.** The description says *when to use / which edge / what it
  judges / that a repo may override* — a tight ~1–2 sentences. If it recaps the
  body's mechanics, cut it back to the trigger + the one-line what.
- **Changelog cruft.** Story/task ids (`sty_…`, `tsk_…`), "(supersedes …)", dates,
  and "shipped by / adapted from" belong in git history or the body's `See` line,
  never in the always-resident metadata — they pin a generic default to this
  repo's timeline and cost tokens forever. This is the single highest-yield cut.
- **Hedging and ceremony.** "an isolated, read-only reviewer that judges whether…"
  said three times, "the embedded order-zero default named by the baseline
  workflow; a repo may override it under .satelle/skills" — the override clause is
  true of every embedded skill; state it in the shortest canonical form (or drop
  it from the description and keep it in the body).

Flag, on the body:
- Repeated caveats, a re-explained verdict block, or prose that a shorter sentence
  covers. Don't slash for its own sake — a gate's judging rules must stay
  unambiguous — but cut anything that doesn't change what the reader does.

Rough budgets (guides, not gates): a gate skill description ≈ **35–60 words**; a
principle body earns length only when each section carries a distinct idea.

### 3. Repo-agnostic fitness — the constitution's test, applied to the default

An embedded default must make sense for **any** repo. Run the repo-agnostic test
on each file and flag leakage:

- **This-repo history.** `sty_…` / `tsk_…` ids, version numbers, this repo's
  timeline — a generic default must not cite the dogfood repo's stories.
- **This-repo mechanics.** `.version`, `install.sh`, `make install`,
  `systemctl … satelle.service`, GitHub Actions, a specific release/deploy flow —
  these are *this* repo's discipline and belong in `.satelle/`, never in the
  embedded default. (Gate skills should not name a deploy command at all.)
- **Opinions beyond the required structure.** A default that assumes a lifecycle
  richer than the generic canonical solution, or bakes an opinion the constitution
  says lives in substrate, is a blocker — name what to move to `.satelle/`.
- **Dead or wrong tags.** `principles:global` is a no-op (only `principles:session`
  is the system-residency marker) — flag it as stale. Principles must not carry
  inert `scope:` (residency is the tag alone; `scope:` remains a workflow field).
  Check `type:` and `applies_to` are present and correct for the artifact's kind.
- **Naming.** Conform to the constitution's substrate-naming: a stage gate is
  `satelle-<object>-<stage>-review`, a structure reviewer `satelle-<object>-review`,
  a principle is bare kebab-case. Flag a name that misencodes the artifact's kind.

## How to run the audit

1. Read the two reference files (§Scope) and glob the corpus.
2. Read every file in scope. For the description axis, count words; for the
   repo-agnostic axis, grep the corpus for the tell-tale leaks in one pass:
   ```bash
   grep -rnE 'sty_[0-9a-f]|tsk_[0-9a-f]|install\.sh|\.version|systemctl|make install|principles:global' \
     internal/config/substrate/skills internal/config/substrate/principles
   ```
   Treat hits as *candidates* — judge each in context (a body `See`-line
   cross-reference differs from a story id buried in an always-resident
   description).
3. Judge each file on all three axes; collect findings.
4. Report using the structure below. If the user asked to apply fixes, edit the
   files, keep every reviewer's judging rules intact, and re-run
   `satelle skill validate <name>` / `satelle principle validate <name>` on each
   changed file.

## Report structure

Lead with the corpus verdict, then per-file findings, then a summary table.

```
## Substrate audit — <N> skills, <M> principles

**Verdict:** <one line: overall health + the highest-yield cut>

### <file-name>
- **[blocker|tighten|nit] <axis>** — <problem>. Why: <cost/risk>. Fix: <specific edit>.
- …

### Corpus-level
- <cross-file overlap, naming drift, or a pattern repeated across files>

### Summary
| File | Focus | Tokens | Repo-agnostic | Top fix |
|------|-------|--------|---------------|---------|
| satelle-story-done-review | ok | -18w desc | ok | drop override clause from desc |
| … | | | | |
```

Prefer a few high-leverage findings over an exhaustive nit list — the point is a
leaner, more portable default set, not a longer report. When a file is already
tight, say so; a clean bill is a valid finding.
