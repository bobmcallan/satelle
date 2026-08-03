#!/usr/bin/env bash
# build-version.sh — compute the ldflag version stamp for make build/install.
#
# Usage: scripts/build-version.sh <base-version> [tag-prefix]
#   tag-prefix defaults to "v" (CLI tags are vX); pass "serve-v" for the serve channel.
#
# Prints <base> when HEAD is exactly the tagged release commit and the tree is clean.
# Otherwise prints <base>+<shortsha>[-dirty] so isDevVersion (any string with '+')
# treats the binary as a non-release build (sty_022929ef).
#
# CI release builds do NOT use this script — they stamp via their own ldflags
# in .github/workflows/release.yml, so published artifacts are unaffected.
set -euo pipefail

base="${1:-}"
if [ -z "$base" ]; then
  echo "usage: $0 <base-version> [tag-prefix]" >&2
  exit 2
fi
prefix="${2:-v}"
tag="${prefix}${base}"

# Not a git tree (tarball / exported source) — stamp the base as-is.
if ! git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  printf '%s\n' "$base"
  exit 0
fi

sha=$(git rev-parse --short=12 HEAD 2>/dev/null || true)
if [ -z "$sha" ]; then
  printf '%s\n' "$base"
  exit 0
fi

dirty=""
if [ -n "$(git status --porcelain 2>/dev/null)" ]; then
  dirty="-dirty"
fi

# Released only when the release tag exists and points at HEAD.
tag_commit=$(git rev-parse -q --verify "${tag}^{commit}" 2>/dev/null || true)
head_commit=$(git rev-parse HEAD 2>/dev/null || true)

if [ -n "$tag_commit" ] && [ "$tag_commit" = "$head_commit" ] && [ -z "$dirty" ]; then
  printf '%s\n' "$base"
  exit 0
fi

printf '%s+%s%s\n' "$base" "$sha" "$dirty"
