---
name: satelle-docs-only-check
scope: system
type: skill
tags: [type:skill, type:reviewer, type:functional-check]
description: Functional-check for the docs lane's close — rejects any path in the story change set that is not doc-typed, and rejects an empty set. The doc-path pattern is the gate's configuration (default markdown); a repo widens it by overriding this skill. Deterministic and self-contained.
---

# Docs-only check (docs lane close gate)

**Functional-check** gate on the `docs` lane's close. On `in_progress → done` it
verifies the story's **change set** touched **only doc-typed paths**.

Any other path — a source file, a build config, anything the pattern does not
name — means the slice is not a documentation slice: it took the light lane
while doing code-shaped work, so the close is **rejected** and the slice belongs
on the repo's project lane. An **empty** change set is also **rejected**: empty
is not evidence of a documentation change.

## The pattern is configuration

`doc_paths` at the top of the check below is an extended regex over the change
set, and it is the whole file-type scope of this lane. It ships as **markdown
only** — the one doc form every repo has. A repo that keeps prose elsewhere
(`docs/` assets, `*.rst`, `*.adoc`) widens it by **overriding this skill** under
`.satelle/skills/`; a repo that wants a narrower lane narrows it the same way.
Never a branch in the binary: the binary runs the enumeration mechanism
(`satelle story diff`), and the gate decides.

## Evidence channels (union — no commit required)

1. **Recorded** — `satelle story diff <id> --recorded` (change_record rows)
2. **Live + substrate** — `satelle story diff <id> --include-substrate` (git
   worktree since engagement, plus mtime-changed substrate, so prose under a
   git-ignored authored dir is visible too)
3. **Commits** — any commit whose message mentions the story id

The check is the embedded ```check script below — **self-contained**, no
external file (see [[satelle-reviewer-self-contained]]). Exit 0 accepts;
non-zero rejects with notes. **Mechanism, not judgment.** See
[[satelle-agent-model]].

```check
#!/usr/bin/env bash
set -uo pipefail

# THE CONFIGURATION: which paths count as documentation. Override this skill to
# change it — see "The pattern is configuration" above.
doc_paths='\.md$'

payload=$(cat)
# Anchored on the id KEY so a story id quoted inside the body cannot win.
sid=$(printf '%s' "$payload" | grep -oE '"id":[[:space:]]*"sty_[a-f0-9]+"' | head -1 | grep -oE 'sty_[a-f0-9]+')
if [ -z "$sid" ]; then
  echo "could not read the story id from the transition payload"
  exit 1
fi

extract_files() {
  # stdin: story-diff JSON; stdout: one path per line
  local raw
  raw=$(cat)
  [ -z "$raw" ] && return 0
  if command -v python3 >/dev/null 2>&1; then
    printf '%s' "$raw" | python3 -c "import json,sys
try:
 print('\n'.join(json.load(sys.stdin).get('files') or []))
except Exception:
 pass" 2>/dev/null || true
  else
    printf '%s' "$raw" | tr -d ' \n' | sed -n 's/.*\"files\":\[\([^]]*\)\].*/\1/p' | tr ',' '\n' | tr -d '"' | grep -v '^$' || true
  fi
}

rec=""
if recorded=$(satelle story diff "$sid" --recorded 2>/dev/null); then
  rec=$(printf '%s' "$recorded" | extract_files)
fi

liv=""
if live=$(satelle story diff "$sid" --include-substrate 2>/dev/null); then
  liv=$(printf '%s' "$live" | extract_files)
fi

com=""
commits=$(git log --grep="$sid" --format=%H 2>/dev/null || true)
if [ -n "$commits" ]; then
  com=$(for c in $commits; do git show --name-only --format= "$c" 2>/dev/null; done | grep -v '^$' || true)
fi

changed=$(printf '%s\n%s\n%s\n' "$rec" "$liv" "$com" | grep -v '^$' | sort -u)
if [ -z "$changed" ]; then
  echo "no change set found for $sid — nothing recorded, nothing in the working tree since engagement, and no commit mentions it. Empty is not evidence of a documentation change."
  exit 1
fi

offenders=$(printf '%s\n' "$changed" | grep -vE "$doc_paths" || true)
if [ -n "$offenders" ]; then
  echo "the slice for $sid touches paths that are not doc-typed (pattern $doc_paths) — this is not a documentation-only change; move it to the repo's project lane, or widen the pattern by overriding this skill:"
  printf '%s\n' "$offenders"
  exit 1
fi
echo "docs-only slice confirmed for $sid:"
printf '%s\n' "$changed"
```
