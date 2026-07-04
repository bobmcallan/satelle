---
name: satelle-substrate-only-check
scope: project
type: skill
tags: [type:skill, type:reviewer, type:functional-check]
description: Functional-check gate for the substrate workflow's close (in_progress → done). It confirms the story's committed slice is SUBSTRATE-ONLY — every touched path is under .satelle/, docs/, or a configured [gate] edit_exempt_paths prefix (a harness authoring dir like .claude/) — and rejects if any code, build, CI, or command path appears. This is the guardrail that keeps the category-driven workflow choice honest: because category "substrate" selects the lighter satelle-substrate-workflow, a code change mis-filed there would skip the project workflow's plan/code-ac/integration gates; this check catches it because the diff itself betrays the code. Local, deterministic (the command IS the decision — no LLM). Self-contained, per satelle-reviewer-self-contained.
---

# Substrate-only check (substrate workflow close gate)

This is a **functional-check** gate on the substrate workflow's close. The workflow
DECLARES it as a scoped reviewer node (`on="done"`), so on the `in_progress → done`
transition it verifies that the story's committed slice touched **only** authored
substrate — markdown under `.satelle/` (workflows, skills, principles, documents,
tasks), `docs/`, or any prefix listed in `[gate] edit_exempt_paths` in
`satelle.toml` (a harness authoring dir like `.claude/` that holds authored
markdown, not product code — the same knob the edit gate uses). If any other path
(a `.go` file, `cmd/`, build/CI config) is in the slice, the change is **not**
substrate-only and belongs on the project workflow, so the close is **rejected**.

The check is the embedded ```check script below — **self-contained**, referencing
no external file (see [[satelle-reviewer-self-contained]]). satelle runs it in the
repo root with the transition payload (`{story, from, to}`) on stdin; it reads the
story id, finds the story's own commit(s) by that id (the conventional
`(sty_…)` commit trailer), and unions the paths they touched. Exit 0 accepts, a
non-zero exit rejects with the offending paths as the notes the executor fixes. It
is **mechanism, not judgment** — the deterministic gate path — so the read-only
LLM-reviewer invariant is untouched. See [[satelle-agent-model]].

Because the slice is identified by the story's committed id, the executor must
**commit + push the substrate slice IN-LOOP at `in_progress`** (a single commit
whose subject ends in the story id) before requesting the close.

```check
#!/usr/bin/env bash
set -uo pipefail
payload=$(cat)
sid=$(printf '%s' "$payload" | grep -oE '"id":"sty_[a-f0-9]+"' | head -1 | grep -oE 'sty_[a-f0-9]+')
if [ -z "$sid" ]; then
  echo "could not read the story id from the transition payload"
  exit 1
fi
commits=$(git log --grep="$sid" --format=%H 2>/dev/null)
if [ -z "$commits" ]; then
  echo "no commit mentioning $sid found — commit + push the substrate slice IN-LOOP at in_progress (subject ending in $sid) before closing"
  exit 1
fi
changed=$(for c in $commits; do git show --name-only --format= "$c"; done | grep -v '^$' | sort -u)
# Allowed prefixes: authored substrate (.satelle/, docs/) plus any [gate]
# edit_exempt_paths configured in satelle.toml — a harness authoring dir (e.g.
# .claude/) holds authored markdown, not product code, so it rides the substrate
# lane too. Reuses v0.0.104's edit_exempt_paths knob (config-over-code, no new
# knob); unset → .satelle/ + docs/ exactly as before.
allow='\.satelle/|docs/'
extra=$(grep -E '^[[:space:]]*edit_exempt_paths' .satelle/satelle.toml 2>/dev/null | grep -oE '"[^"]+"' | tr -d '"')
for p in $extra; do
  esc=$(printf '%s' "$p" | sed 's#[^A-Za-z0-9/]#\\&#g')
  allow="$allow|$esc"
done
offenders=$(printf '%s\n' "$changed" | grep -vE "^($allow)" || true)
if [ -n "$offenders" ]; then
  echo "the slice for $sid touches non-substrate paths — this is not a substrate-only change; use the project workflow (category fix/feature/chore):"
  printf '%s\n' "$offenders"
  exit 1
fi
echo "substrate-only slice confirmed for $sid:"
printf '%s\n' "$changed"
```
