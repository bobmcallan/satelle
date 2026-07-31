## [0.0.368] - 2026-07-31

### Added
- **The web service repairs its own mirror.** The UI is a push-fed copy: the CLI posts a snapshot as each mutating verb finishes, and a push that did not land — the service was restarting, down, or unreachable — was lost permanently, leaving the page on its last good frame forever. The serving tier now re-requests a full snapshot for every project it renders: once at startup, which is what repairs the push a `satelle update` restart just swallowed, then every five minutes. It re-requests by running `satelle workspace add` in the repo, so the CLI remains the sole reader of a per-repo database and the mirror remains a read-only view (sty_e6e467fe)
- A view the service could not reconcile now says so: the landing row and the project header carry a **`stale · last update <time>`** flag naming the last confirmed ingest and the `satelle workspace add` remedy. A stale view is never again indistinguishable from a stuck story (sty_e6e467fe)
- `satelle help projects` gains a "When the UI looks stale" section, and `satelle workspace add --help` now states plainly that the verb is the manual re-seed — recovery used to be an undocumented side effect (sty_e6e467fe)

### Changed
- `/ingest/snapshot` fingerprints the body it receives. An identical re-post records freshness only — no row rewrite, no live-update doorbell — so the repair loop never makes an open page re-render on a timer and never rewrites state it does not need to (sty_e6e467fe)
- `SATELLE_SERVER_ENDPOINT` and its `none|off|-` off-switch moved to `internal/config`, so the serving tier honours exactly the same switch as the CLI push path (sty_e6e467fe)

### Fixed
- The mutating-verb path is untouched: the push stays fail-silent under its bounded budget, and no verb waits on UI delivery. CLI-side retry was considered and rejected — it repairs only the failures the CLI survived to notice, and it would put retry latency in every transition (sty_e6e467fe)
- The repair loop refuses a partition whose repo directory is gone, or whose database this machine's `SATELLE_HOME` does not hold, so a service under a foreign home cannot re-seed from an empty store and wipe the view it exists to repair (sty_e6e467fe)
- `satelle serve` binds its listener before starting the repair loop, so the startup pass cannot race its own endpoint (sty_e6e467fe)

## [0.0.367] - 2026-07-31

### Added
- **`satelle doctor`** — one diagnostic surface answering whether satelle is ready to govern a repository. It COMPOSES the validators that already own their rules (the agents layer and its machine-wide profile references, workflow structure, cross-workflow consistency, lifecycle-hook allocations, reviewer ceilings, required binaries, harness scaffolding) rather than adding rules of its own, so init, doctor, and the engagement precondition can never form three different opinions about the same repo (sty_e9da28e2)
- `satelle doctor --all` checks every registered workspace repository **independently**: each root is contained, and an unreadable or uninitialised one becomes its own one-finding report rather than aborting the sweep. Output ends with a healthy/unhealthy tally and the exit code is the worst result across all of them (sty_e9da28e2)
- `satelle doctor --live` adds bounded, opt-in provider probes — a `--version` call for a command binding, a single `initialize` handshake for an ACP one. Neither opens a session or sends a prompt, so **ordinary doctor performs no paid and no network model call at all**. Every probe runs in its own process group and is killed and reaped on the deadline or on cancellation, so a probe never leaves a provider process behind. Authentication is diagnosed only where the provider says so; an unexplained failure is reported as a spawn failure rather than guessed at (sty_e9da28e2)
- **`internal/health`** — the shared finding vocabulary: a stable identifier, severity, and remediation per defect. The identifier is the contract, so the same defect reports the same id whether you meet it in `doctor`, in `satelle init`, or in an engagement refusal (sty_e9da28e2)
- `satelle doctor --json` emits repos, findings, grants with per-field sources, gate and hook allocations, a summary, and the exit code. Exit codes are documented in both the help and the payload: `0` healthy (warnings allowed), `1` error findings, `2` doctor could not run (sty_e9da28e2)
- `satelle help doctor` documents severity, exit codes, the `--live` side effect, and the distinction between repo workflow POLICY (`.satelle/`) and machine-wide EXECUTION profiles (`~/.satelle/agents.toml`) (sty_e9da28e2)

### Changed
- `satelle init`'s deployment validation is now a PRINTER over `doctor.Check`, not a second check list. Its FAIL/WARN lines carry the finding identifier alongside the artifact. The only check init still owns is the substrate analysis of what init itself just wrote (sty_e9da28e2)
- `satelle service status` reports each registered repository's readiness — `N registered — H healthy, U unhealthy`, naming each unhealthy repo and its first finding — so an unhealthy repo is visible from the service surface instead of looking identical to a ready one. It stays informational: the service itself is running fine. The diagnostic lives on the CLI side deliberately, because satelle-serve is a push-fed mirror that never opens a repo database (sty_e9da28e2)
- An engagement refusal is rendered from the shared vocabulary, so the identifiers and remediation it carries are the ones `satelle doctor` prints for the same repo. Fail-closed behaviour is unchanged (sty_e9da28e2)
- `agentvalidate` reports `Findings` additively: `Problems`/`Warnings` are now derived from them, each finding's detail being the exact prose that always appeared, so the two surfaces cannot drift. Identifiers are assigned by the producing check, never by matching message text (sty_e9da28e2)
- `satelle agent validate` and `satelle doctor` render per-field provenance through one function, so the two displays cannot diverge (sty_e9da28e2)

