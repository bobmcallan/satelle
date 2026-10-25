---
name: satelle-epic-merged-check
scope: system
type: skill
tags: [type:skill, type:reviewer, type:functional-check]
description: Functional-check for a container's close — rejects while a branch-lane member's `epic/<child-id>` branch tip is not an ancestor of the remote's trunk, i.e. the `merged` step has not put that child on origin. Skips cancelled members and members tagged `lane:trunk`. Deterministic and self-contained; the members are the gate payload's snapshot.
---

# Epic-merged check (container close gate)

**Functional-check** gate on a container's close (`children-resolved`). A child on
the epic-branch lane closed with its slice on a pushed `epic/<child-id>` branch and
nothing on trunk; the container's `merged` step merges those branches and publishes
trunk. This check is what makes that step's work a precondition of closing rather
than a hope: it **rejects until every branch-lane member's branch tip is an
ancestor of the remote's trunk**.

The members are the `children` snapshot the gate is handed (id and status, from
the one membership rule, `epicset`) — this check never computes membership
itself. It skips:

- a **cancelled** member: it never holds the container;
- a member tagged **`lane:trunk`**: it landed on trunk through its own release.

Every other member must have a pushed `epic/<child-id>` whose tip is already on
`<remote>/<trunk>`. A member with no such branch is rejected too: tag it
`lane:trunk` if it was meant to land on trunk itself. A container with no
branch-lane member is accepted.

The trunk and the remote are the ones `satelle trunk sync --json` reports, never a
hardcoded `main` or `origin`. A repo that lands epics through a pull request
overrides this skill by name under `.satelle/skills/`.

The check is the embedded ```check script below — **self-contained**, no
external file (see [[satelle-reviewer-self-contained]]). Exit 0 accepts;
non-zero rejects with notes. **Mechanism, not judgment.** See
[[satelle-agent-model]].

```check
#!/usr/bin/env bash
set -uo pipefail

payload=$(cat)
# Anchored on the id KEY so a story id quoted inside the body cannot win.
sid=$(printf '%s' "$payload" | grep -oE '"id":[[:space:]]*"sty_[a-f0-9]+"' | head -1 | grep -oE 'sty_[a-f0-9]+')
if [ -z "$sid" ]; then
  echo "could not read the story id from the transition payload"
  exit 1
fi

# The members the gate was handed: {"id":"sty_…","status":"…"} objects.
members=$(printf '%s' "$payload" | grep -oE '\{"id":"sty_[a-f0-9]+","status":"[a-z_]+"\}' || true)
if [ -z "$members" ]; then
  echo "$sid has no members in the gate payload; nothing to merge"
  exit 0
fi

# The trunk and the remote are the binary's answer, not a guess.
sync=$(satelle trunk sync --json 2>&1)
trunk=$(printf '%s\n' "$sync" | sed -n 's/^[[:space:]]*"trunk":[[:space:]]*"\([^"]*\)".*/\1/p' | head -1)
remote=$(printf '%s\n' "$sync" | sed -n 's/^[[:space:]]*"remote":[[:space:]]*"\([^"]*\)".*/\1/p' | head -1)
if [ -z "$trunk" ] || [ -z "$remote" ]; then
  echo "cannot resolve trunk and remote: $sync"
  exit 1
fi
git fetch --quiet "$remote" "$trunk" >/dev/null 2>&1 || true

unmerged=""
checked=0
while IFS= read -r member; do
  [ -z "$member" ] && continue
  id=$(printf '%s' "$member" | grep -oE 'sty_[a-f0-9]+')
  status=$(printf '%s' "$member" | sed -n 's/.*"status":"\([a-z_]*\)".*/\1/p')
  [ "$id" = "$sid" ] && continue
  [ "$status" = "cancelled" ] && continue

  story=$(satelle story get "$id" --json 2>&1) || {
    echo "cannot read member $id: $story"
    exit 1
  }
  if printf '%s\n' "$story" | grep -qE '^    "lane:trunk",?$'; then
    continue
  fi
  checked=$((checked + 1))

  branch="epic/$id"
  tip=$(git ls-remote "$remote" "refs/heads/$branch" 2>/dev/null | awk '{print $1}' | head -1)
  if [ -z "$tip" ]; then
    unmerged="$unmerged\n  $id: $remote has no $branch (tag it lane:trunk if it was meant to land on $trunk itself)"
    continue
  fi
  git fetch --quiet "$remote" "$branch" >/dev/null 2>&1 || true
  git merge-base --is-ancestor "$tip" "refs/remotes/$remote/$trunk" 2>/dev/null
  case $? in
    0) ;;
    1) unmerged="$unmerged\n  $id: $branch ($tip) is not on $remote/$trunk" ;;
    *) unmerged="$unmerged\n  $id: cannot compare $branch with $remote/$trunk" ;;
  esac
done <<EOF
$members
EOF

if [ -n "$unmerged" ]; then
  printf "%s has branch-lane members not yet on %s/%s:" "$sid" "$remote" "$trunk"
  printf "$unmerged\n"
  echo "Run the container's merged step: merge each branch and 'satelle trunk publish --story $sid', then present the close again."
  exit 1
fi
echo "$sid: $checked branch-lane member(s) are on $remote/$trunk"
exit 0
```
