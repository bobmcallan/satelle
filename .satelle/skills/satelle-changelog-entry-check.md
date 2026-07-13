---
name: satelle-changelog-entry-check
scope: project
type: skill
tags: [type:skill, type:reviewer, type:functional-check]
description: Functional-check gate for release close (release → done) — rejects if CHANGELOG.md has no level-2 entry for the satelle.version on HEAD. Deterministic, no LLM; self-contained.
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
# Read version from HEAD's .version (the release commit).
VER=$(awk '$1=="satelle.version:" {print $2}' .version 2>/dev/null)
if [ -z "$VER" ]; then
  echo "could not read satelle.version from .version on HEAD"
  exit 1
fi
if [ ! -f CHANGELOG.md ]; then
  echo "CHANGELOG.md missing — add an entry for $VER before closing release"
  exit 1
fi
# Level-2 header: ## [X.Y.Z]  (date optional)
if ! grep -qE "^## \[${VER}\]" CHANGELOG.md; then
  echo "CHANGELOG.md has no entry for version $VER — add '## [$VER] - <date>' (with Breaking subsection if needed) in the release step before close"
  exit 1
fi
echo "changelog entry present for $VER"
```