### Fixed
- Doctor judges the workflows that actually GOVERN a repo — authored files plus the embedded defaults none of them shadows. Reading only the authored directory left a repo governed by an embedded default with no allocation or lifecycle-hook checking at all, reported healthy without the governing workflow ever being looked at. The consistency (ambiguity) check still sees only authored files, because an on-disk wildcard workflow legitimately overrides the embedded wildcard baseline (sty_e9da28e2)
- A missing agent executable is a WARNING, not a failure. Shipping it as an error refused `satelle init` on any machine without the provider CLI on PATH — including CI. A repo is legitimately initialised before its CLI exists; the gates stay inert until one is, and dispatch already refuses at the moment it matters. A MALFORMED command (one whose first token is a placeholder) stays an error: no environment can make it work (sty_e9da28e2)
- Environment values are never printed, in any mode including `--json`. Doctor lists env KEY names with whether each resolved; an unresolved `${VAR}` names the key, never its contents (sty_e9da28e2)

## [0.0.366] - 2026-07-30

### Added
- **Lifecycle hooks are declarable substrate.** A workflow can now declare an operation that fires OUTSIDE the status graph — story creation today — naming both the skill that judges it and the logical agent that runs that skill. Previously create review resolved a skill from `create_review:` frontmatter and ran it against an EMPTY agent selector, which the engine silently resolved to `[reviewer]`; nothing in the substrate could inspect or change that allocation, and nothing validated it (sty_ede16f51)
- One generic grammar, in the new stdlib-only `internal/wfhook`: `hooks:` with `operation`, `skill`, and an optional `agent`. Go's entire per-operation knowledge is a table of which operations exist and which yield a verdict — no provider, model, effort, command, or tool grant appears anywhere on the hook path, and keys like `model:` on a hook are refused. A future lifecycle operation is a name in that table plus its call site, not a new parser, resolver, or validator (sty_ede16f51)
- `satelle workflow show <name>` renders one workflow's identity, graph shape, and each hook's full allocation: operation, skill, logical agent, the machine-wide profile or local binding it resolves through, interface, model, effort, permission ceiling, and source files. Read-only and deliberately tolerant — an unresolvable allocation is marked UNRESOLVED rather than failing, because diagnosing a misconfiguration is what the command is for (sty_ede16f51)
- `satelle agent validate` prints a `HOOK` allocation line per declared hook, carrying how it was declared, and no longer reports a binding a hook allocates as orphaned (sty_ede16f51)
- `satelle help workflows` gains a "Four things a workflow governs" section distinguishing transition nodes/edges, lifecycle hooks, deterministic structure checks, and agent judgments — where each is declared, when it fires, and who decides (sty_ede16f51)

### Changed
- The scalar `create_review: <skill>` form remains fully supported as the documented shorthand: it resolves to the same hook with `agent = reviewer`, but as a DECLARED default with provenance rather than an empty string falling through `gateBinding`. Every shipped workflow — this repo's five and the four embedded defaults — is left on the shorthand, which is the compatibility proof (sty_ede16f51)
- Validation refuses a bad hook before it can run, split by ownership so neither check reports the other's findings twice: `workflow validate` owns the DECLARATION (unresolved skill, unknown operation, missing skill, an operation declared both ways), `agent validate` owns the ALLOCATION (missing binding, non-reviewer role on a verdict hook, an in-loop binding that cannot produce an isolated verdict). `satelle agent validate` runs both (sty_ede16f51)
- `satelle init` seeds hook skills through the same parser, so a `hooks:`-declared skill travels with the default solution — the previous scalar-only read would have missed it (sty_ede16f51)

### Fixed
- Hooks are checked even when a workflow's DOT block does not parse. They are frontmatter, so an unparseable graph must not hide a broken allocation (sty_ede16f51)
- The reviewer permission ceiling is judged in exactly one place. A first cut re-checked it per hook, which turned an intentionally-advisory heuristic into a hard failure and rejected every workflow's shorthand hook in any repo whose reviewer template the heuristic cannot classify. `checkBinding` remains the single owner, keeping its settled severity split: a provable escape (a Codex danger sandbox) is a hard problem, an unexpressed ceiling stays a warning (sty_ede16f51)

## [0.0.365] - 2026-07-30

