# Changelog

All notable changes to satelle are documented here. Format: Keep a Changelog–style,
newest release first. Each release is a level-2 `## [X.Y.Z] - DATE` header.

**Breaking marker:** a non-empty `### Breaking` subsection under a version means that
version is breaking — the single marker require-init and post-upgrade heal key on.
Agents retrieve deltas with `satelle changelog [--from X] [--to Y]` (no git history).

## [0.0.261] - 2026-07-16

### Added
- Home-keyed runtime plane: satelle.db, logs, backups, and stories cache under `~/.satelle/<repo-key>/` (sty_4660bbe1)
- `satelle runtime path` and `satelle runtime migrate` for inspect + explicit legacy migration

### Changed
- Fresh `satelle init` no longer writes runtime files or gitignore entries under the repo for the DB/logs/backups
- Authored substrate stays under `.satelle/`; effective process uses DataDir vs RuntimeDir throughout

## [0.0.260] - 2026-07-15
### Changed
- Rehydrate operator path fully documented (install → login → bind → rehydrate); post-deploy all-local scope note; empty-tree bind+rehydrate happy-path test (sty_2f1538a4)

## [0.0.259] - 2026-07-15
### Added
- Workstate pull: `satelle sync workstate pull` restores stories/executions/ledger from personal hosted into the local store (store-first + view regen); conflict policy fails when both non-empty unless `--force` (sty_45bfcc50)
- Sync-down rehydrate: `satelle sync rehydrate` (alias `sync pull`) runs config deploy → documents pull → workstate pull without pushing; `project bind` creates minimal satelle.toml when absent (sty_2f1538a4)
### Changed
- OpenAPI publishes GET workstate/items and workstate/ledger for pull/rehydrate; workstate wire includes acceptance_criteria, archived, ledger refs/project_id for round-trip fidelity

## [0.0.258] - 2026-07-15
### Changed
- Local DB placement decision: per-repo default path; never multi-repo global blob; agents stay repo-scoped (sty_1eaa15f5)

## [0.0.257] - 2026-07-15
### Changed
- Workspace continuity posture: local disk + init gitignore defaults; personal sync is backup/rehydrate — supersedes git-as-SoT guidance (sty_c5c54f02)

## [0.0.256] - 2026-07-14
### Changed
- Documents pull skips excludedLocal paths before content fetch (no wasted download for backups/ poison); skip visibility hard-asserted on poisoned-partition sync (sty_0fd04503)

## [0.0.255] - 2026-07-14
### Fixed
- Documents pull skips excludedLocal paths (e.g. backups/) and advances the cursor so poisoned partitions unwedge; skipped paths are reported (sty_84f14ace)
- Hosted pre-mutation backup into the documents partition is opt-in via `[backup] hosted = true` (default off) so init no longer wedges sync (sty_84f14ace)

## [0.0.254] - 2026-07-14
### Added
- First surface-scoped consumer: `satelle-design-review` gate on project workflow (`on=integration`, `applies_to=surface:ui`) using this repo's app.css as design authority (sty_e4359efe)

## [0.0.253] - 2026-07-14
### Added
- Decision: v1 trusts surface: tags (accepted risk) with scoped-gate-skipped telemetry when applies_to filters a gate (sty_dcce86d5)

## [0.0.252] - 2026-07-14
### Added
- Additive executor augmentation: edge-less `agent=executor` nodes with `on=` + `applies_to` compose surface-scoped rubrics onto spine steps without forking the workflow graph (sty_8225d8a5)

## [0.0.251] - 2026-07-14
### Added
- Step-level `applies_to` on edge-less scoped reviewer nodes: gate is enqueued only when the story holds a matching tag (EqualFold ANY-match); unknown DOT attributes and mis-placed applies_to fail closed (sty_c6d093c8)

## [0.0.250] - 2026-07-14
### Fixed
- Repair CHANGELOG structure after 0.0.249 entry was inserted into intro prose (sty_034d843c)

## [0.0.249] - 2026-07-14
### Added
- Controlled tag vocabulary via `[tags.vocabulary]` in satelle.toml: namespaces like `surface` reject unknown values at create/set (case-insensitive match, stored in declared casing); multi-surface via repeated keys (`surface:ui` + `surface:cli`) (sty_034d843c)

## [0.0.248] - 2026-07-14
### Added
- Bare `satelle sync` runs the configured sync — config push, documents push+pull, and work-state push across every opted-in area, self-gating on each `[sync]` scope; all-local repos print one "nothing to sync" line. Per-kind subcommands and `sync config deploy` are unchanged/excluded (sty_cfcc3bb8)

## [0.0.247] - 2026-07-14
### Fixed
- Actually ship matching embedded CHANGELOG for 0.0.246 tag-filter release (sty_f7115cd2)

## [0.0.246] - 2026-07-14
### Fixed
- Sync embedded CHANGELOG with root for tag-filter release (sty_f7115cd2)

## [0.0.245] - 2026-07-14
### Added
- `story list` / `task list` support `--tag` for exact tag filter (ANY-match on multi-value namespaces); document repeated-key multi-value as canonical (sty_f7115cd2)

## [0.0.244] - 2026-07-14
### Fixed
- Close-out release for additive story/task tag mutation already shipped in 0.0.241 (sty_033d4611)

## [0.0.243] - 2026-07-14
### Fixed
- init incomplete-hook WARN path tested for unparseable settings; document harness exit-2 enforcement for reinforcement hooks (sty_0699637c)

## [0.0.242] - 2026-07-14
### Fixed
- Strengthen multi-serve dead-root filter test: childRoots + assignSlug never see missing/non-.satelle registry paths (sty_7a8d5d44)

## [0.0.241] - 2026-07-14
### Fixed
- init auto-heals inert principle `scope:` and `principles:always`→`principles:session` before restamp so old repos can stamp and serve (sty_cc8ce91c)
- multi-serve skips registry roots whose path is missing or has no `.satelle` (sty_7a8d5d44)
- init reinforces missing SessionStart/PreToolUse on existing harness hook files and WARNs if the set stays incomplete (sty_0699637c)
### Added
- `story set` / `task set` support `--add-tags` and `--remove-tags` (incl. namespace group remove like `sprint:*`) without clobbering the rest (sty_033d4611)

## [0.0.240] - 2026-07-14
### Added
- Progress strip shows a temporary step-0 pulsing "starting" light while a live pre-transition engagement seat is held and no ledger transitions have landed yet (sty_e1314fe3)

## [0.0.239] - 2026-07-14
### Fixed
- Heal-path release commit subject names the story id for release-gate convention (sty_a9ec33e7)

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
