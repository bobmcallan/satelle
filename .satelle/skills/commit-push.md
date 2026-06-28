---
name: commit-push
scope: project
kind: skill
tags: [kind:skill, type:executor]
description: Executor skill for the commit-push step. The executor stages and commits the slice (conventional message, the story id, no AI attribution), pushes to main, then WATCHES the GitHub Actions run for the pushed commit until it concludes — recording the conclusion and the run URL as evidence so the commit-push reviewer can see the deployment worked. Project-scope (this repo's trunk-based release + CI process).
---

# Commit-push (executor step)

You are the **executor** in the `commit-push` step of the workflow. The slice is
built and the prior gates have accepted it; your job is to **land it and prove the
release succeeded**, then leave the evidence for the gate that follows. This is an
executor rubric, not a reviewer — you DO the work (see the
[[satelle-recursive-actor-model]] principle: the executor mutates; the reviewer
only judges).

## What to do

1. **Stage and commit.** Stage the slice's changes and commit with a **conventional
   commit** subject that ends with the story id in parens, e.g.
   `feat(web): add the X view (sty_1234abcd)`. **No AI attribution** — no
   `Co-Authored-By`, no "generated with" trailer (this repo's convention). Verify
   the commit captured every intended file (`git show --stat HEAD`) before pushing
   — a partial commit is a defect.
2. **Push to main.** This repo is **trunk-based**: pushing to `main` IS the release
   (`git push origin main`). Do not open a branch or PR.
3. **Watch the CI run.** The push triggers the GitHub Actions workflow — that run
   IS the deployment. Watch it to completion with `gh run watch` (resolve the run
   for the pushed commit, e.g. `gh run list --commit "$(git rev-parse HEAD)"` then
   `gh run watch <run-id> --exit-status`). Do not walk away while it is in progress.
4. **Record the evidence.** Capture the run's **conclusion** (success / failure)
   and its **URL** (`gh run view <run-id> --json conclusion,url`) and record it on
   the story as evidence — a ledger note or a story tag — so the next gate can read
   it without re-deriving it. The commit SHA, the run URL, and the conclusion are
   the proof the deployment worked.

## When it fails

If the CI run concludes **failure**, the release is not done: read the failing
job's logs, fix the cause under this same story, and re-commit/push — do not
advance the step on a red run. If the failure is outside the slice (flaky
infra), record that in the evidence and surface it rather than silently retrying.

## Hand-off to the gate

The `commit-push` reviewer ([[satelle-commit-push-review]]) reads your evidence:
it confirms the CI run for the pushed commit concluded success and summarises the
slice. Leave the commit SHA, the run URL, and the success conclusion where it can
find them. You never enact your own status advance — the reviewer's accept does
that (see [[satelle-recursive-actor-model]]).
