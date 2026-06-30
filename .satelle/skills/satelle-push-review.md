---
name: satelle-push-review
scope: project
type: skill
tags: [type:skill, type:reviewer, type:functional-check]
description: Functional-check gate for the `push` step. Confirms the pushed HEAD bumped satelle.version + stamped satelle.build, the commit carries no AI attribution, the GitHub Actions `test` run for HEAD concluded success, and the version-gated `release` published the tag v<version>. Emits a PR-style summary document. Deterministic — the embedded check IS the verdict; a missing bump, a red/absent run, or an unpublished release rejects. Self-contained (no external script), per satelle-reviewer-self-contained. Replaces satelle-commit-push-review.
---

# Push gate (functional check)

This is a **functional-check** gate on the `push` step. The check is the embedded
```check script below — **self-contained**, referencing no external file (see
[[satelle-reviewer-self-contained]]). satelle runs it in the repo root on the gated
transition; exit 0 accepts, non-zero rejects with the output tail as notes.

It verifies, deterministically (mechanism, not judgment — the read-only reviewer
invariant holds): (1) HEAD **bumped** `satelle.version` and **stamped** `satelle.build`
in `.version`; (2) the commit carries **no AI attribution**; (3) the `test` run for
HEAD **concluded success**; (4) the `release` workflow **published** the tag
`v<satelle.version>`. Then it emits a PR-style summary under `.satelle/documents/`.
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

# 2. no AI attribution in the commit message.
if git log -1 --format='%B' | grep -qiE 'co-authored-by|generated with|[^a-z]claude'; then
  echo "push-review: commit carries AI attribution (forbidden in this repo)" >&2
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

# 5. emit the PR-style summary document.
out_dir=".satelle/documents"
mkdir -p "${out_dir}"
out="${out_dir}/commit-summary-${sty:-${sha}}.md"
{
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
