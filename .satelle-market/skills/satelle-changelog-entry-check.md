---
name: satelle-changelog-entry-check
scope: project
type: skill
tags: [solo-dev, gate, functional-check, release]
description: Functional-check gate for release close (release → done). Rejects if the changelog (example: CHANGELOG.md) has no level-2 entry for the version on HEAD. Deterministic, no LLM; self-contained.
---

# Changelog entry check (release close gate)

**Functional-check** on `release → done`. The project workflow declares it as a
scoped reviewer node (`on="done"`). It verifies the release commit on HEAD
bumped `.version` **and** `CHANGELOG.md` carries a matching `## [X.Y.Z]` entry
for that version. A release without a changelog entry fails closed.

The check runs on `done` (not entry to `release`) because the `.version` bump
and changelog entry are written by the in-loop release step and only land in
the release commit. See [[satelle-agent-model]], [[satelle-reviewer-self-contained]].

```check
#!/usr/bin/env bash
set -uo pipefail
# Independent versions (<story-id>): CLI key satelle.version → ## [X.Y.Z];
# serve key satelle-serve.version → ## [serve-vY]. Both required when present.
CLI=$(awk '$1=="satelle.version:" {print $2}' .version 2>/dev/null)
SERVE=$(awk '$1=="satelle-serve.version:" {print $2}' .version 2>/dev/null)
if [ -z "$CLI" ]; then
  echo "could not read satelle.version from .version on HEAD"
  exit 1
fi
if [ ! -f CHANGELOG.md ]; then
  echo "CHANGELOG.md missing — add an entry for $CLI before closing release"
  exit 1
fi
if ! grep -qE "^## \[${CLI}\]" CHANGELOG.md; then
  echo "CHANGELOG.md has no entry for CLI version $CLI — add '## [$CLI] - <date>' before close"
  exit 1
fi
if [ -n "$SERVE" ] && ! grep -qE "^## \[serve-v${SERVE}\]" CHANGELOG.md; then
  echo "CHANGELOG.md has no entry for serve version $SERVE — add '## [serve-v$SERVE] - <date>' before close"
  exit 1
fi
echo "changelog entry present for CLI $CLI${SERVE:+ and serve-v$SERVE}"
```
