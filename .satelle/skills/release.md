---
name: release
scope: project
type: skill
tags: [type:skill]
description: In-loop executor skill for the merged `release` step (sty_d9a0b573). The driving session — NOT an isolated sub-process — stages the story's slice, bumps .version + stamps the build date, makes a conventional commit ending in the story id with NO AI attribution, pushes to main, watches the `test` run and the version-gated `release` run to conclusion, confirms the published tag, refreshes the local service, and records a PR-style summary as a story attachment. Merges the former commit + push + record-release steps into one in-loop step so no isolated agent is spawned and no context is lost. The `satelle-story-release-review` gate then judges the recorded evidence.
---

# Release (in-loop executor step)

You are the **executor** in the merged `release` step, running **in-loop as the
driving session** — not an isolated sub-process. The prior gates accepted the
slice (acceptance criteria met, tests exercise the change, `make integration`
green). Your job is to **commit the slice with a version bump, push it, prove the
release, and record the evidence** — all in one step. You DO the work (see
[[satelle-agent-model]]: the executor mutates; the reviewer only judges). You
never enact your own status advance — the `release → done` gate does that.

## 1. Stage and commit (bump is mandatory)

1. **Format, then stage the STORY'S SLICE** — only the files this story changed
   (read the story body/acceptance criteria and `git status --short`):
   ```bash
   gofmt -s -w internal/ cmd/ tests/ 2>/dev/null; git add <the story's files>
   ```
   Do NOT `git add -A`: the tree may carry another session's in-flight changes.
   Confirm the staged set is exactly the slice (`git diff --cached --stat`).
2. **Bump `.version`** — MANDATORY. It is the single source of truth for the
   release tag (`v<satelle.version>`) and the baked build identity; `release` cuts
   a tag ONLY when `.version` changed, so a missed bump strands the released binary.
   - Increment the **patch** of `satelle.version` (`0.0.11` → `0.0.12`).
   - Set `satelle.build` to `date -u +"%Y-%m-%d-%H-%M-%S"`. `git add .version`.
3. **Commit.** A conventional-commit subject ending with the story id in parens,
   e.g. `feat(web): add the X view (sty_1234abcd)`. **No AI attribution** — no
   `Co-Authored-By`, no "generated with" trailer (this repo's convention). Verify
   the commit captured the intended files (`git show --stat HEAD`).

## 2. Push and prove the release

Pushing to `main` triggers **`test`** (build, vet, gofmt, unit tests). On its
success the version-gated **`release`** workflow cuts the tag and publishes assets.
There is no deploy workflow — the push to main IS the release. Watch BOTH.

```bash
git push origin main
SHA=$(git rev-parse HEAD)
TID=$(gh run list --commit "$SHA" --workflow test --limit 1 --json databaseId -q '.[0].databaseId')
gh run watch "$TID" --exit-status
for i in $(seq 1 20); do RID=$(gh run list --commit "$SHA" --workflow release --limit 1 --json databaseId -q '.[0].databaseId'); [ -n "$RID" ] && break; sleep 3; done
gh run watch "$RID" --exit-status
gh release view "v$(awk '$1=="satelle.version:"{print $2}' .version)"
```

**Do not auto-retry, amend, or force-push.** If `test` fails the slice is not
landed — read `gh run view "$TID" --log-failed`, fix under this same story, and
re-run the release step. If `release` fails, the publish did not happen — surface
it and stop; never record success over a red run.

Then **refresh the local service** from the pushed code so the running binary is
current: `make install && satelle service install`; confirm `satelle version`
reports the pushed commit + new version.

## 3. Record the summary WITH the story

Write a short PR-style summary (what shipped, why, the SHA, the `test` + `release`
run URLs/conclusions, the published tag) and attach it to the story — the evidence
the release gate judges:

```bash
satelle story attach <sty_id> --name "release-summary-<sty_id>" \
  --type story-implementation-summary --body "…"
```

The `satelle-story-release-review` gate then judges this recorded evidence and the
story's acceptance criteria. See [[satelle-agent-model]], [[satelle-done-is-last]].
