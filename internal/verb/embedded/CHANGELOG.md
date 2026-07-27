## [0.0.336] - 2026-07-27

### Fixed
- Substrate close gate no longer requires a git commit: unions recorded change set, live worktree with opt-in `--include-substrate`, and commits naming the story; rejects empty sets and uncommitted non-substrate paths (sty_6469025e)

### Changed
- `satelle story diff --include-substrate` opt-in mtime leg for authored dirs + data dir; default live path stays git-only for scope-review (sty_6469025e)
- Substrate workflow prose no longer mandates commit+push when `.satelle/` is git-ignored (sty_6469025e)

## [0.0.335] - 2026-07-27

### Added
- Enacted status transitions record a `change_record` ledger row (paths/counts) and optional type:change patch attachment; `satelle story diff --recorded` unions the file lists; substrate-only-check prefers recorded files over git log (sty_948ad5df)

### Changed
- Gate payloads exclude type:change attachments so large patches do not starve plan/step-summary under the docs ceiling (sty_948ad5df)

## [0.0.334] - 2026-07-26

### Changed
- Work-state push is incremental: a per-(server, project, repo) cursor outside the repo sends only records changed since the last successful push; the silent 2,000-item ceiling is gone; story-less ledger rows are included; `--full` re-sends everything (sty_88e83180)
- Config and documents push skip unchanged content via server manifest sha comparison, reporting skipped-without-upload separately from server-side unchanged (sty_88e83180)

## [0.0.333] - 2026-07-26

### Removed
- Embedded `code.md` executor rubric no longer ships: it named lifecycle states and gates the baseline does not define, and claimed the generic word `code` in every repo's skill namespace (sty_01f49dd5)

### Changed
- Dispatched-step contract (fresh stdin payload, pull-by-id, never advance status) lives in `satelle-agent-model`; `satelle-dot-standard` examples mark `@skill:code` as repo-authored; this repo's `.satelle/skills/code.md` matches in-loop `agent=executor` (sty_01f49dd5)
- New embed self-consistency test rejects embedded prose that names non-shipping states/gates as though they ship (sty_01f49dd5)

## [0.0.332] - 2026-07-26

### Changed
- UserPromptSubmit injection replaces the static create-and-engage reminder when a live engagement seat has a forward DOT advance: the engaged form names the story id, status, next gate(s), and `satelle story set` command, all derived from the governing workflow via `wfdot.AdvanceOptions` (terminal/park/back-edge filtered; multi-Msquare cancel sinks do not silence the spine) (sty_e16a2cd7)

## [0.0.331] - 2026-07-26

### Fixed
- Hook handlers (`gate`, `commitgate`, `prompt`) refresh `heartbeat_at` on a live owner-held engagement seat so a long `in_progress` step no longer drops the seat after HeartbeatTTL; stale and foreign-owner leases are never revived, and heartbeat write failures are fail-open (sty_3bb1d8be)

## [0.0.330] - 2026-07-26

### Fixed
- `satelle.toml` joins the config push bundle as the `settings` area so rehydrate can restore `[sync]` scopes; `[hosted] project` is redacted at push and re-applied from the local binding at deploy so another repo cannot be rebound; `satelle.local.toml` remains excluded by the unconditional `.local` rule (sty_ea18294f)

## [0.0.329] - 2026-07-26

### Security
- Unconditional `.local` exclusion on every outbound file bundle: config push, documents push, `satelle publish`, and hosted backup never transmit a path whose name carries a `.local` segment (satelle.local.toml, notes.local.md, …). Applied at bundle assembly so a new sync area cannot bypass it; dry-run and `sync scopes` report withheld paths (sty_698e70b6)

## [0.0.328] - 2026-07-25

### Added
- Controlled story **category** vocabulary: default list ships as embedded data (`internal/config/substrate/config/categories.toml`), not a Go literal; satelle.toml `[categories]` may extend (`extra`) or replace (`vocabulary`) (sty_b2315e17)
- Enforcement phase `off | warn | reject` (default **warn** so upgrades never hard-break); reject fails create/set in verb; warn is a CLI stderr advisory (web/MCP get casing + reject only — no warning channel in this slice) (sty_b2315e17)
- Case-insensitive category ↔ `applies_to` matching in workflow resolution (`containsStr` uses EqualFold) so a legal-but-differently-cased value never silently resolves no workflow (sty_b2315e17)

