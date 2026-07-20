---
name: satelle-parallel-ready-check
scope: project
type: skill
tags: [type:skill, type:reviewer, type:functional-check]
description: >-
  Functional-check gate on entry to ready for satelle-parallel-story-workflow —
  rejects if HEAD is main/master, or the current branch is not pushed to
  origin. Exit 0 accepts. Self-contained; no LLM.
---

# Parallel ready check (integration → ready)

**Functional-check** on entry to `ready`. Declared as a scoped reviewer node
(`on="ready"`). Asserts the leaf is on a **story branch** (not main) and that
branch is **pushed** to `origin` — the definition of ready for parallel epic
children (green was proven in the integration step; this check enforces branch
discipline).

Self-contained ```check```; runs in the worktree repo root with transition
payload on stdin. Exit 0 accepts, non-zero rejects with notes.

```check
#!/usr/bin/env bash
set -uo pipefail
payload=$(cat)
sid=$(printf '%s' "$payload" | grep -oE '"id":"sty_[a-f0-9]+"' | head -1 | grep -oE 'sty_[a-f0-9]+')
branch=$(git rev-parse --abbrev-ref HEAD 2>/dev/null || true)
if [ -z "$branch" ] || [ "$branch" = "HEAD" ]; then
  echo "detached HEAD or no branch — checkout story/<id> before ready"
  exit 1
fi
if [ "$branch" = "main" ] || [ "$branch" = "master" ]; then
  echo "on $branch — parallel-story ready requires branch story/<id>, not main"
  exit 1
fi
# Prefer story/<id> naming; allow other non-main branches but warn in notes path via echo
if [ -n "$sid" ] && [ "$branch" != "story/$sid" ]; then
  echo "note: branch is '$branch' (expected story/$sid); continuing if pushed"
fi
# Must have upstream and be in sync with (or ahead is ok only if pushed)
if ! git rev-parse --abbrev-ref '@{u}' >/dev/null 2>&1; then
  echo "branch $branch has no upstream — git push -u origin HEAD before ready"
  exit 1
fi
# Reject if local is ahead of upstream (not fully pushed)
ahead=$(git rev-list --count '@{u}..HEAD' 2>/dev/null || echo 99)
if [ "${ahead:-99}" != "0" ]; then
  echo "branch $branch is $ahead commit(s) ahead of upstream — push before ready"
  exit 1
fi
echo "ready-ok: branch $branch pushed (story ${sid:-unknown})"
```
