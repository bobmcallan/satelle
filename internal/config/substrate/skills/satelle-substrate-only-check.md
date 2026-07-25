---
name: satelle-substrate-only-check
scope: system
type: skill
tags: [type:skill, type:reviewer, type:functional-check]
description: Functional-check gate for substrate workflow close (in_progress → done) — rejects if the story's committed slice touches any path outside .satelle/, docs/, the binary managed footprint (root .gitignore; harness hook scaffolds under .claude/ and .grok/), or [gate] edit_exempt_paths, catching code mis-filed under category substrate that would otherwise skip the project workflow's plan/code-ac/integration gates. Deterministic, no LLM; self-contained per satelle-reviewer-self-contained.
---

# Substrate-only check (substrate workflow close gate)

**Functional-check** gate on the substrate workflow's close. The workflow
DECLARES it as a scoped reviewer node (`on="done"`); on `in_progress → done` it
verifies the story's committed slice touched **only** substrate-lane paths:
markdown under `.satelle/` (workflows, skills, principles, documents, tasks),
`docs/`, the binary's own **managed footprint** outside `.satelle/` (the root
`.gitignore` managed block and harness hook scaffolds under `.claude/` and
`.grok/` that `satelle init` deploys), or any prefix in `[gate]
edit_exempt_paths` in `satelle.toml` (repo-side extension of the same lane —
unchanged semantics, a different axis from the edit gate's "editable without
an engaged story"). Any other path (a `.go` file, `cmd/`, build/CI config)
means the change is **not** substrate-only and belongs on the project
workflow — the close is **rejected**.

Why the managed footprint rides this lane: those files are binary-emitted,
binary-managed **process configuration**, not product code. No build/test lane
can meaningfully vet a `.gitignore` block or a harness hook scaffold, and
routing them through the project/code workflow adds cost without judgment.
They belong here by default so an init-heal or footprint story is not forced
onto a heavy lane.

The check is the embedded ```check script below — **self-contained**, no
external file (see [[satelle-reviewer-self-contained]]). satelle runs it in the
repo root with `{story, from, to}` on stdin; it reads the story id, finds the
story's own commit(s) by that id (the `(sty_…)` commit trailer), and unions the
paths they touched. Exit 0 accepts; non-zero rejects with the offending paths
as the notes the executor fixes. It is **mechanism, not judgment** — the
deterministic gate path — so the read-only LLM-reviewer invariant is untouched.
See [[satelle-agent-model]].

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
# Allowed prefixes: authored substrate (.satelle/, docs/), the binary managed
# footprint init writes outside .satelle/ (root .gitignore; harness hook
# scaffolds under .claude/ and .grok/ — process config, not product code), plus
# any [gate] edit_exempt_paths in satelle.toml (repo-side extension knob,
# unchanged semantics). Product code (.go, cmd/, Makefile, CI) still fails.
allow='\.satelle/|docs/|\.gitignore$|\.claude/|\.grok/'
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