### Changed
- Synonym collapses in the shipped default: bug/bugfix/defect → `fix`; infra → `infrastructure`; frontend/web/ui/cli are not categories (use `surface:` + a TYPE category) (sty_b2315e17)

## [0.0.328] - 2026-07-25

### Added
- Controlled story **category** vocabulary: default list ships as embedded data (`internal/config/substrate/config/categories.toml`), not a Go literal; satelle.toml `[categories]` may extend (`extra`) or replace (`vocabulary`) (sty_b2315e17)
- Enforcement phase `off | warn | reject` (default **warn** so upgrades never hard-break); reject fails create/set in verb; warn is a CLI stderr advisory (web/MCP get casing + reject only — no warning channel in this slice) (sty_b2315e17)
- Case-insensitive category ↔ `applies_to` matching in workflow resolution (`containsStr` uses EqualFold) so a legal-but-differently-cased value never silently resolves no workflow (sty_b2315e17)

### Changed
- Synonym collapses in the shipped default: bug/bugfix/defect → `fix`; infra → `infrastructure`; frontend/web/ui/cli are not categories (use `surface:` + a TYPE category) (sty_b2315e17)

## [0.0.327] - 2026-07-25

### Added
- Create-path notice when `[review] gate_create` is undefined (pre-seed scaffold): stories/tasks file without create review and the one-line enable fix is named; explicit `gate_create = false` silences; never auto-rewrites config (sty_d4d0ee59)
- `[review] gate_create` joins `scaffoldConfigDefaults` so init/validate advisories cover the same seed as the create notice (sty_d4d0ee59)

## [0.0.326] - 2026-07-25

### Fixed
- Create-review premise example is repo-agnostic (`<path>:<symbol>`) so v0.0.325's leaked satelle-internal illustration does not ship in the binary (sty_b9692e1f)

## [0.0.325] - 2026-07-25

### Changed
- Create gate gains premise falsification: reject story rationales that assert checkable facts about this repo contradicted by named evidence; opinion/value/priority never reject grounds (sty_b9692e1f)

## [0.0.324] - 2026-07-25

### Fixed
- `satelle agent validate` no longer warns that a named `role=reviewer` binding is a perform-only allocation — that contradicted live gate NODE lines after per-gate agent= (sty_6ab016dc)

## [0.0.323] - 2026-07-25

### Fixed
- Named gate bindings (`agent=<name>` on a gated edge) run that binding's harness, not the default `[reviewer]` runner (sty_68dafd5f)

## [0.0.322] - 2026-07-25

### Added
- Controlled `cancel-reason` tag namespace (repo config via `[tags.vocabulary]`) so cancelled stories are queryable by why they closed — shelved, duplicate, superseded, withdrawn, wrong (sty_de8e8e2c)

### Changed
- Cancel gate is category-aware: parent/epic-parent cancels accept only when every child is done or cancelled (mirrors done-review); conditional `cancel-reason` tag expectation when the repo declares the namespace; cancelled is terminal and revival is a new story tagged `supersedes:<id>` (sty_de8e8e2c)

## [0.0.321] - 2026-07-25

### Fixed
- ACP model set_config_option: hard-fail only on explicit model rejection; Method not found is a soft miss so peers without the method still run (sty_a476a2f8 follow-up)

## [0.0.320] - 2026-07-25

### Changed
- Workflows name agents, never models: DOT `model=` is superseded (warn + strip on refresh); gated edges and scoped reviewers allocate any `role=reviewer` binding via `agent=<name>`; ACP surfaces peer rejection of `session/set_config_option` model (sty_a476a2f8)

### Added
- Integration coverage for named gate bindings, role-mismatch refusal, and model= strip (sty_a476a2f8)

## [0.0.319] - 2026-07-25

