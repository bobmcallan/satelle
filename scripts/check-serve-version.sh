#!/usr/bin/env bash
# check-serve-version — fail when serve-path sources changed since the last
# serve-v* tag but satelle-serve.version was not advanced (sty_4a5c6924).
# Exit 0 when no serve-path change, or when the serve version line advanced
# relative to the tagged commit. Configuration-over-code: the path list and
# .version key are the contract; this script is the gate mechanism.
set -euo pipefail

ROOT=$(git rev-parse --show-toplevel 2>/dev/null) || {
  echo "check-serve-version: not a git repo" >&2
  exit 1
}
cd "$ROOT"

SERVE_PATHS=(
  'cmd/satelle-serve/'
  'internal/web/'
  'internal/mirror/'
  'internal/buildinfo/'
)

# Latest serve-v* tag (not latest CLI tag).
BASE=$(git tag -l 'serve-v*' --sort=-v:refname | head -1 || true)
if [ -z "$BASE" ]; then
  echo "check-serve-version: no serve-v* tag yet — ok (first serve release uses current .version)"
  exit 0
fi

changed=$(git diff --name-only "${BASE}..HEAD" -- "${SERVE_PATHS[@]}" 2>/dev/null || true)
# Also count unstaged/staged worktree changes on serve paths (pre-commit style).
wt=$(git diff --name-only HEAD -- "${SERVE_PATHS[@]}" 2>/dev/null || true)
cached=$(git diff --name-only --cached -- "${SERVE_PATHS[@]}" 2>/dev/null || true)
all=$(printf '%s\n%s\n%s\n' "$changed" "$wt" "$cached" | grep -v '^$' | sort -u || true)

if [ -z "$all" ]; then
  echo "check-serve-version: no serve-path changes since $BASE"
  exit 0
fi

# Version at BASE vs HEAD (and working tree .version).
ver_at() {
  git show "$1:.version" 2>/dev/null | awk '$1=="satelle-serve.version:" {print $2; exit}'
}
base_ver=$(ver_at "$BASE")
head_ver=$(awk '$1=="satelle-serve.version:" {print $2; exit}' .version)
if [ -z "$head_ver" ]; then
  echo "check-serve-version: satelle-serve.version missing from .version" >&2
  exit 1
fi
if [ -z "$base_ver" ]; then
  echo "check-serve-version: tag $BASE has no satelle-serve.version — require current $head_ver present"
  exit 0
fi
if [ "$head_ver" = "$base_ver" ]; then
  echo "check-serve-version: serve-path changes since $BASE but satelle-serve.version still $base_ver" >&2
  echo "changed:" >&2
  printf '  %s\n' $all >&2
  echo "bump satelle-serve.version in .version (and changelog serve-v entry) before release" >&2
  exit 1
fi
echo "check-serve-version: ok — serve-path changed since $BASE; version $base_ver → $head_ver"
