---
name: satelle-agents-layer-only-check
scope: system
type: skill
tags: [type:skill, type:reviewer, type:functional-check]
description: Functional-check for the agents-repair close — rejects any path other than .satelle/workflows/agents*.toml in the story change set (recorded + live with substrate leg + commits naming it), including uncommitted worktree paths; rejects empty sets. Deterministic, self-contained.
---

# Agents-layer-only check (agents-repair close gate)

**Functional-check** gate on the agents-repair lane's close. On
`in_progress → done` it verifies the story's **change set** touched **only**
the agents layer: the `agents*.toml` bindings directly under
`.satelle/workflows/` (`agents.toml`, `agents.local.toml`, …).

Any other path — other substrate under `.satelle/`, `docs/`, code, build or CI
config, committed or uncommitted — means the slice is not an agents-layer
repair and belongs on another lane: the close is **rejected**. An **empty**
change set is also **rejected**: empty is not evidence of a repair.

The lane exists because a broken binding refuses every story that dispatches
through it, the fix story included; this check keeps the lane from carrying
any other work.

## Evidence channels (union — no commit required)

The slice is the **union** of:

1. **Recorded** — `satelle story diff <id> --recorded` (change_record rows)
2. **Live + substrate** — `satelle story diff <id> --include-substrate` (git
   worktree since engagement plus mtime-changed substrate, so git-ignored
   `.satelle/` is visible)
3. **Commits** — any commit whose **subject** names the story id; a mention
   only in another commit's body is a citation, not ownership

The check is the embedded ```check script below — **self-contained**, no
external file (see [[satelle-reviewer-self-contained]]). Exit 0 accepts;
non-zero rejects with notes. **Mechanism, not judgment.** See
[[satelle-agent-model]].

```check
#!/usr/bin/env bash
set -uo pipefail
payload=$(cat)
sid=$(printf '%s' "$payload" | grep -oE '"id":"sty_[a-f0-9]+"' | head -1 | grep -oE 'sty_[a-f0-9]+')
if [ -z "$sid" ]; then
  echo "could not read the story id from the transition payload"
  exit 1
fi

extract_files() {
  # stdin: story-diff JSON; $1: the list key (default files); stdout: one entry per line
  local raw key="${1:-files}"
  raw=$(cat)
  [ -z "$raw" ] && return 0
  if command -v python3 >/dev/null 2>&1; then
    printf '%s' "$raw" | python3 -c "import json,sys
try:
 d=json.load(sys.stdin)
 print('\n'.join(d.get(sys.argv[1]) or []))
except Exception:
 pass" "$key" 2>/dev/null || true
  else
    printf '%s' "$raw" | grep -oE "\"$key\":\\[[^]]*\\]" | head -1 | sed 's/^"[^"]*"://' | tr ',' '\n' | grep -oE '"[^"]+"' | tr -d '"' | grep -v '^$' || true
  fi
}

# Channel 1: recorded change_record union.
rec=""
unres=""
if recorded=$(satelle story diff "$sid" --recorded 2>/dev/null); then
  rec=$(printf '%s' "$recorded" | extract_files)
  unres=$(printf '%s' "$recorded" | extract_files unresolved_records)
fi

# Channel 2: live git + substrate mtime (no baseline → empty).
liv=""
if live=$(satelle story diff "$sid" --include-substrate 2>/dev/null); then
  liv=$(printf '%s' "$live" | extract_files)
fi

# Channel 3: commits whose SUBJECT names the story; a body citation is not
# ownership, so --grep is only the candidate filter.
com=""
owned=""
for c in $(git log --grep="$sid" --format=%H 2>/dev/null || true); do
  subj=$(git show -s --format=%s "$c" 2>/dev/null || true)
  case "$subj" in *"$sid"*) owned="$owned $c" ;; esac
done
if [ -n "$owned" ]; then
  com=$(for c in $owned; do git show --name-only --format= "$c" 2>/dev/null; done | grep -v '^$' || true)
fi

changed=$(printf '%s\n%s\n%s\n' "$rec" "$liv" "$com" | grep -v '^$' | sort -u)

if [ -n "$unres" ]; then
  echo "recorded rows used as recorded (range unresolved):"
  printf '%s\n' "$unres"
fi

if [ -z "$changed" ]; then
  echo "no change set found for $sid — nothing recorded, nothing in the working tree since engagement (incl. git-ignored substrate), and no commit subject names it. An empty change set is not evidence of an agents-layer repair."
  exit 1
fi

# The agents layer, and nothing else.
allow='^\.satelle/workflows/agents[^/]*\.toml$'
offenders=$(printf '%s\n' "$changed" | grep -vE "$allow" || true)
if [ -n "$offenders" ]; then
  echo "the slice for $sid touches paths outside .satelle/workflows/agents*.toml — the agents-repair lane carries only an agents-layer repair; move this work to its own story (category substrate, fix, feature, …) or revert it:"
  printf '%s\n' "$offenders"
  exit 1
fi
echo "agents-layer-only slice confirmed for $sid:"
printf '%s\n' "$changed"
```