### Added
- **Machine-wide agent profiles.** An operator running satelle across several repositories can now define each provider's execution binding once, in a profile catalog at `~/.satelle/agents.toml`, instead of restating it in every repo. A profile carries the full `AgentBinding` execution surface — role, interface, command, model, effort, tools, timeout, principles, env, settings, secondary — and may extend another profile (sty_c7dfeedf)
- A repo consumes a profile only by naming it: `profile = "<name>"` on a binding in `.satelle/agents.toml`. Anything the repo states inline still wins, and `env`/`settings` merge key-wise with the repo's key winning. `role` is identity, not an override — a repo declaring a role that contradicts the profile's is refused rather than silently resolved either way (sty_c7dfeedf)
- `satelle agent profiles` lists the catalog; `satelle agent validate` now reports every effective field with the tier that supplied it — `repo`, `profile:<name>`, `global-role:<name>`, or `embedded` — so an operator sees not only what will run but where it was authored. `env`/`settings` lines name the field and its source only; values may be secrets and are never printed (sty_c7dfeedf)
- `satelle agent migrate` seeds a starter catalog from the machine's selected `[agent] cli`. It is opt-in, never automatic, never overwrites an existing catalog, and writes nothing into any repository. The catalog it seeds leaves `[roles]` commented out, so it changes nothing until a repo opts in (sty_c7dfeedf)
- `satelle help global-agents` documents the file, the precedence ladder, and the boundary (sty_c7dfeedf)

### Changed
- Resolution has **one** site — `config.LoadEffectiveAgents` — which every surface (bootstrap, `agent validate`, `init` deployment validation, the process view) now calls. Two independently-merging call sites was the defect worth designing against (sty_c7dfeedf)
- The `${VAR}` KV is layered: the catalog's `[vars]` is the machine-wide base and a repo's own `[vars]` (with its gitignored `satelle.local.toml` overlay) win per key. Expansion still happens in memory at dispatch wiring, so a machine-wide secret referenced as `${NAME}` reaches the agent process without ever being written into a repository (sty_c7dfeedf)
- Reviewer ceiling, interface, context-channel and workflow-allocation checks all run against the **merged** binding, so a profile cannot smuggle a capability past a check by supplying it machine-wide (sty_c7dfeedf)

### Fixed
- Nothing changes for an existing installation. With no catalog on the machine — including a repo relying on `~/.satelle/config.toml [agent] cli` — every repo resolves exactly as before, and a repo with no `profile=` anywhere is untouched by whatever an operator later adds to the catalog. In particular there is **no implicit same-name merge**: a profile called `reviewer` and a repo `[reviewer]` that never mentions it do not combine (sty_c7dfeedf)
- The catalog is execution configuration only, enforced mechanically rather than by convention: a profile carrying `applies_to`, `skill`, `prompt`, `on`, `output_*` or any other policy key is refused at load, and `LoadGlobalAgents` reads three sections and nothing else — workflows and skills dropped into the machine-wide home are invisible to workflow discovery (sty_c7dfeedf)

## [0.0.364] - 2026-07-30

### Changed
- The planner benchmark is now a **controlled study** rather than a race. `tests/plannerbench` answers three questions independently — transport at a fixed provider/model, provider-or-model at a fixed interface, and isolated dispatch versus the in-loop executor — and a comparison is readable only when every dimension it declares held is identical across sides. Anything else is reported as confounded (naming the differing dimension), underpowered, or skipped-with-a-reason, and yields no conclusion. There is no code path that names a provider winner from unmatched cells (sty_115eec5c)
- The study is **data**, not Go: `study.json` declares bindings, comparisons (`free_variable` + `holds`), context bands, seed and sample minimum. Adding a provider or a question is a JSON edit, and nothing in the report or classifier names a provider (sty_115eec5c)
- Fixtures are **seeded source trees** under `testdata/fixtures/<name>/tree/` — real multi-package Go with a `cmd/` entry point, declared symbols and an existing `_test.go` — plus per-criterion `expected_seams` as the oracle's ground truth. The previous synthetic fixtures were title/body/acceptance rows over an empty repo, which also made the read-only fidelity check vacuous: `productDigest` over an empty directory could not fail no matter what the agent did (sty_115eec5c)
- Samples are scheduled as a full work list and **shuffled** under the recorded seed, so run order is randomized across cells instead of nested per binding, where the binding that ran last absorbed every warm-cache and rate-limit effect of the study. Each sample records its global `run_order`, and percentiles use nearest-rank with no interpolation — at n=3 interpolation invents a value no sample produced (sty_115eec5c)
- `report.md`/`report.json` are pure: records in, report out, no clock. `make planner-report` regenerates a byte-identical report from the durable per-sample files without spending a token. The closing binding-change recommendation is gated — it may only fire from a supported comparison whose p50 gap clears the study's declared threshold, never from a collection-mixed one (sty_115eec5c)