### Changed
- Optimise embedded substrate for token economy, focus, and repo-agnostic fitness: promote satelle-skill-naming; strip dogfood sty_/tsk_ ids; language-agnostic code.md; description budgets ≤60 for skills/workflows; single-source binding and bundling doctrine; scope:system on embedded defaults; sprint index form is a repo choice (sty_24365c69)
- Downstream: `satelle update` then `satelle restore` to pick up new defaults (seeded copies with a stale embedded_sha are not auto-healed) (sty_24365c69)

## [0.0.318] - 2026-07-25

### Added
- Property-based conformance suite over `config.EmbeddedDefaults()` (Tier 1): structure/OKF, scope manifest, legal tags, description ceilings, dogfood-id ratchet, banned stories paths, reviewer accept/reject + verdict blocks (sty_6830e78e)
- Embedded wikilink + workflow `@skill:` resolution via wfdot, with justified allow-lists and negative fixtures (sty_6830e78e)
- Generalised ```check fence extractor `structure.CheckFence` and integration golden tables for every coded-check skill (sty_6830e78e)
- Gate-wiring integration tests (reviewer stub accept/reject, status-unchanged) and discovery/waiver map for embedded workflow gates (sty_6830e78e)
- Opt-in LLM judgment suite under `tests/llm/` (`//go:build llm`, `make judgment`) with labelled fixtures and best-of-three rule (sty_6830e78e)

### Changed
- Retired brittle substring pins subsumed by Tiers 1–3 from `substrate_okf_test`, `embed_principle_test`, `principle_consistency_test`; deleted `substrate_path_test.go`; survivors comment the non-tabular property (sty_6830e78e)
- README Testing table documents judgment cost and hermetic default path (sty_6830e78e)

## [0.0.317] - 2026-07-22

### Fixed
- Stories tab backlog and engaged chips share one chip style and baseline; engaged label is number-first ("N engaged") to match backlog (sty_c7ab5180)
- TestRepoReviewerModelIsActive skips when `.satelle/agents.toml` is absent so CI is not red after operator-owned `.satelle/` (sty_c7ab5180, follow-up to sty_91a390a0)

## [serve-v0.0.10] - 2026-07-22

### Fixed
- Same backlog/engaged chip alignment and number-first labels as CLI 0.0.317 (sty_c7ab5180)

## [0.0.316] - 2026-07-22

### Fixed
- Stories tab backlog and engaged chips share one chip style and baseline; engaged label is number-first ("N engaged") to match backlog (sty_c7ab5180) — tag not cut (CI red on pre-existing agents.toml pin)

## [0.0.315] - 2026-07-22

### Changed
- Project tab headings: no link underline in any state; slight bold on hover (weight 500) under selected bold (600) with bold-ghost width reserve so the row does not reflow (sty_e4632f45)
- Stories tab active accent bar runs continuously under the engaged chip (cluster owns the border); chip hidden entirely when engagement count is 0, with live fragment insert/remove on 0↔n (sty_e4632f45)

## [serve-v0.0.9] - 2026-07-22

### Changed
- Same tab-heading polish and hide engaged-0 chip as CLI 0.0.315 (sty_e4632f45)

## [0.0.314] - 2026-07-22

### Added
- Root AGENTS.md: drive engaged stories/epics to a terminal status; dogfood as you progress; prefer `satelle help` for process detail (sty_c0de8674)

## [0.0.313] - 2026-07-22

### Fixed
- Project Stories tab "engaged 1" chip no longer nests a story link inside the tab anchor (invalid HTML that floated the chip between Stories and Tasks); badge is a tab-cluster sibling with CSS under `.tabs .n-engaged` (sty_dd9396d4)

## [serve-v0.0.8] - 2026-07-22

### Fixed
- Same engaged-chip nested-anchor layout fix as CLI 0.0.313 (sty_dd9396d4)

## [0.0.312] - 2026-07-22

### Fixed
- PreToolUse hook commands use an absolute path to satelle-hook.sh so a drifted agent shell cwd can no longer brick Bash/Edit with "No such file"; init/heal upgrades relative form; wrapper probes CLAUDE_PROJECT_DIR/SATELLE_PROJECT_DIR for binary resolution (sty_57582675)

## [0.0.311] - 2026-07-22

