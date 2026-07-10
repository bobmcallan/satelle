---
name: release
scope: project
type: skill
tags: [type:skill]
description: In-loop executor skill for the merged `release` step. Stages the story's slice, bumps .version + stamps the build date, commits (no AI attribution), pushes to main, LOCALLY INSTALLS the new binary and restarts the web service under a persistent supervisor (true dogfood — the running service must report the new version; the restart MECHANISM is not the test, so an unreachable systemd --user bus does not fail a good binary, but an ephemeral relaunch is not acceptable), records the `test` + version-gated `release` run URLs with real conclusions and the published tag — one consolidated check, NOT `gh run watch` (sty_bfb2b392) — and attaches a PR-style summary. The `satelle-story-release-review` gate is the authority on CI-green and recorded local-install evidence.
---

# Release (in-loop executor step)

You are the **executor** for the merged `release` step (in-loop on the driving session when the workflow assigns `agent=executor`). Prior gates accepted the slice. **Commit with a version bump, push, install locally, prove the release, and record evidence.** You never self-enact `release → done` — the gate does that.

**Local install is part of the release, not optional cleanup.** Dogfood means the machine you released from runs the new binary as the live web service. The pass criterion is mechanism-AGNOSTIC: `make install` succeeds AND the **running** web service reports the new version — reached through a **persistent supervisor** (a system unit, or a linger-backed user manager that survives session loss), never an ephemeral `nohup`/`setsid` relaunch that dies with your shell. `satelle service install` is the preferred path, but it is a *means*, not the test: if its systemd `--user` bus is unreachable (a headless / non-login WSL agent session — no `/run/user/<uid>/systemd`) yet `make install` succeeded and the live version matches under a persistent supervisor, that is **not** a failed release — do not fail a good binary over the restart mechanism. The release HAS failed only when the binary did not install, or the running service still reports the OLD version (or runs under no persistent supervisor). Fix the persistence under this story and re-verify; do not attach a success summary or proceed to done until the live version matches persistently.

## 1. Stage and commit (bump is mandatory)

1. **Format, then stage the STORY'S SLICE** — only the files this story changed (read the story body/acceptance criteria and `git status --short`):
   ```bash
   gofmt -s -w internal/ cmd/ tests/ 2>/dev/null; git add <the story's files>
   ```
   Do NOT `git add -A`: the tree may carry another session's in-flight changes. Confirm the staged set is exactly the slice (`git diff --cached --stat`).
2. **Bump `.version`** — MANDATORY. Single source of truth for the release tag (`v<satelle.version>`) and the baked build identity; `release` cuts a tag ONLY when `.version` changed, so a missed bump strands the released binary.
   - Increment the **patch** of `satelle.version` (`0.0.11` → `0.0.12`).
   - Set `satelle.build` to `date -u +"%Y-%m-%d-%H-%M-%S"`. `git add .version`.
3. **Commit.** A conventional-commit subject ending with the story id in parens, e.g. `feat(web): add the X view (sty_1234abcd)`. **No AI attribution** — no `Co-Authored-By`, no "generated with" trailer (this repo's convention). Verify the commit captured the intended files (`git show --stat HEAD`).

## 2. Push, then install locally (dogfood), then capture CI evidence

Pushing to `main` triggers **`test`**. On success, the version-gated **`release`** workflow cuts tag `v<satelle.version>` and publishes assets. Push to main IS the publish path. **Do not `gh run watch`** — RECORD conclusions (sty_bfb2b392).

```bash
git push origin main
SHA=$(git rev-parse HEAD)
for i in $(seq 1 10); do TID=$(gh run list --commit "$SHA" --workflow test --limit 1 --json databaseId -q '.[0].databaseId'); [ -n "$TID" ] && break; sleep 3; done
```

### 2a. Local install — mandatory (during the CI window)

While CI runs, install the new binary and restart the **local** dogfood service from the pushed HEAD so a **persistent** supervisor runs it:

```bash
make install            # MANDATORY — must exit zero (this is the binary)
satelle service install # preferred restart path (systemd --user)
```

Then **verify** the live stack matches the release (CLI alone is not enough — a replaced binary can leave a stale serve process):

```bash
VER=$(awk '$1=="satelle.version:"{print $2}' .version)
# CLI binary on PATH
satelle version   # must report $VER and the pushed commit SHA prefix
# Live web service (footer is baked into the running process)
curl -fsS "http://127.0.0.1:${PORT:-8787}/" | grep -F "satelle $VER"
```

The **running service reporting `$VER`** is the pass criterion — not any particular restart command:

- `make install` non-zero → **release failed** (the binary itself). Fix and re-run.
- `satelle service install` non-zero because the systemd `--user` bus is unreachable (headless / non-login WSL — no `/run/user/<uid>/systemd`): this alone is **not** a failed release. Restart via a **persistent** fallback instead, then re-verify the live version:
  - a linger-backed user manager — `loginctl enable-linger $USER` then start the user unit once a user bus exists; or
  - a **system** unit that runs regardless of login — `sudo cp ~/.config/systemd/user/satelle.service /etc/systemd/system/ && sudo systemctl enable --now satelle.service`.
  - An ephemeral `nohup`/`setsid satelle serve …` relaunch is **NOT** an acceptable final state — it dies with the session and the fleet silently reverts. Use it only as a throwaway probe, never as the released supervisor.
- CLI version or the footer still shows the **previous** version → **release failed** (stale process, or a non-persistent relaunch). Make a persistent supervisor run the new binary; re-verify.
- Do **not** treat "printed guidance" or a manual note as success — the dogfood is done only when the live version matches AND is served by a persistent supervisor.

Draft the summary body (§3) while CI proceeds only after local install verification passes.

### 2b. CI + published tag (one consolidated look)

```bash
for i in $(seq 1 8); do
  gh run list --commit "$SHA" --json name,status,conclusion,url
  gh release view "v$(awk '$1=="satelle.version:"{print $2}' .version)" 2>/dev/null && break
  sleep 15
done
```

Record what you actually SEE — `test` and `release` run URLs with **real conclusions** and the published tag. **Do not auto-retry, amend, or force-push, and never record success over a red or unconcluded run.** If `test` failed, fix under this story and re-run release. If `release` failed or the tag is missing, surface it and stop.

## 3. Record the summary WITH the story

Write a short PR-style summary and attach it — evidence the release gate judges. **Must include local install** (CLI version line + footer/service check that matched `$VER`):

```bash
satelle story attach <sty_id> --name "release-summary-<sty_id>" \
  --type story-implementation-summary --body "…"
```

Summary must cover: what shipped, SHA, version/tag, test + release URLs/conclusions, **local install verified** (`satelle version` + live footer/service at the new version).

The `satelle-story-release-review` gate judges this recorded evidence and the ACs. See [[satelle-agent-model]], [[satelle-done-is-last]].