### Fixed
- **The artifact score was the transition validator.** `scorePlan` called `agentartifact.ValidateAll` — the same function the gate runs — so the score was true exactly when the run committed and carried no independent quality signal. The oracle is now independent (this package no longer imports `agentartifact` at all, asserted structurally by parsing its own imports): it scores substance against the seeded tree — a file that exists, a symbol the tree actually declares, and a test named in the same section as one of those hits. The literal string `AC<n>` contributes nothing, so a plan that labels every criterion and reaches no seam scores zero and is flagged `label_only`. A committed run can now score low, and a refused run is still scored from the body recovered out of the executor log (sty_115eec5c)
- **Unreported usage could surface as an available zero, and only one attempt was counted.** Token totals came from the first regex match in the ledger text even though the repair/escalate policy makes up to three attempts, and a `TOTAL 0` cost row flipped usage to available. Usage is now summed across every `agent-attempt` event, the numeric fields are pointers that marshal away entirely unless some attempt reported usage, and `satelle story cost` is a cross-check that can never decide availability. A genuinely reported zero stays available with `tokens_total: 0` — reported-zero and unreported are different facts (sty_115eec5c)
- **Failure class came from substring scans of combined output.** `strings.Contains(out, "auth")` matched "author" and "authoritative", routing a quality failure into an infrastructure exit. Classification now reads only typed values: the engine's `agent-failure.outcome`, the attempt event's `validator_ok` and phase, the process exit status, and the harness's own `exec.LookPath` and digest facts. `malformed_output` and `denied_mutation` are deliberately not infrastructure — a model that answered badly, or a performer that wrote when it should not have, are results worth counting. The text-matched `auth` class is removed rather than reimplemented; auth failures surface as `exit_status` with the message preserved in `diagnostics.detail` (sty_115eec5c)
- **The failure-mode tests asserted the class literal they were handed**, which proved nothing about classification. All nine classes are now exercised through the real path — an actual `satelle story set --status plan` transition against a stub agent script — covering none, exit_status, signal_killed, timeout, malformed_output, denied_mutation, spawn, setup and attachment. Those runs spend no tokens, so they are permanent hermetic coverage rather than an opt-in extra (sty_115eec5c)
- **The benchmark bindings granted `Bash(satelle:*)` and called it read-only**, so the study did not measure the tool policy the planner ships with. Bindings now default to the grant read from the repo's own `[planner]` section, and a binding that diverges must declare its own policy name and a reason; both `shipped-planner-grant` and the legacy `read-only` label are reserved against it. Because the report holds `tool_policy` as a dimension, a divergent binding cannot be compared against a shipped-grant one — so the default study's cross-provider comparison is confounded by construction, and says so (sty_115eec5c)
- Topology is a first-class variable, so an in-loop session's richer progressive interaction is no longer attributable to whichever provider happened to run in-loop. In-loop samples cannot be spawned by a test (the in-loop executor *is* the driving session), so they are ingested as operator attestations and must carry the same dimensions and the same accounting — interventions, conversation state, visible progress — as an instrumented sample, or they are refused. Topology conclusions are always labelled collection-mixed and never justify a default change (sty_115eec5c)

## [0.0.363] - 2026-07-30

### Fixed
- Unit tests no longer read the operator's real `/etc/systemd/system/satelle.service`. `systemUnitPath()` hardcoded that absolute path and no test redirected it, so `go test ./...` passed or failed depending on whether the developer happened to have a satelle system unit installed — and six tests in `internal/cli` went red the moment one was. GitHub runners have none, so CI would have stayed green while every developer with a real install saw red: CI and the developer machine silently testing different things (sty_d50218d1)
- The path now resolves through a package-level `systemUnitDir` that hermetic tests redirect to a temp dir, the same idiom `restartHooks` and `procRoot` already use. Production is unchanged: the default is the real location, nothing outside tests assigns it, and there is deliberately no environment or flag knob for a test-only need (sty_d50218d1)
- The six tests were repaired by isolating their environment, not by relaxing assertions — each still asserts exactly what it did before. `TestSystemUnitPathIsInjected` guards the isolation itself (empty `HOME` so the user unit cannot mask the system one), so a revert to a hardcoded path fails the suite (sty_d50218d1)