### Fixed
- Bash fence no longer treats fd-duplication redirects (`2>&1`, `>&2`, `n>&-`) as file targets, so allowed cross-repo `satelle story|task` commands with stderr merge are not falsely denied (sty_74c0556f)

## [0.0.310] - 2026-07-22

### Changed
- Local web client favicon matches satelle.dev animated ◐ monogram (SMIL terminator + reduced-motion fallback, brand green) (sty_2b1af84b)

## [serve-v0.0.7] - 2026-07-22

### Changed
- Same satelle.dev-aligned favicon monogram as CLI 0.0.310 (sty_2b1af84b)

## [0.0.309] - 2026-07-21

### Added
- Project Stories tab always shows live story-engagement count (including 0); soft-refreshes via `fragment/engagement` without full page reload (sty_01ba9482)

## [serve-v0.0.6] - 2026-07-21

### Added
- Same always-visible engagement count chrome as CLI 0.0.309 (sty_01ba9482)

## [0.0.308] - 2026-07-21

### Changed
- All agent bindings in `.satelle/agents.toml` set `effort = "high"` (reasoning/thinking) by default (sty_a82c3ed0)

## [0.0.307] - 2026-07-21

### Added
- Workspace landing shows Stories (with backlog badge), Tasks, Workflow, and Documents counts matching project tabs; `GET /fragment/projects` for soft-refresh without full-page reload (sty_f968f9db)

## [serve-v0.0.5] - 2026-07-21

### Added
- Same workspace landing counts + soft-refresh as CLI 0.0.307 (sty_f968f9db)

## [0.0.306] - 2026-07-21

### Changed
- Embedded `satelle-story-cancel-review`: supersede claims must name delivering story/commit and verify ACs; sibling bundling is an explicit reject with stop-and-surface (sty_9d7be832 / epic:scope-integrity)

## [0.0.305] - 2026-07-21

### Fixed
- Init/rebase seed all CSV edge skills (scope-review + workflow-change on baseline close), not only the first skill in the list (sty_814ad29a CI)

## [0.0.304] - 2026-07-21

### Added
- Embedded `satelle-story-scope-review` on implementation exit (baseline close + project in_progress→integration) using engagement baseline / story diff (sty_814ad29a / epic:scope-integrity)

## [0.0.303] - 2026-07-21

### Added
- Engagement baseline ledgered on first performing-state entry; `satelle story diff` enumerates changes since baseline (incl. untracked) for scope gates — report only, no pass/fail (sty_da169e03 / epic:scope-integrity)

## [0.0.302] - 2026-07-21

### Added
- Workflow panel diagram and Transitions list show full multi-reviewer CSV gates and parallel= concurrent marker (sty_1b7a0ca2)

## [serve-v0.0.4] - 2026-07-21

### Added
- Same workflow multi-reviewer / parallel diagram UX as CLI 0.0.302 (sty_1b7a0ca2)

## [0.0.301] - 2026-07-20

### Added
- Opt-in concurrent multi-reviewer gates: edge `parallel=true`/`parallel=N` runs CSV reviewers concurrently with no short-circuit; multi-reject errors name every rejecter; ledger records all verdicts (sty_4f0a15db)
- Plan gate dogfood: plan→in_progress runs plan-review + architecture + integration-coverage with `parallel=true` (sty_4f0a15db)

### Changed
- Default multi-reviewer behaviour remains sequential first-reject short-circuit unless `parallel=` is set

## [0.0.300] - 2026-07-20

### Added
- Workflow binding-form knowledge: edge CSV vs scoped on=, over-fire trap, list-order short-circuit in `satelle help workflows`; advisor check 7; init/workflows README pointers (sty_9882b8c6)
- `satelle workflow validate` WARN for single-state scoped reviewers that re-fire on rework inbound edges (non-fatal; sty_9882b8c6)
- Embedded `satelle-workflow-change-review` gate (n/a fast-accept when slice touches no workflow file), bound as CSV edge reviewer on baseline close and project in_progress→integration (sty_9882b8c6)

### Changed
- Planner binding: Claude/fable via command transport (plans only; Bash(satelle:*) for attach)

