---
name: satelle-push-review
scope: project
type: skill
tags: [type:skill, type:reviewer, type:functional-check]
description: Functional-check gate for the `push` step. Confirms the pushed HEAD bumped satelle.version + stamped satelle.build, the commit carries no AI attribution, the GitHub Actions `test` run for HEAD concluded success, and the version-gated `release` published the tag v<version>. Emits a PR-style summary document. Deterministic — the embedded check IS the verdict; a missing bump, a red/absent run, or an unpublished release rejects. Self-contained (no external script), per satelle-reviewer-self-contained. Replaces satelle-commit-push-review.
---

# Push gate (functional check)

This is a **functional-check** gate on the `push` step. The check is the embedded,
self-contained check script below, referencing no external file (see
[[satelle-reviewer-self-contained]]). satelle runs it in the repo root on the gated
transition; exit 0 accepts, non-zero rejects with the output tail as notes.

It verifies, deterministically (mechanism, not judgment — the read-only reviewer
invariant holds): (1) HEAD **bumped** `satelle.version` and **stamped** `satelle.build`
in `.version`; (2) the commit carries **no AI attribution**; (3) the `test` run for
HEAD **concluded success**; (4) the `release` workflow **published** the tag
`v<satelle.version>`. Then it emits a PR-style summary into the OKF sub-bundle
`.satelle/documents/story-implementation-summary/`.
See [[satelle-agent-model]].

```check
#!/usr/bin/env bash
set -uo pipefail

sha="$(git rev-parse HEAD)"
subject="$(git log -1 --format=%s)"
sty="$(printf '%s' "$subject" | grep -oE 'sty_[0-9a-f]+' | head -1 || true)"
vdiff="$(git show HEAD -- .version)"

# 1. .version bumped + dated in HEAD.
if ! printf '%s' "$vdiff" | grep -q '^+satelle\.version:'; then
  echo "push-review: HEAD did not bump satelle.version in .version" >&2
  exit 1
fi
if ! printf '%s' "$vdiff" | grep -q '^+satelle\.build:'; then
  echo "push-review: HEAD did not stamp satelle.build in .version" >&2
  exit 1
fi
ver="$(awk '$1=="satelle.version:"{print $2}' .version)"

# 2. no AI attribution — detected STRUCTURALLY, not by substring-grepping prose, so
# a commit that merely DESCRIBES the attribution check (like this skill's own fixes)
# is not caught (sty_db4f96e9). Co-authors are read from the PARSED trailer block (a
# mid-prose mention is not a trailer); the tool line is matched by its distinctive
# full form, which ordinary prose does not reproduce.
if [ -n "$(git log -1 --format='%(trailers:key=Co-authored-by,valueonly)' | tr -d '[:space:]')" ]; then
  echo "push-review: commit carries a Co-authored-by trailer (attribution forbidden in this repo)" >&2
  exit 1
fi
if git log -1 --format='%B' | grep -qF 'Generated with [Claude Code](https://claude.com/claude-code)'; then
  echo "push-review: commit carries a tool-attribution line (forbidden in this repo)" >&2
  exit 1
fi

# 3. test run for HEAD concluded success.
if ! command -v gh >/dev/null 2>&1; then
  echo "push-review: gh CLI unavailable — cannot verify CI for ${sha}" >&2
  exit 1
fi
tid="$(gh run list --commit "${sha}" --workflow test --limit 1 --json databaseId -q '.[0].databaseId' 2>/dev/null || true)"
if [ -z "${tid}" ]; then
  echo "push-review: no test run found for ${sha}" >&2
  exit 1
fi
if ! gh run watch "${tid}" --exit-status >/dev/null 2>&1; then
  echo "push-review: test run ${tid} did not conclude success for ${sha}" >&2
  exit 1
fi
test_url="$(gh run view "${tid}" --json url -q .url 2>/dev/null || echo '')"

# 4. release published the tag for this version.
if ! gh release view "v${ver}" >/dev/null 2>&1; then
  echo "push-review: release v${ver} not published (tag/assets missing)" >&2
  exit 1
fi
rel_url="$(gh release view "v${ver}" --json url -q .url 2>/dev/null || echo '')"
echo "push-review: v${ver} released; test ${tid} success"

# 5. emit the PR-style summary document into the OKF sub-bundle (kept out of the
#    flat root documents index; the indexer surfaces the sub-bundle as one entry).
out_dir=".satelle/documents/story-implementation-summary"
mkdir -p "${out_dir}"
out="${out_dir}/commit-summary-${sty:-${sha}}.md"
{
  echo "---"
  echo "type: story-implementation-summary"
  echo "title: Push summary — ${sty:-${sha}}"
  echo "description: v${ver} — ${subject}"
  echo "timestamp: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
  echo "---"
  echo
  echo "# Push summary — ${sty:-${sha}}"
  echo
  echo "- **Commit:** \`${sha}\`"
  echo "- **Subject:** ${subject}"
  echo "- **Version:** v${ver}"
  echo "- **test:** ${test_url:-n/a} (success)"
  echo "- **release:** ${rel_url:-n/a} (v${ver} published)"
  echo
  echo "## Acceptance criteria"
  echo
  if [ -n "${sty}" ]; then
    satelle story get "${sty}" 2>/dev/null \
      | python3 -c 'import sys,json; print(json.load(sys.stdin).get("acceptance_criteria","") or "_none recorded_")' 2>/dev/null \
      || echo "_acceptance criteria not found for ${sty}_"
  else
    echo "_acceptance criteria not found for this commit_"
  fi
  echo
  echo "## Files changed"
  echo
  echo '```'
  git show --stat --format= HEAD
  echo '```'
} > "${out}"
echo "push-review: wrote ${out}"
exit 0
```
