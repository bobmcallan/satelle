# Changelog

All notable changes to satelle are documented here. Format: Keep a Changelog–style,
newest release first. Each release is a level-2 `## [X.Y.Z] - DATE` header.

**Breaking marker:** a non-empty `### Breaking` subsection under a version means that
version is breaking — the single marker require-init and post-upgrade heal key on.
Agents retrieve deltas with `satelle changelog [--from X] [--to Y]` (no git history).

## [0.0.238] - 2026-07-14
### Fixed
- Remove story-id from init_analysis advice path so repo-agnostic unit check stays green (heal-path release)

## [0.0.237] - 2026-07-14
### Fixed
- gofmt drift_test.go so CI gofmt job passes (sty_a9ec33e7)

## [0.0.236] - 2026-07-14
### Fixed
- Harden init restamp heal integration assertions (restamped report + deployed.version required on release binaries) (sty_a9ec33e7)

## [0.0.235] - 2026-07-14
### Fixed
- Complete sty_a9ec33e7 heal path release with restore-exemption integration coverage

## [0.0.234] - 2026-07-14
### Fixed
- Integration proof that one `satelle init` heals unstamped-identical embedded files (sty_a9ec33e7)

## [0.0.233] - 2026-07-14
### Fixed
- `satelle init` re-stamps stampless embedded-owned files whose body is byte-identical to the current default (breaks pre-stamp repo deadlock); `restore` is exempt from the deployed.version stamp gate so heal commands can run (sty_a9ec33e7)

## [0.0.232] - 2026-07-14
### Fixed
- Rebase best-effort pushes the pre-wipe backup tree to the personal hosted documents partition when configured (sty_873a5380)

## [0.0.231] - 2026-07-14
### Fixed
- Init materializers thread backup policy (ResolveHostedServer + Notice) so pre-mutation backups honor hosted/advisory on converge; scaffold documents `[backup] local_only` (sty_873a5380)

## [0.0.230] - 2026-07-14
### Added
- Pre-mutation substrate backup helper: local floor under `.satelle/backups/`, optional personal hosted push, and `[backup] local_only` to suppress the online advisory (sty_873a5380)
- Converge-overwrite and restore now back up existing files before rewrite; rebase documents the same backups/ policy (sty_873a5380)

## [0.0.229] - 2026-07-14
### Fixed
- Project-workflow integration assertion expects in-loop `in_progress` (`agent=executor`) after sty_db003275 revert (sty_5faf46f1)

## [0.0.228] - 2026-07-14
### Fixed
- Multi-project `serve` hub supervises children for life: log exit to stderr, tear down the dead proxy, respawn with capped backoff, and park after consecutive fast failures — no more silent permanent 502s while the landing shows healthy (sty_5faf46f1)
- Unhealthy child boot no longer registers a live reverse-proxy route; failed projects appear as errored landing rows (sty_5faf46f1)

### Changed
- Project workflow `in_progress` is in-loop (`agent=executor`) again; the `[coder]` binding remains dormant (sty_db003275)

## [0.0.227] - 2026-07-14
### Fixed
- `satelle-substrate-only-check` accepts the binary's managed footprint outside `.satelle/` — the root `.gitignore` and the init-deployed harness hook scaffolds under `.claude/` and `.grok/` — so an init-footprint story closes on the substrate lane instead of being forced onto the full project/code workflow; `[gate] edit_exempt_paths` stays the repo-side extension knob and product code (`.go`, `cmd/`, Makefile, CI) is still rejected naming offenders (sty_40973fb6)

## [0.0.226] - 2026-07-14
### Added
- Project workflow dispatches `in_progress` to an isolated Grok `[coder]` binding with plan-consumption evidence (sty_565a0202)
- `grantsSatelleCLI` accepts grok `read_file` as a disk context channel when headless shell is unavailable (sty_565a0202)

### Changed
- Code skill rewritten for the dispatched coder (plan pull first + PLAN-CONSUMED evidence) (sty_565a0202)

## [0.0.225] - 2026-07-13
### Fixed
- Detect deployed harness scaffold drift vs binary-canonical wrappers; warn on SessionStart/status and fail-closed store verbs until `satelle init` heals (sty_ac25b787)

## [0.0.224] - 2026-07-13
### Fixed
- PreToolUse fail-visible wrappers deploy as `.satelle/hooks/*.sh` with $-free harness commands so Grok no longer skips the edit/commit gates (sty_adfb9862)

## [0.0.223] - 2026-07-13
### Fixed
- Integration suite guards the full host production surface (SATELLE_HOME tree, ~/.config/satelle, installed binary hash/mtime), not only config.toml bytes (sty_6d824d6a)

## [0.0.222] - 2026-07-13
### Fixed
- Integration suite hard-isolates `SATELLE_HOME` (TestMain backstop + per-test `run()` env) so tests never write the host workspace registry (sty_ee7f40c6)

## [0.0.221] - 2026-07-13
### Fixed
- `satelle changelog` and require-init prefer the CHANGELOG **embedded in the binary** — disk is build input only, so downstream repos never get empty false negatives (sty_b5fa838a)
- Integration tests use free listen ports for multi-serve and browser hermetic servers (sty_b5fa838a)

## [0.0.220] - 2026-07-13
### Fixed
- Browser integration tests allocate a free port in `serveRepo` so a leftover satelle process cannot answer healthz for the wrong repo (sty_23aae116)

## [0.0.219] - 2026-07-13