### Fixed
- Help topic reviewer-checks: paste defect jammed validate/done-gate prose mid DOT bullet (sty_46c584b1 dogfood)

## [0.0.299] - 2026-07-20

### Fixed
- Nav topbar no longer shows an unexplained **mirror** mode pill; empty-identity RO UI omits the strip, and identity email keeps a read-only/CLI-fed accessible description (sty_eea989dd)

## [serve-v0.0.3] - 2026-07-20

### Fixed
- Same topbar mirror-pill UX fix as CLI 0.0.299 (sty_eea989dd)

## [0.0.298] - 2026-07-20

### Verified
- Operator system unit ExecStart migrated to satelle-serve; live footer brands satelle-serve 0.0.2 (sty_2c51cc2c)

## [0.0.297] - 2026-07-20

### Verified
- Large-repo mirror STATUS/PROGRESS dogfood: live mutator matches CLI within ≤1s under 0.0.296-generation serve; original stale badge classified as (a) stale binary/unit (sty_3a76a5fa)

## [0.0.291] - 2026-07-20

### Removed
- Parallel epic path and `[gate] allow_parallel` opt-out — single performing story always enforced; deleted parallel workflows/skills/launcher (sty_a614a0ea)

## [0.0.296] - 2026-07-20

### Verified
- effort= field closed for sprint:7 order:3 (sty_657f77b9; shipped 0.0.294)

## [0.0.295] - 2026-07-20

### Verified
- Sprint 7 agent failover + effort fields closed on co-shipped 0.0.294 (sty_5bf61f89, sty_657f77b9)

## [0.0.294] - 2026-07-20

### Added
- Engagement seat recovery: `story set --status <same>` re-acquires a dropped seat on a performing story; edit-gate deny distinguishes dropped-seat vs no story (sty_4f74d01f / sprint:7)
- Agents.toml `effort=` (reasoning/thinking) with `{effort}` placeholder + ACP injection (sty_657f77b9)
- Agents.toml `secondary=` / `[defaults] secondary` — one retry on rate-limit/unavailable (sty_5bf61f89)

## [0.0.293] - 2026-07-20

### Verified
- Mirror partition lifecycle on operator host: junk `002`/`sse-abort*` partitions pruned; landing is satelle + solidsafe-engine-v0 + solidsafe-ui-v0 only (sty_eb61be02)

## [0.0.292] - 2026-07-20

### Added
- Mirror partition lifecycle: `POST /ingest/remove`, `satelle workspace partitions` / `workspace prune`, `workspace remove` purges the partition (sty_eb61be02 / epic:mirror-hygiene)
- Serve `X-Satelle-Instance` on `/healthz`; `SATELLE_SERVER_ENDPOINT` env (`none` disables discovery+push) (sty_5aa08259)

### Fixed
- Hermetic integration tests no longer seed the operator's live `:8787` mirror via auto-probe (sty_5aa08259)
- `workspace add` with no matching serve registers and exits 0 with seed skipped (was non-zero)

## [0.0.290] - 2026-07-20

### Added
- Opt-in `[gate.command_allow]` step policy: restrict git subcommands (e.g. push) to permitted story statuses via satelle.toml; commitgate enforces when configured (sty_c21490cc)

## [serve-v0.0.2] - 2026-07-20

### Changed
- Footer brands the running artifact via `buildinfo.Name` (`satelle` vs `satelle-serve`); Makefile and release.yml stamp Name per binary (sty_4a5c6924)
- `make check-serve-version` fails closed when serve-path sources change without advancing `satelle-serve.version`; release skill runs it on serve-path slices (sty_4a5c6924)

## [0.0.289] - 2026-07-20

### Fixed
- Serve landing slug uniqueness: snapshot ingest returns 409 when two repo_keys share a directory basename; landing/crumbs fall back to repo_key for legacy collisions so hrefs stay unique (sty_57d5ce25)

## [0.0.288] - 2026-07-20

### Changed
- `satelle workspace add` bootstraps `[server] endpoint` into `satelle.local.toml` when a local serve answers the service port, then seeds; fails non-zero with exact remedy when seed cannot proceed (sty_0122610a / epic:serve-adoption)
- `satelle init` qualifies registry-only `workspace: member` when endpoint is unset; help/README state the local.toml endpoint prerequisite

