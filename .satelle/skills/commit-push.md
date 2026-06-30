---
name: commit-push
scope: project
type: skill
tags: [type:skill, type:executor]
description: Executor skill for the commit-push step. The executor stages and commits the slice (conventional message, the story id, no AI attribution), pushes to main, then WATCHES the GitHub Actions `test` run to conclusion AND determines the release outcome — the `release` workflow is version-gated, so a green `test` run is NOT a published binary unless `.version` was bumped. It records the commit SHA, the test run URL/conclusion, and the release outcome (published tag, or "no release — .version unchanged") as evidence for the commit-push reviewer. Project-scope (this repo's trunk-based, two-workflow CI/release process).
---

# Commit-push (executor step)

You are the **executor** in the `commit-push` step of the workflow. The slice is
built and the prior gates have accepted it; your job is to **land it and prove what
actually happened to the release**, then leave the evidence for the gate that
follows. This is an executor rubric, not a reviewer — you DO the work (see the
[[satelle-agent-model]] principle: the executor mutates; the reviewer only judges).

## The pipeline has TWO workflows — don't conflate them

Pushing to `main` triggers the **`test`** workflow (build, vet, gofmt, unit tests,
static build). A green `test` run means **the code is sound** — it does **NOT** mean
a new binary was published. Publishing is a **separate, version-gated** workflow:
**`release`** runs after `test` succeeds but is a **no-op unless `.version` was
bumped** (it cuts the tag `v<satelle.version>` only when that tag does not yet
exist). So a slice can land with `test` green and **no new binary released** — that
is the normal case, and it is the root cause of binary drift if you call a green
`test` run "the release". Report the release outcome honestly.

## What to do

1. **Stage and commit.** Stage the slice's changes and commit with a **conventional
   commit** subject that ends with the story id in parens, e.g.
   `feat(web): add the X view (sty_1234abcd)`. **No AI attribution** — no
   `Co-Authored-By`, no "generated with" trailer (this repo's convention). Verify
   the commit captured every intended file (`git show --stat HEAD`) before pushing
   — a partial commit is a defect.
2. **Push to main.** This repo is **trunk-based** (`git push origin main`) — no
   branch, no PR. The push triggers `test`, and `test` success triggers the
   version-gated `release`.
3. **Watch the `test` run to conclusion.** Resolve the run for the pushed commit
   (`gh run list --commit "$(git rev-parse HEAD)" --workflow test`) and watch it
   (`gh run watch <run-id> --exit-status`). Do not walk away while it is in
   progress. A red run means the slice is not landed — see *When it fails*.
4. **Determine the release outcome.** Decide whether this slice publishes a binary:
   - **`.version` unchanged** (the common case — check `git show --stat HEAD` /
     `git log -1 -p -- .version`): **NO new binary is released.** The change lands in
     `main` source, but the released binary (what `satelle update` serves) is
     unchanged. Record this explicitly — do not claim a deployment happened.
   - **`.version` bumped**: a release IS expected. After `test` goes green, watch
     the **`release`** run (`gh run list --workflow release --limit 3`,
     `gh run watch <release-run-id> --exit-status`) and confirm it published the tag
     and assets (`gh release view "v$(awk '$1=="satelle.version:"{print $2}' .version)"`).
     `satelle update --check` should then report the new version available.
5. **Record the evidence.** Capture, on the story (a ledger note or tag): the commit
   SHA, the **`test`** run URL + conclusion, and the **release outcome** from step 4
   — either `released v<version> (<release-run-url>)` or
   `no release — .version unchanged`. The commit-push reviewer reads exactly this.
6. **Refresh the local service.** This workflow has no deploy gate — so nothing else
   restarts the running `satelle serve`. Once `test` is green, reinstall and restart
   the service from the pushed code so the web UI reflects `main`:
   `make install && satelle service install`. Use `make install`, **not** a bare
   `go build` — the service's `ExecStart` is the PATH binary
   (`~/.local/bin/satelle`, from `os.Executable()` at install time), so a build that
   only updates the repo `./satelle` leaves the service on the OLD binary. `make
   build`/`make install` now bake the version, commit, and a generated timestamp via
   `-ldflags` (sty_27077b11), so afterwards `satelle version` reports the **real**
   version + the pushed commit — confirm it shows the commit you just pushed.

## When it fails

If the `test` run concludes **failure**, the slice is not landed: read the failing
job's logs, fix the cause under this same story, and re-commit/push — do not advance
the step on a red run. If a `.version` bump's **`release`** run fails, the publish
did not happen: surface it (the tag/assets are missing) rather than recording a
success. If a failure is outside the slice (flaky infra), record that in the
evidence and surface it rather than silently retrying.

## Hand-off to the gate

The `commit-push` reviewer ([[satelle-commit-push-review]]) reads your evidence: it
confirms the `test` run for the pushed commit concluded success and that the release
outcome is recorded (published tag, or an explicit "no release expected"). Leave the
commit SHA, the test run URL, the conclusion, and the release outcome where it can
find them. You never enact your own status advance — the reviewer's accept does that
(see [[satelle-agent-model]]).
