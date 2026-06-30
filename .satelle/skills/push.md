---
name: push
scope: project
type: skill
tags: [type:skill, type:executor]
description: Executor skill for the `push` step. Pushes the committed slice to main, watches the GitHub Actions `test` run to conclusion, then — because the prior `commit` step bumped .version — watches the version-gated `release` run for the same SHA and confirms it published the tag + assets. Refreshes the local service. Records the SHA, the test + release run URLs/conclusions, and the published tag as evidence for the push reviewer. No auto-retry on failure — surface and stop. Project-scope (trunk-based: push to main IS the release).
---

# Push (executor step)

You are the **executor** in the `push` step. The `commit` step has committed the
slice with a bumped `.version`; your job is to **push it and prove the release**,
then leave the evidence for the `satelle-push-review` gate. You DO the work (see
[[satelle-agent-model]]).

## The pipeline has TWO workflows

Pushing to `main` triggers **`test`** (build, vet, gofmt, unit tests, static build).
On `test` success the **`release`** workflow runs — and because `commit` bumped
`.version`, `release` is NOT a no-op this time: it cuts the tag `v<satelle.version>`
and publishes the assets. There is **no deploy workflow** — the push to main IS the
release. Watch BOTH runs; do not stop after `test`.

## What to do

1. **Push to main.** Trunk-based (`git push origin main`) — no branch, no PR. Capture
   the pushed SHA: `SHA=$(git rev-parse HEAD)`.
2. **Watch `test` to conclusion.** Resolve the run for the SHA and watch it:
   ```bash
   TID=$(gh run list --commit "$SHA" --workflow test --limit 1 --json databaseId -q '.[0].databaseId')
   gh run watch "$TID" --exit-status
   ```
   A red run means the slice is not landed — see *When it fails*.
3. **Watch the version-gated `release` run.** It is `workflow_run`-triggered, so it
   spawns after `test` finishes — poll briefly for it, then watch it:
   ```bash
   for i in $(seq 1 20); do
     RID=$(gh run list --commit "$SHA" --workflow release --limit 1 --json databaseId -q '.[0].databaseId')
     [ -n "$RID" ] && break; sleep 3
   done
   gh run watch "$RID" --exit-status
   ```
   Confirm it published the tag + assets:
   ```bash
   gh release view "v$(awk '$1=="satelle.version:"{print $2}' .version)"
   ```
   `satelle update --check` should then report the new version available.
4. **Refresh the local service.** No deploy gate restarts the running `satelle serve`,
   so reinstall from the pushed code: `make install && satelle service install`.
   Use `make install` (not a bare `go build`) so the service's PATH binary is updated;
   confirm `satelle version` reports the pushed commit + the new version.
5. **Record the evidence.** Capture on the story (a ledger note or tag): the SHA, the
   `test` run URL + conclusion, the `release` run URL + conclusion, and the published
   `v<version>` tag. The `satelle-push-review` gate reads exactly this.

## When it fails

If `test` concludes failure, the slice is not landed: read the failing job
(`gh run view "$TID" --log-failed`), fix under this same story, and re-run the
`commit`/`push` steps — do not advance on a red run. If the `release` run fails, the
publish did not happen: surface it (the tag/assets are missing) rather than recording
success. **Do not auto-retry, amend, or force-push** — surface the failure and stop.

## Hand-off to the gate

The `satelle-push-review` gate ([[satelle-agent-model]]) reads your evidence: it
confirms `.version` was bumped + dated, the `test` run for the SHA concluded success,
the `release` published the tag/assets, and the commit follows conventions. You never
enact your own status advance — the gate's accept does that.