## [0.0.287] - 2026-07-20

### Changed
- README documents serve-adoption surface: workspace add, satelle-serve, independent versions, CI vs local tests; retired ui push named as removed (sty_1fef9026)

## [0.0.286] - 2026-07-20

### Fixed
- `satelle update` still refreshes satelle-serve from serve-v* when the CLI is already current (sty_19ff03f4)

## [0.0.285] - 2026-07-20

### Added
- Independent version keys in `.version`: `satelle.version` (CLI) and `satelle-serve.version` (serve); release tags `vX` (latest) and `serve-vY` (not latest) (sty_19ff03f4 / epic:serve-adoption)

### Changed
- Makefile stamps each main with its own version; release.yml / install.sh / `satelle update` resolve CLI and serve releases independently
- Changelog gate requires both CLI and serve-v headers when serve version is set; live footer dogfood greps serve version

## [serve-v0.0.1] - 2026-07-20

### Added
- First independent satelle-serve version line (sty_19ff03f4) — tag `serve-v0.0.1`, assets `satelle-serve-v0.0.1-*`

## [0.0.284] - 2026-07-20

### Added
- Dedicated `satelle-serve` binary (`cmd/satelle-serve`) for the push-fed mirror UI; Makefile, install.sh, release, and `satelle update` ship both artifacts (sty_80233c10 / epic:serve-adoption)
- `satelle service install` prefers sibling `satelle-serve` (or `--serve-bin`); falls back to `satelle serve` with a notice

### Changed
- Live verb-dispatch web.Server removed from `internal/web` — package is mirror-only (link isolation for the serve binary)
- `satelle serve` is a deprecated alias that shares `internal/serve.Run` with the dedicated binary

### Fixed
- `ledger.EventTelemetry` owns display extraction so web need not import verb

## [0.0.283] - 2026-07-20

### Breaking
- `satelle ui` / `satelle ui push` removed — use `satelle workspace add` (register + seed mirror). Invoking the old spelling prints the replacement (sty_805bee9c / epic:serve-adoption)

### Added
- `satelle workspace add` seeds the push-fed serve mirror when `[server] endpoint` is set; register-only notice when unset (sty_805bee9c)
- `satelle init` prints agent-readable `workspace: member` / `workspace: not-member` lines (sty_805bee9c)

### Changed
- README, projects help, and empty-mirror landing copy point at `workspace add` instead of `ui push`

## [0.0.282] - 2026-07-20

### Fixed
- CLI UI push drain: mutating verbs deliver change + snapshot before process exit (no fire-and-forget race against store close); coalesced one snapshot per invocation; black-holed endpoint bounded to 1.5s and fail-silent (sty_9ba3d709 / epic:serve-adoption)

## [0.0.281] - 2026-07-19

### Added
- Push-fed mirror UI parity: full snapshot kinds (docs with mod_time/provenance/source, ledger, seats, identity, settings, story docs) and template-rendered project/workspace pages on the mirror serve (sty_400c022b / epic:mirror-ui-parity)

### Changed
- Serve templates use absolute `/static/` assets so CSS/JS resolve under `/r/{slug}/` base href
- Browser and integration e2e re-enabled against the push-fed mirror (seed via ui push)

## [0.0.280] - 2026-07-19

### Added
- Push-fed read-only serve mirror (`~/.satelle/serve/mirror.db` per repo-key) with `/ingest/change` and `/ingest/snapshot` (sty_dbdadfa0)
- `satelle ui push` full-state reconcile to the local UI server; auto-snapshot on CLI mutations when `[server]` is set (sty_1dde0d47)

### Changed
- `satelle serve` is a single process with no supervisor children, no repo DB open, no fingerprint poller or maintenance loops
- Service unit WorkingDirectory is `$HOME` (not a single repo) for the push-fed server (sty_455f0d6e)

### Fixed
- Integration suite covers mirror render, SSE, restart restore, and non-ingest POST rejection

## [0.0.279] - 2026-07-18

