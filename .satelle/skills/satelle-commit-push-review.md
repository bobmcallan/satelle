---
name: satelle-commit-push-review
scope: project
type: skill
tags: [kind:skill, type:reviewer, type:functional-check]
description: Functional-check gate for the commit-push step. Verifies the pushed commit's GitHub Actions run concluded SUCCESS (evidence the deployment worked) and emits a PR-style commit-summary document (each acceptance criterion, the files changed, the commit SHA, the CI run URL). Deterministic gate — the embedded check IS the verdict; a failed or missing run rejects. Self-contained (no external script), per satelle-reviewer-self-contained.
---

# Commit-push gate (functional check)

This is a **functional-check** gate on the commit-push step. The check is the
embedded ```check script below — **self-contained**, referencing no external file
(see [[satelle-reviewer-self-contained]]). satelle runs it in the repo root on the
gated transition; exit 0 accepts, non-zero rejects with the output tail as notes.

It does two jobs, and both are mechanism, not judgment — so the read-only LLM
reviewer invariant is untouched (this is the deterministic gate path, like the
deploy check): (1) it **verifies the deployment worked** by confirming the GitHub
Actions run for the pushed `HEAD` commit concluded `success`, rejecting on a failed
or missing run; and (2) it **emits a PR-style commit-summary** document under
`.satelle/documents/` noting each acceptance criterion (read from the story the
commit names), the files changed, the commit SHA, and the CI run URL — the reviewer
authoring the summary the operator reads. See [[satelle-actor-model]].

```check
#!/usr/bin/env bash
set -uo pipefail

sha="$(git rev-parse HEAD)"
subject="$(git log -1 --format=%s)"
sty="$(printf '%s' "$subject" | grep -oE 'sty_[0-9a-f]+' | head -1 || true)"

# 1. Verify the GitHub Actions run for the pushed commit concluded success.
if ! command -v gh >/dev/null 2>&1; then
  echo "commit-push: gh CLI unavailable — cannot verify the deployment run for ${sha}" >&2
  exit 1
fi
run_id="$(gh run list --commit "${sha}" --limit 1 --json databaseId -q '.[0].databaseId' 2>/dev/null || true)"
if [ -z "${run_id}" ]; then
  echo "commit-push: no GitHub Actions run found for commit ${sha}" >&2
  exit 1
fi
if ! gh run watch "${run_id}" --exit-status >/dev/null 2>&1; then
  echo "commit-push: CI run ${run_id} did not conclude success for ${sha}" >&2
  gh run view "${run_id}" 2>/dev/null | tail -20 >&2 || true
  exit 1
fi
run_url="$(gh run view "${run_id}" --json url -q .url 2>/dev/null || echo '')"
echo "commit-push: CI run ${run_id} concluded success (${run_url})"

# 2. Emit the PR-style commit-summary document.
out_dir=".satelle/documents"
mkdir -p "${out_dir}"
out="${out_dir}/commit-summary-${sty:-${sha}}.md"
{
  echo "# Commit summary — ${sty:-${sha}}"
  echo
  echo "- **Commit:** \`${sha}\`"
  echo "- **Subject:** ${subject}"
  echo "- **CI run:** ${run_url:-n/a} (success)"
  echo
  echo "## Acceptance criteria"
  echo
  if [ -n "${sty}" ]; then
    # The database is the sole story store (no on-disk mirror) — read ACs from it.
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
echo "commit-push: wrote ${out}"
exit 0
```