### Breaking
- `satelle install` removed — use `satelle init` (collided with `service install`)
- `satelle workspace rm` removed — use `satelle workspace remove`
- `satelle sync config pull` removed — use `satelle sync config deploy`
- agents.toml: `harness=`, `inject_principles=`, and nested `[agents.NAME]` are no longer dual-read at runtime — run `satelle init` (MigrateAgents) to rewrite
- deployed repos behind a binary with breaking changelog entries fail closed until `satelle init` stamps `.satelle/deployed.version`

### Added
- CLI surface audit (`.satelle/documents/cli-surface-audit.md`) and require-init drift gate (sty_23aae116)

## [0.0.218] - 2026-07-13

### Added
- CHANGELOG.md + `satelle changelog` verb; release fails closed without an entry (sty_f52ba0c3)

## [0.0.217] - 2026-07-13

### Added
- init substrate analysis reports placement/residency/tag-axis and config drift (sty_c73f8905)

### Fixed
- orphaned engagement leases no longer open the edit gate or hold the seat (sty_1738f973)
- PreToolUse wrappers fail-visible: infrastructure failure never bare-denies (sty_c75c73ed)

## [0.0.199] - 2026-07-12

### Added
- feat(init): embedded_sha provenance — converge unedited embedded substrate on re-init (sty_304ee454)

## [0.0.198] - 2026-07-12

### Added
- feat(workflow): per-gate model= override for reviewer bindings (sty_19456622)

## [0.0.197] - 2026-07-12

### Fixed
- fix(hooks): emit harness-specific PreToolUse deny JSON (sty_5e4bc568)

## [0.0.196] - 2026-07-12

### Added
- feat(init): register repo in local workspace registry by default (sty_3bdbdc38)

## [0.0.195] - 2026-07-12

### Changed
- docs(init): gitignore + [sync] comments — git source of truth, sync mirror (sty_aa7cd897)

## [0.0.194] - 2026-07-12

### Changed
- docs(init): scaffold satelle.toml documents [sync]/[hosted]/[vars] (sty_8966f18a)

## [0.0.193] - 2026-07-12

### Fixed
- fix(init): agents.toml scaffold declares role= for zero-warning init (sty_5f1d7b2e)

## [0.0.192] - 2026-07-12

### Added
- feat: on_enter_agent one-shot park entry dispatch (sty_5cabe26f)

### Changed
- release: v0.0.192 on_enter_agent park dispatch (sty_5cabe26f)
- test: copy task-run skill in execution lifecycle fixtures (sty_5cabe26f)
- Wire blocked-triage binding; keep park as reviewer (sty_c77a1672)
- Add satelle-story-blocked-triage performing skill (sty_c29009e7)
- Add satelle-recognise-blockage principle; restore .gitignore managed block (sty_c1c4457b)
- Add recognise-blockage principle for process park/triage (sty_c1c4457b)
- docs(substrate): close record for epic:workflow-review-followups (sty_0fdb7188)
- docs(substrate): hard plan-fidelity gate in code-ac-review (sty_ca97c680)

## [0.0.191] - 2026-07-12

### Changed
- docs(substrate): record hybrid agent-model decision (A) (sty_e3687ec4)

### Fixed
- fix(hooks): teach commitgate pre-execution and fused engage+commit (sty_577d292f)

## [0.0.190] - 2026-07-11

### Added
- feat(agent): role-based gate resolution + verdict transparency (sty_e21cbc08)

## [0.0.189] - 2026-07-11

### Added
- feat(agentstep): formalize Invoke over shared buildRequest+runOnce (sty_ba860c8a)

## [0.0.188] - 2026-07-11

### Changed
- docs(agent): role-contract design for epic:agent-invoke-unify (sty_69fd4e20)

## [0.0.187] - 2026-07-11

### Changed
- docs(epic): close epic:workflow-review — children shipped (sty_171f747e)
- config(gate): exempt .claude/ for harness skill authoring (sty_fad29f15)
- docs(skill): clarify format vs binding drift and refresh path (sty_fad29f15)

## [0.0.186] - 2026-07-11

### Added
- feat(workflow): consultative workflow refresh to canonical format (sty_084f4879)

## [0.0.185] - 2026-07-11

### Added
- feat(workflow): deterministic format-drift detection (sty_2c0c8599)

## [0.0.184] - 2026-07-11

### Added
- feat(agent): validate agents.toml + workflow bindings on demand and at engage (sty_93eec36d)

## [0.0.183] - 2026-07-11

### Added
- feat(wfdot): emit canonical node-consistent edge gates (sty_ccf41efa)

## [0.0.182] - 2026-07-11

### Added
- feat(agents): grok/codex presets, harness→command rename, documented preset menu (sty_17cae74b)
- feat(gate): un-bypassable story+workflow enforcement + agents.toml command presets (sty_949e8739)
- feat(update): sudo-free service restart onto the new binary; release dogfood via satelle update (sty_1ac9f095)
- feat(service): persist the supervisor when systemd --user is unreachable (sty_00dadc91)
- feat(sync): blanket [sync] all scope fallthrough (sty_3ba374e7)

### Changed
- docs(release): mechanism-agnostic dogfood restart (sty_dfc73ced)
- chore: gofmt substrate_okf_test (sty_e4902c51)

### Fixed
- fix(agents): keep [reviewer] grant visible — full grok command, not the bare preset (sty_17cae74b)
- fix(service): --system install restarts a running unit to load the new binary (sty_1ac9f095)
- fix(update): use os.Process.Signal, not syscall.Kill (windows cross-build) (sty_1ac9f095)
- fix(hooks): config-only edit-gate exemptions + fix relative-path bypass (sty_8c3d345c)
- fix(hooks): dual Claude+Grok deny reason on edit/commit gates (sty_e4902c51)

## Older releases

See git tags (`git tag --sort=-v:refname`) for history before the window above.