### Added
- CLI change publisher: optional `[server] endpoint` fire-and-forget POSTs on the ChangeNotifier seam; init scaffolds the commented block; unreachable servers never fail verbs (sty_126228b2 / epic:serve-split order:2)

## [0.0.278] - 2026-07-18

### Added
- CLI substrate freshness without serve: post-story-verb backlog refresh, documented SessionStart + reindex + post-verb triggers, self-sufficiency integration test (sty_d0950127 / epic:serve-split order:1)

## [0.0.277] - 2026-07-18

### Fixed
- Fail-visible wrapper tests: binary-present re-emit/exit-code matrix and writeHookScripts retirement of legacy/kimi scripts (closes sty_616c5454 order:2 formal verification)

## [0.0.276] - 2026-07-18

### Changed
- On-demand harness scaffolds: `satelle init` no longer PATH-guesses or defaults to Claude; scaffolds only existing `.claude`/`.grok` dirs or `--harness claude,grok` (sty_92b5ad23)
- Store-backed verbs lazy-install the matching harness scaffold from session markers (`CLAUDE_CODE_*`, `GROK_AGENT`); first session of a new harness may lack hooks (documented trade-off)
- Single parameterized `.satelle/hooks/satelle-hook.sh gate|commitgate <harness>` replaces per-harness pretooluse scripts; re-init retires legacy scripts (epic:minimal-harness-footprint order:2)

### Fixed
- Integration tests that assumed default Claude scaffolds now pass `--harness` / existing dirs; local-binary re-exec and version-stamp tests isolate env/cwd from a repo pin (sty_92b5ad23)

## [0.0.275] - 2026-07-18

### Added
- agents.toml `interface = "command" | "acp"` dual transport for isolated dispatch; default remains full command templates (any CLI including Claude)
- ACP client runner for `interface=acp` (e.g. `grok agent stdio`): session protocol, permission auto-policy, decision fold, timeout kill (epic:agent-dispatch-transport / sty_669f060a, sty_2a8c5d6f, sty_d70c2f5a)
- `satelle help agent-dispatch` documents CLI control-plane in vs command/ACP agent I/O out; validate reports interface= on grants

### Changed
- `RunnerFromBinding` is the dispatch factory; agentstep and applyAgentGrants resolve interface+command together

## [0.0.274] - 2026-07-17

### Fixed
- Isolated agents receive story attachments in the transition payload `docs` array; skills/CTA stop directing attachment I/O at in-repo `.satelle/stories/`; status warns on leftover residue (sty_58fa970e)

## [0.0.273] - 2026-07-17

### Fixed
- `satelle migrate` / `runtime migrate` refuse relocating a live runtime (fresh engagement lease or responding serve); `--allow-live` overrides with an explicit stranded-writes warning; split-brain recovery documented in migrate help (sty_5308eb60)

## [0.0.272] - 2026-07-17

### Fixed
- Default `[gate] edit_exempt_paths` seeds `.gitignore` alongside `.satelle/` so init/migrate's managed `.gitignore` rewrite no longer trips the engaged-story stop hook; `satelle migrate` appends the entry to pre-existing non-empty lists without clobbering operator additions (empty list remains deliberate opt-out) (sty_f115e6bf)

## [0.0.271] - 2026-07-17

### Changed
- Hosted client adopts project-addressed sync routes (`/api/v1/projects/{project}/{config,documents,workstate}`); drops workspace id + `?project=` query; `published` stays workspace-level (sty_ca64d0cb)
- OpenAPI + contract tests pin the project-addressed surface; document sync cursor keys on (server, project, repo)
- Declared park nodes: `from="*"` / `from="s1,s2"` materialize inbound park edges; resume enforced to `park_origin` on work_items; wildcard edge endpoints rejected (sty_f75286dc)
- Workflows (project, substrate, baseline) collapse park idiom to `from="*"` on blocked; `satelle-dot-standard` documents the rule

# Changelog

All notable changes to satelle are documented here. Format: Keep a Changelog–style,
newest release first. Each release is a level-2 `## [X.Y.Z] - DATE` header.