### Added
- The project workflow now carries four coded deployment gates, one per objective, so a failed deployment names the objective that failed: `satelle-build-unit-check` (go build + go test, on entry to integration — nothing ran the unit suite in the workflow before this), `satelle-integration-check` (make integration, plus an assertion that the operator's installed binary and running service are byte-identical before and after), `satelle-ci-published-check` (the test and release workflows concluded success for the actual HEAD SHA, and the tag for .version is published), and `satelle-dogfood-check` (the installed CLI reports .version and the serving process runs the installed binary under a persistent supervisor). All four VERIFY and never PERFORM — no gate pushes, tags, updates, installs or restarts. Substrate only; no binary change (sty_7a2dc74b)

## [0.0.362] - 2026-07-30

### Fixed
- **Regression in 0.0.361:** `satelle update`'s bus-free restart sent SIGTERM first and escalated to SIGKILL. systemd classifies SIGTERM as a CLEAN exit, so a `Restart=on-failure` unit did not respawn from it — it STOPPED, permanently, and the escalation could not recover it because the process was already gone. Any user on a bus-unreachable host whose unit predates 0.0.361 (every user unit satelle wrote before then) lost their service on update. **Upgrade from 0.0.361.** (sty_d45618d5)
- The signal is now DERIVED from the unit's `Restart=` policy, read from the unit file on disk with no bus: `always` → SIGTERM, `on-failure` → SIGKILL (the only signal systemd counts as a failure, hence the only one that unit respawns from). A signal is sent only when its effect on that policy is established (sty_d45618d5)
- Blind escalation is removed rather than reordered: exactly one signal is sent, ever. A policy that never respawns (`Restart=no`, absent, an unestablished conditional like `on-abnormal`) or an unreadable unit file means NO signal at all — the process is left running and the output says why, because stopping a service nothing will restart is strictly worse than leaving it stale (sty_d45618d5)

### Changed
- One reader now serves every unit-file directive the update path consults (`installedUnitFile` + `unitDirective`), so exe identity and the Restart policy can never be read from different files or parsed by different scanners (sty_d45618d5)
- The test fake now models systemd's documented signal semantics instead of the author's expectation — an on-failure unit that receives SIGTERM goes down and stays down in the suite. The 0.0.361 defect was invisible because its fake respawned on SIGKILL regardless; a fake cannot falsify a belief about the system it stands in for (sty_d45618d5)

## [0.0.361] - 2026-07-30

### Fixed
- `satelle update` now cycles the live service onto the new binary **without a reachable systemd bus**, instead of printing two systemctl commands that cannot work on the affected host. A supervisor respawns its own child from the unit file autonomously — D-Bus is only how an *external* actor requests a restart — so update signals the stale process and lets the supervisor do it. Graceful stop first, escalating to a forceful one only when a legacy `Restart=on-failure` unit ignores it (sty_f20f3f3b)
- Success is confirmed from kernel facts — the pid now holding the port, its exe identity against the installed binary, and that its parent is still the same supervisor — never from the signal's exit code. A cycle that does not converge makes `satelle update` exit non-zero with what it observed, so it can no longer report an installed-and-live release it did not verify (sty_f20f3f3b)
- Releases no longer require the operator to restart the service by hand. Three release stories (sty_acd4b61e, sty_4e6f0788, sty_87c0ef37) were delayed or parked by this; the last was parked at `release` with a green, published, CI-verified build and every check passing except the live footer (sty_f20f3f3b)

### Changed
- A process under **no persistent supervisor** is now deliberately left running rather than signalled — nothing would respawn it, so stopping it would be strictly worse than leaving it stale. `satelle update` says so and names the one-time `satelle service install --system` fix. A systemd *user* manager without lingering counts as non-persistent, because it dies with the login session (sty_f20f3f3b)
- The per-user unit written by `satelle service install` is now `Restart=always` (was `on-failure`) so a graceful stop respawns it. Units already on disk keep their policy until re-installed — adopting a new one needs `daemon-reload`, which needs the bus this path exists to avoid — and are handled by the escalation instead (sty_f20f3f3b)
- `.satelle/skills/release.md` no longer instructs the operator to cycle the service. `check_live_footer` is now a verification of what `satelle update` already did, the manual commands are demoted to the unsupervised case, and the section states that a per-release hand-restart is a defect to raise rather than a step to follow (sty_f20f3f3b)

## [0.0.360] - 2026-07-30

### Added
- `satelle agent validate` now JUDGES the role-vs-grant contract it previously only displayed. A binding a workflow allocates to a DISPATCHED performer (a spine `agent=<name>` node, or `on_enter_agent=`) whose `tools` grant carries no context channel is now an ERROR naming the binding, the allocating node, and both fixes — surfaced before a story is engaged, instead of as a dispatch refusal mid-transition (sty_87c0ef37)
- A `role = "reviewer"` binding that grants shell is now a WARNING (exit 0). Reviewers are fed their documents in the transition payload and never reach the dispatch path that consults a grant, so the shell is capability that is never exercised. Stated as the mechanical fact — an unused grant — not as a prohibition; whether a repo keeps it stays the repo's call (sty_87c0ef37)
- `satelle help agent-dispatch` states the contract positively and once, under "What each role needs": performers PULL their context and so need one of two channels (`Bash(satelle:*)` or `read_file`); reviewers are PUSHED theirs and need none. Previously the rule was only inferable from a refusal message you had to trigger first (sty_87c0ef37)

### Changed
- One predicate now owns "does this grant carry a context channel": `config.GrantsContextChannel`. The private `agentstep.grantsSatelleCLI` is deleted and its three dispatch call sites retargeted, so the runtime refusal and the validate error cannot disagree about any grant string. It carries its own quote-stripping tokenizer, so a quoted TOML token is judged identically on both paths (sty_87c0ef37)
- `agentstep.isInLoopCommand` likewise folded into `config.IsInLoopCommand` rather than left as a second copy alongside it (sty_87c0ef37)
- Engagement now refuses one transition EARLIER for an under-granted performer. `agentvalidate` is the shared authority for `satelle agent validate`, `satelle init`, and leaving the workflow entry state, so a workflow allocating a performer whose grant has no context channel is refused at engage rather than at the dispatch it protects — no agent is spawned. Same condition, same predicate, reported sooner (sty_87c0ef37)

### Fixed
- The `agent-dispatch` help topic no longer claims satelle's grant check "expects Claude-shaped tools for dispatch" — that contradicted `read_file` being an accepted channel, and would have sent a Grok-native repo hunting for a `Bash(satelle:*)` it does not need (sty_87c0ef37)

## [0.0.359] - 2026-07-30

### Added
- `satelle status --line` renders one statusline row — a live server link plus the engaged `<story_id>::<stage>` — for a terminal status area. It is strictly read-only: the engaged story comes from the `Leases.List` seat lookup, never `satelle story seat`, which reaps stale leases (writes) and would thrash lease state under Claude's 300ms debounce. It never fails loudly: any error yields one degraded, well-formed line and exit 0, so a statusline can never show a stack trace (sty_4e6f0788)
- `satelle agents install claude|all` now installs a `statusLine` entry into `.claude/settings.json` running that renderer. Claude allows exactly one statusLine and offers no composition, so a statusLine the operator already owns is left byte-for-byte, the install still succeeds, and the exact snippet to fold satelle's segment into their own script is printed instead. `satelle agents remove` prunes only a satelle-owned entry (sty_4e6f0788)
- The link degrades honestly: it is a real OSC 8 hyperlink only on terminals known to render one (iTerm2, WezTerm, Kitty) and plain text everywhere else, so Terminal.app never receives raw escape bytes; and only a service that actually answers is linked at all — a dead one renders unlinked and says so (sty_4e6f0788)

### Changed
- Grok and Codex intentionally get no statusline, and the reason is now recorded in the tree (`satelle agents` help and the Claude statusline source): Grok has no scriptable statusline, and Codex's built-in `[tui].status_line` takes a fixed item list with no command backing. Both harnesses see the same facts through the SessionStart availability line (sty_4e6f0788)

## [0.0.358] - 2026-07-30

### Changed
- `satelle status` now reports the web service as a URL plus its real answering state (`live` / `not answering`), probed via the existing `healthzOK` seam, replacing the `web port <n>` config echo — a value that read like confirmation but could not fail (sty_fb5e6d96)
- The global service port is now resolved through one path (`servicePortResolved`, which `servicePort` delegates to) for every surface that states a user-facing URL, so `satelle status` and the session-start line can never name different ports for the same machine state (sty_fb5e6d96)

### Added
- `satelle hook context` injects one line naming the web service URL and whether anything is answering on it, so a new session learns where the server is without asking the agent. It reaches Claude, Grok and Codex through the SessionStart wiring all three scaffolds already carry — no per-harness config change. An unreadable global config renders as `availability unknown`, never a fabricated `live`, and the hook stays fail-open (sty_fb5e6d96)

## [0.0.357] - 2026-07-30

### Fixed
- `satelle service status` now derives its verdict from the unit file on disk, cgroup/listening-port process discovery, and exe identity (shared with `satelle update`'s restart path), instead of collapsing a failed or empty `systemctl --user is-active` query into "inactive (not installed or stopped)" — the exact false negative that wrongly parked two release stories as "world-not-ready" on a WSL host whose user-session D-Bus was unreachable (sty_acd4b61e)
- `.satelle/skills/release.md`'s dogfood triad now names `satelle service status` as bus-independent evidence for `check_persistent_supervisor` and `check_live_footer`, so both checks are satisfiable on a host with no reachable user bus (sty_acd4b61e)

## [0.0.356] - 2026-07-30

### Fixed
- Exe-identity extraction now compiles on Windows via a platform-specific helper (`syscall.Stat_t` does not exist there); the v0.0.355 release failed its cross-compile matrix on this exact line and never published a tag (sty_c344d080)

## [0.0.355] - 2026-07-30

### Fixed
- `satelle update`'s restart step now verifies the live process is running the newly installed binary by exe device+inode identity, instead of trusting a restart command's exit code or a version string that may not have changed (sty_c344d080)
- When neither the systemd user nor system bus is reachable, the running service is now located via cgroup and listening-port inspection so a stale-but-running process is never reported identically to "no service configured" (sty_c344d080)
- A start-limited (restart-exhausted) systemd unit is now named explicitly with a `reset-failed` remedy, instead of falling through to a generic "no restartable service was found" message (sty_c344d080)

## [0.0.354] - 2026-07-30

### Fixed
- satelle-code-ac-review now judges a single primary objective and falsifies prohibition/invariant-shaped acceptance criteria by enumerating the paths that could violate them, instead of accepting on the presence of one compliant sample (sty_02acce1b)
- satelle-reviewer-objective-audit now audits reviewer skills for evidentiary underreach (a presence-only bar unable to falsify a prohibition) alongside its existing overreach checks (sty_02acce1b)

## [0.0.353] - 2026-07-30

### Fixed
- Planner benchmark runs now preserve schema-versioned redacted final results, attached artifacts, hashes, timestamps, and structured diagnostics in durable per-run evidence (sty_dec87e60)
- Artifact scoring now parses the attached document body and reports explicit findings per acceptance criterion instead of relying on a fragile aggregate substring oracle (sty_dec87e60)
- Unreported benchmark usage is now represented as unavailable with provenance and omitted token totals rather than ambiguous numeric zero (sty_dec87e60)

### Changed
- Infrastructure and under-sample failures now fail the live benchmark harness while artifact-quality findings remain auditable benchmark data, with hermetic coverage for failure classes and interruption retention (sty_dec87e60)

## [0.0.352] - 2026-07-30

### Added
- Contracted isolated steps can opt into bounded validate–repair–escalate policies through skill frontmatter, including efforts, escalation bindings, attempt limits, token/time budgets, and fail-on-exhaust handling (sty_b5c082ac)
- Every quality attempt records provider-neutral binding, model, effort, duration, usage availability, validator findings, and escalation reason telemetry (sty_b5c082ac)

### Changed
- Structured artifact validation now reports all deterministic findings for targeted repair, attaches only the final valid candidate, and distinguishes unreported usage from measured zero (sty_b5c082ac)
- Agent-dispatch help documents policy defaults, rate-limit failover separation, budget behavior, cancellation, and attempt evidence (sty_b5c082ac)

## [0.0.351] - 2026-07-30

### Added
- Skills can declare generic typed output contracts that Satelle decodes, validates, and attaches before committing a dispatched workflow transition (sty_52dbbf69)
- Command and ACP final responses share one structured artifact decoder with field-level validation and optional acceptance-criterion coverage (sty_52dbbf69)

### Changed
- Contracted planners can run with read-only repository tools and return JSON artifacts, while legacy self-attaching steps retain their existing migration-compatible path (sty_52dbbf69)
- Agent-dispatch help documents structured output contracts, canonical envelopes, transactional refusal, and legacy migration (sty_52dbbf69)

## [0.0.350] - 2026-07-30

### Added
- Provider-neutral progressive execution events now cover lifecycle, heartbeats, safe messages, tool activity, artifact candidates, usage, completion, and failure across command and ACP agents (sty_f926256c)
- Named dispatches now write normalized live diagnostics, with an explicit opt-in raw transport trace that filters hidden reasoning and redacts credential-shaped values (sty_f926256c)

### Changed
- Interactive isolated-agent progress and silence-aware heartbeats now stream on stderr while structured stdout and the authoritative final agent response remain unchanged (sty_f926256c)
- Agent-dispatch help documents normalized diagnostics and the `SATELLE_AGENT_TRACE_RAW` troubleshooting opt-in (sty_f926256c)

## [0.0.349] - 2026-07-30

### Fixed
- Claude, Grok, and Codex PreToolUse wrappers now return coherent structured denials with actionable reasons instead of mixing JSON stdout with exit 2 and empty stderr (sty_56cda59c)
- Hook infrastructure and malformed-output failures now use safe harness-specific fallback denials without leaking captured stderr, while irrelevant Bash remains fail-open (sty_56cda59c)

### Changed
- Agent-dispatch help documents each harness deny envelope and the distinction between structured exit-zero handling and exit-two stderr fallback (sty_56cda59c)

## [0.0.348] - 2026-07-29

### Added
- Opt-in Claude command vs Grok ACP planner benchmark with representative fixtures and wall-time, usage, artifact, policy, and failure-observability evidence (sty_a3526df6)

### Fixed
- Workflow edit hooks now derive source-edit permission from executor-owned DOT states, deny driving-session work-ahead during transitions, and preserve exact isolated-performer dispatches across Claude, Grok, and Codex (sty_a3526df6)
- In-tree shell mutations are governed consistently through edit and commit hooks, while read-only preflight and Satelle artifact recording remain available (sty_a3526df6)

### Changed
- Planner transport evidence retains Claude command as the repository default because the one-run ACP pilot did not meet the predeclared artifact and observability threshold (sty_a3526df6)

## [0.0.347] - 2026-07-29

### Fixed
- Codex ACP no longer calls authenticate for CLI-owned api-key/chat-gpt methods; session reuse follows operator `codex login` (sty_71491143)
- Agent-dispatch help documents CLI-owned auth for live Codex dogfood instead of SATELLE_CODEX_DOGFOOD (sty_71491143)

## [0.0.346] - 2026-07-29

### Fixed
- Codex compliance hooks now use documented tool matchers and explicit synchronous command handlers; local hook verification uses Codex CLI login and proves SessionStart plus no-story PreToolUse denial (sty_71491143)
- Codex install/remove verification now covers all generated hook ownership and the engaged-story allow path (sty_71491143)

### Changed
- Codex help and binding guidance no longer require Satelle-specific credential or opt-in environment variables (sty_71491143)

## [0.0.345] - 2026-07-29

### Added
- `satelle agents install|remove` creates/reconciles harness compliance scaffolds (`.claude/settings.json`, `.grok/hooks/satelle.json`, `.codex/hooks.json`) with blocking PreToolUse hooks, in addition to launchers (sty_9e86f407)
- Codex harness support: `.codex/hooks.json`, `hook gate|commitgate --harness codex`, init `--harness codex` (sty_9e86f407)
- Hermetic installed-launcher ACP suite and opt-in `make codex-smoke` / `tests/codexlive` (sty_9e86f407)

### Fixed
- Codex ACP install binding no longer appends unsupported `stdio` subcommand; generated command is `sh <launcher>` → `npx -y @agentclientprotocol/codex-acp` (sty_9e86f407)
- `bashCommandFromEvent` accepts Codex shell `command` as a JSON argv array (sty_9e86f407)

### Changed
- Agent-dispatch help and README document three-harness compliance, ownership boundaries, and live smoke (sty_9e86f407)
- Re-run `satelle init` (or `agents install`) after upgrade: `satelle-hook.sh` now forwards `--harness` (scaffold drift until healed)

## [0.0.344] - 2026-07-29

### Fixed
- Codex ACP no longer receives the Grok-only `--reasoning-effort` argv flag; effort rides `session/set_config_option` (Grok ACP argv injection preserved) (sty_aa726901)
- Command-transport Codex forwards binding `effort` via `-c model_reasoning_effort="{effort}"` (empty drops the flag) (sty_aa726901)
- Agent validate hard-rejects `role=reviewer` Codex *command* templates whose sandbox is not read-only (`workspace-write` or omitted), not only danger modes (sty_aa726901)

### Added
- Hermetic Codex-shaped ACP fixtures for init, model/effort config, CaptureAnswer/Full, mutation deny, Satelle-only grant contract (sty_aa726901)
- `satelle agents install|remove` for `claude`/`grok`/`codex`/`all` — satelle-owned launchers under `$SATELLE_HOME/agents/bin/` without changing the default reviewer (sty_aa726901)

### Changed
- `buildArgs` supports fused `{model}`/`{effort}`/`{settings}` placeholders (exact-token behaviour unchanged for `{system}`/`{payload}`) (sty_aa726901)
- Agent-dispatch help documents effort per transport, Codex sandbox hard-reject, and `satelle agents` vs `satelle agent` (sty_aa726901)

## [0.0.343] - 2026-07-29

### Added
- Codex as a first-class agent transport: preferred ACP spawn (`DefaultCodexACPCommand` via `@agentclientprotocol/codex-acp`) and secondary `codex exec` template (`DefaultCodexExecCommand`); dogfood practice in `satelle help agent-dispatch` (sty_3b4909bb)

### Changed
- `NewRunner("codex")` maps to the exec command template (unmapped stub removed); migrate expands bare `command = "codex"` (sty_3b4909bb)
- Agent validate treats Codex `-s read-only` as reviewer ceiling evidence and hard-rejects `danger-full-access` / `--dangerously-bypass-approvals-and-sandbox` for role=reviewer (sty_3b4909bb)

## [0.0.342] - 2026-07-29

### Fixed
- Seat-refusal message names `satelle story stop-request` for a LIVE seat and `satelle story seat release` only for a STALE seat, so agents no longer cancel a healthy story to free the engagement seat (sty_7b69954a)

### Changed
- Operator preemption is a named park reason on the existing `blocked` state (`preempted-by:<id>` free-form tag) — no new `hold` lifecycle state (sty_7b69954a)
- `satelle-recognise-blockage` covers preemption as a separate case from blockage and forbids cancel-to-free-seat (sty_7b69954a)
- `satelle-story-cancel-review` hard-rejects seat-contention justifications; `satelle-story-blocked-review` accepts preemption parks (sty_7b69954a)

## [0.0.341] - 2026-07-28

### Added
- `satelle-executor-deliverables` principle promoted to the embedded system default (ondemand residency): executors are told required outcome artifacts, never gate-judging criteria; clean-init discoverability covered by an integration test (sty_fd4c3466)

### Changed
- Repo-local `.satelle/principles/satelle-executor-deliverables.md` removed so the embedded default is the single source (same shape as other binary-only principles); no second copy remains to diverge (sty_fd4c3466)

## [0.0.340] - 2026-07-28

### Fixed
- Coded-check gate nodes no longer advertise a live `agent=` binding: `format-drift` reports `inert_coded_check_agent` when a scoped node (or non-default edge agent) names a skill with a ```check fence, and `workflow refresh` strips/rewrites it consultatively; embedded baseline `estimate` and substrate `subcheck` omit `agent=`; `structure.CheckCommand` is the single coded-check definition (`skillCheck` delegates) (sty_4cebc624 / epic:coded-check-gate-honesty)

## [0.0.339] - 2026-07-28

### Fixed
- ACP dispatch no longer persists pre-tool agent narration into step summaries and other prose artifacts: capture segments on `tool_call`/`tool_call_update` and keeps the last `agent_message` run by default; verdict path keeps full capture so parseDecision still finds a decision before trailing chatter (sty_844b6ab1)

## [0.0.338] - 2026-07-27

### Changed
- `satelle-story-classification`: `order:<N>` is the position in the sprint only (not per-epic / per-grouping); non-durable; epic members keep consecutive sprint slots; `epic-parent` carries no order (sty_0656d3c6)

## [0.0.337] - 2026-07-27

### Added
- Step-summary and scoped judge nodes may allocate a named `role=reviewer` binding (`agent=<name>`); `Summarise` uses that binding's harness/model (default remains `[reviewer]`) so high-frequency step recaps can run on a cheap Grok agent (sty_8ee40f94)

### Fixed
- Named judge agents on the step-summary node or scoped `on=` gates no longer misclassify as performing lifecycle states, executor augmentations, or phantom `from="*"` park edges (sty_8ee40f94)

## [serve-v0.0.11] - 2026-07-27

### Fixed
- Workflow diagram footnote routing treats the step-summary skill (not only literal `agent=reviewer`) so a cheap named summariser stays off the main flow (sty_8ee40f94)

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
