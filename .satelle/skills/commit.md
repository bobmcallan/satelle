---
name: commit
scope: project
type: skill
tags: [type:skill, type:executor]
description: Executor skill for the `commit` step. Stages the slice, bumps the canonical version and stamps the build date in .version (MANDATORY on every commit — .version is the single source the release tag and the build identity derive from), then makes a conventional commit ending in the story id with NO AI attribution. It does NOT push — the following `push` step pushes and watches CI. Project-scope (this repo's trunk-based release: a commit bumps the version so the push cuts a release).
---

# Commit (executor step)

You are the **executor** in the `commit` step. The slice is built and the prior
gates have accepted it; your job is to **stage it, bump the version, and commit** —
then leave the push to the next step. You DO the work (see [[satelle-agent-model]]:
the executor mutates; the reviewer only judges). You do **not** push here.

## Why the bump is mandatory

`.version` is the **single source of truth** for both the release tag (`v<satelle.version>`)
and the build identity baked into the binary. The `release` workflow cuts a tag
**only when `.version` changed**, so a commit that does not bump leaves the released
binary (what `satelle update` serves) stale — the binary-drift trap. Therefore
**every** commit on this step bumps the patch version and stamps the build date.

## What to do

1. **Stage and format.** Format Go and stage everything:
   ```bash
   gofmt -s -w internal/ cmd/ 2>/dev/null; git add -A
   ```
   Confirm the staged set is the slice you intend (`git diff --cached --stat`).
2. **Bump `.version`** — MANDATORY. `.version` carries one canonical version plus a
   build date:
   ```
   satelle.version: <x.y.z>
   satelle.build:   <UTC>
   ```
   - Increment the **patch** of `satelle.version` (`0.0.11` → `0.0.12`).
   - Set `satelle.build` to `date -u +"%Y-%m-%d-%H-%M-%S"`.
   - `git add .version`.
   The release tag will be `v<satelle.version>`; a missed bump means no tag is cut.
3. **Commit.** A **conventional commit** subject ending with the story id in parens,
   e.g. `feat(web): add the X view (sty_1234abcd)`. **No AI attribution** — no
   `Co-Authored-By`, no "generated with" trailer (this repo's convention). Verify the
   commit captured every intended file (`git show --stat HEAD`) — a partial commit is
   a defect.
4. **Do NOT push.** The `push` step pushes to `main` and watches CI. Leave `HEAD`
   committed locally with the bumped `.version`.

## Hand-off to the next step

The `push` step pushes `HEAD`, watches the `test` run, then the version-gated
`release` run, and confirms the tag/assets. The `satelle-push-review` gate then
verifies the bump, the green CI, and the published release. You never enact your own
status advance — the workflow's gates do that (see [[satelle-agent-model]]).