**Breaking marker:** a non-empty `### Breaking` subsection under a version means that
version is breaking — the single marker require-init and post-upgrade heal key on.
Agents retrieve deltas with `satelle changelog [--from X] [--to Y]` (no git history).

## [0.0.270] - 2026-07-17

### Fixed
- `satelle init` re-run converges the managed `.gitignore` block (stale pre-relocation ignores replaced; content outside markers kept); init help names the home-keyed runtime plane (sty_87c8a69c)

### Tests
- Init-path regression for stale managed-block convergence and home-keyed help text (sty_87c8a69c)

## [0.0.269] - 2026-07-17

### Added
- `satelle migrate [--yes]`: dry-run-by-default compose verb that converges a repo to the current structure — runtime relocate, legacy residue removal, unedited-seed prune, gitignore managed-block converge, then deployment validation (sty_a3915840)

### Fixed
- `ensureGitignore` rewrites the content between managed markers to the current form (upgrade path for pre-relocation ignore blocks) (sty_87c8a69c / sty_a3915840)
- `satelle runtime migrate` is dry-run by default; pass `--yes` to apply (aligned with migrate/prune)

### Changed
- `satelle init` help describes the home-keyed runtime plane and steers half-upgraded repos to `satelle migrate`

## [0.0.268] - 2026-07-17

### Fixed
- Tests that resolve the home-keyed runtime plane without SATELLE_HOME now panic instead of minting orphan dirs under the real ~/.satelle (sty_c36c211f)

### Added
- `satelle runtime list [--orphans]`: list home-keyed key dirs with linked/stale/unknown status, repo reverse-map (repo.path marker + registry), and rm suggestions (sty_c36c211f)
- `testutil.IsolateHome` helper and `repo.path` marker written on open/init for reverse resolution (sty_c36c211f)

### Changed
- Integration host-surface guard records new runtime key dirs as pollution while still allowing pre-existing live-service key trees (sty_c36c211f)

## [0.0.267] - 2026-07-17

### Fixed
- Containment fence denies only another repo's git working tree; temp/scratchpad/non-repo paths are allowed (sty_a8454d10)
- cp/mv/rsync/ln treat only destinations as mutation targets so cross-repo reads are not denied (sty_a8454d10)

### Changed
- Shared foreign-tree predicate (`gitRootOf` / `foreignTreeTarget`) for Bash and Edit gates; removed `isBenignOutsidePath` allowlist (sty_a8454d10)

## [0.0.266] - 2026-07-16

### Fixed
- Status transitions that skip a required workflow DOT step are refused with the expected next step named, even when the transition gater is unwired (sty_ebd3d666)

## [0.0.265] - 2026-07-16

### Added
- Web effective-process view: provenance chips (default|edited|authored) on docs/workflows and node agent/model bindings on the workflow diagram (sty_ba0eb5c6)
- `process-view` verb: substrate list + agentvalidate allocations for CLI/web

### Changed
- Provenance classification extracted to `internal/substrate` (shared by CLI list and web)

## [0.0.264] - 2026-07-16

### Fixed
- Containment no longer denies Bash redirects to `/dev/null` and other stdio sinks (sty_aadd4d6c)

## [0.0.263] - 2026-07-16

### Added
- Cross-repo containment: Bash PreToolUse denies mutations outside the session-home anchor (sty_aadd4d6c)
- `[gate] allow_outside_tree_edits` opt-in (default deny) for deliberate multi-repo installs
- Session principle `satelle-cross-repo-containment` (create anywhere, action only at home)
- Quote-aware Bash tokenizer: `git -C` commit/push forms match; quoted prose does not

### Changed
- Session anchor prefers `SATELLE_PROJECT_DIR` / `CLAUDE_PROJECT_DIR` over CWD-derived config root

## [0.0.262] - 2026-07-16

### Added
- Virtual sparse defaults: workflows/skills/principles resolve from the binary without seeding unedited copies (sty_29e5a9a5)
- `satelle substrate list|edit|prune` for effective provenance, materialize-on-edit, and seed cleanup

### Changed
- Fresh `satelle init` no longer materialises unedited default markdown (tasks still seeded — coded gates need headers)
- DocIndex List/Count overlay embedded defaults at read time

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
