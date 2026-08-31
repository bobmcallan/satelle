package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/agentstep"
	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/docstory"
	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/logfile"
	"github.com/bobmcallan/satelle/internal/oplog"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// storeAnnotation marks a command as needing the local store. The root's
// persistent pre-run opens the bootstrap (config + db) only for these commands
// and closes it after — so `satelle version` / `--help` never create a db.
const storeAnnotation = "needs-store"

// storeOptionalAnnotation marks a store-backed command that must ALSO be
// runnable from a directory satelle does not govern — because what it reports is
// not about the current repo. `satelle doctor --all` is the case: it enumerates
// the workspace registry, so refusing it in an ungoverned cwd would make the
// estate un-inspectable from anywhere but a satelle repo, and the upgrade
// guidance names it as the blind-safe thing to run (sty_0f471251).
//
// Only ErrNotInitialised is tolerated. Any other bootstrap failure still stops
// the command — this widens WHERE a command may run, never what it ignores.
const storeOptionalAnnotation = "store-optional"

// needsStoreOptional flags a store-backed command that tolerates an ungoverned
// working directory. The command MUST handle a nil app.
func needsStoreOptional() map[string]string {
	return map[string]string{storeAnnotation: "1", storeOptionalAnnotation: "1"}
}

// appCtxKey carries the opened *app.App on the command context.
type appCtxKey struct{}

// uiDrainCtxKey carries the per-invocation UI push drain (sty_9ba3d709).
type uiDrainCtxKey struct{}

// needsStore returns a cobra annotations map flagging a store-backed command.
func needsStore() map[string]string { return map[string]string{storeAnnotation: "1"} }

// openAppForCmd opens the bootstrap and stashes it on the command's context.
// Called from the root's PersistentPreRunE for store-backed commands.
func openAppForCmd(cmd *cobra.Command) error {
	a, err := app.Open()
	if err != nil {
		// An ungoverned repo is an operator condition, not a bootstrap fault:
		// return the actionable line bare rather than wrapped in `bootstrap:`
		// noise (sty_20a7824c). Non-zero, deliberately — this is the same class
		// of refusal these verbs already returned via requireAgents, so scripts
		// branching on a satelle verb keep their current semantics.
		if errors.Is(err, app.ErrNotInitialised) {
			return err
		}
		return fmt.Errorf("bootstrap: %w", err)
	}
	// Drift / breaking-surface gate: a deployed repo behind a breaking binary
	// release fails closed and names `satelle init` as the heal path.
	// `restore` is a heal command and must not sit behind the stamp gate it
	// heals (sty_a9ec33e7) — keep confirmation; keep other store verbs gated.
	if cmd.Name() != "restore" {
		if derr := refuseBreakingDrift(a.RepoRoot); derr != nil {
			_ = a.Close()
			return derr
		}
	}
	// Lazy harness install (epic:minimal-harness-footprint): if this process is
	// inside a Claude/Grok session and the repo lacks that harness scaffold,
	// install it now (idempotent). First session of a new harness may still
	// have run without hooks; next session picks them up.
	ensureLazySessionHarness(a.RepoRoot)
	// Scaffold drift (sty_ac25b787): deployed harness wrappers behind the binary
	// fail closed for store-backed verbs — hash mechanism, not ### Breaking.
	// `status` is exempt so it can REPORT the drift (AC3); heal is still init.
	if cmd.Name() != "status" {
		if derr := refuseScaffoldDrift(a.RepoRoot); derr != nil {
			_ = a.Close()
			return derr
		}
	}
	// Broken configuration refuses to run (sty_d0d6bb67): an initialized repo
	// (this command reached the store, so .satelle exists) must carry a loadable
	// agents layer — no silent fallback to compiled defaults. `satelle init` is
	// not store-backed, so a fresh repo still bootstraps and (re)seeds the file.
	eff, err := requireAgents(a)
	if err != nil {
		_ = a.Close()
		return err
	}
	agents := eff.Agents
	// Wire the opened stores into the verb registry — the single seam both the
	// CLI and the web server dispatch through. The CLI is one-shot, so wiring
	// the package globals per invocation is correct.
	verb.SetWorkItemStore(a.Store.Stories)
	verb.SetLedgerStore(a.Store.Ledger)
	verb.SetTxRunner(a.Store.InTx)
	verb.SetDocIndexStore(a.Store.DocIndex)
	verb.SetAuthoredDirs(a.AuthoredDirs())
	verb.SetSubstrateConfigDir(a.Config.ResolveDataDir(a.RepoRoot))
	verb.SetLeaseStore(a.Store.Leases)
	// UI push drain (sty_9ba3d709 / sty_126228b2 / sty_21a7d16d): machine
	// [service] endpoint (env > config > derived localhost:port). SATELLE_SERVER_ENDPOINT=none
	// disables push (hermetic tests). Clear first so a prior test/process state
	// cannot leak sinks into this one-shot invocation.
	verb.SetChangeNotifier(nil)
	var drain *uiDrain
	var serveEP string
	if gc, gerr := config.LoadGlobal(); gerr == nil {
		serveEP = gc.Service.ResolveEndpoint()
	}
	if ep := serveEP; ep != "" {
		drain = &uiDrain{
			endpoint: ep,
			repoKey:  config.RepoKey(a.RepoRoot),
			app:      a,
		}
		verb.AddChangeNotifier(drain.mark)
	}
	// Stories attachments are RUNTIME state (home-keyed under RuntimeDir —
	// sty_4660bbe1). The database is the sole story store (markdown mirror
	// removed, sty_fa1e02e1); this dir holds plan/step-summary attachments only.
	verb.SetStoryDir(filepath.Join(a.RuntimeDir, "stories"))
	// Authored root (documents/, workflows/, …) — needed when a verb spans both
	// planes (e.g. migrateLegacySummaries moves docs → runtime stories/).
	verb.SetDataDir(a.DataDir)
	// Archive-retention policy for the closed-story attachment dirs — a no-op
	// unless satelle.toml sets a count/age policy (sty_aba7200c).
	verb.SetStoryRetention(a.Config.StoriesKeepClosed, a.Config.StoriesKeepDays)
	// Binary attachment cap + content-type allowlist (sty_40e5a305): enforced
	// in the verb so CLI and any future hosted/MCP caller share one rule.
	verb.SetAttachmentPolicy(a.Config.ResolveAttachmentMaxBytes(), a.Config.ResolveAttachmentAllowTypes())
	// Backups root is also runtime (sibling of stories/, not of tasks/).
	verb.SetBackupsDir(filepath.Join(a.RuntimeDir, "backups"))
	// Seat concurrency mode (sty_c098dc2d): [engagement] parallel selects the
	// arbitration KEY a story claims the engagement seat under — "none" (the
	// default, and what this repo has always enforced: one performing story) or
	// "epic" (sibling children of one epic, one working tree per lease). Unwired
	// is "none", so a repo with no [engagement] section is unaffected.
	verb.SetEngagementMode(a.Config)
	// Controlled tag vocabulary (sty_034d843c): validate namespaces declared in
	// satelle.toml [tags.vocabulary] at story/task create and set. Independent of
	// the agent CLI — must work with no harness installed.
	verb.SetTagVocabulary(a.Config)
	// Assignee holder (sty_8ccaa906): local credstore PrincipalID only — no
	// hosted.Client.Me, no network. Empty server or missing credential means
	// unassigned (offline team-of-1).
	verb.SetAssigneeResolver(func() string {
		server := config.ResolveHostedServer(a.Config)
		if server == "" {
			return ""
		}
		cred, err := (hosted.FileStore{}).Load(server)
		if err != nil {
			return ""
		}
		return cred.PrincipalID
	})
	// Engage precondition (sty_93eec36d): agents.toml + workflow agent= validation
	// before a story leaves its entry state. agents already loaded by requireAgents.
	// Vars are the LAYERED KV (machine-wide catalog [vars] under the repo's own,
	// repo keys winning — sty_c7dfeedf), so a profile that references ${SECRET}
	// resolves from the operator's machine without the value entering the repo.
	verb.SetAgentsConfig(agents, eff.Vars)
	// A task, unlike a story, IS authored substrate: its <data_dir>/tasks/tsk_*.md
	// work-definition file is the source of truth and the store is its index
	// (sty_c1f9e74c). Wire the dir so create/set materialise the file and `reindex`
	// ingests it. MUST stay on DataDir — never derive from DBPath (runtime plane).
	verb.SetTaskDir(filepath.Join(a.DataDir, "tasks"))
	// Wire the flat-file operation log (runtime logs/operations.log): a plain-text
	// mirror of state-mutating verbs that a read-only reviewer can scan to verify a
	// DB change the SQLite store hides from it (sty_be257fef).
	verb.SetOpLog(oplog.New(a.RuntimeDir, logRotation(a)))
	// Wire the isolated reviewer that gates status transitions. The agent CLI is
	// the install-time choice (global config); the gate is inert until a
	// workflow names a reviewer skill whose rubric is installed.
	if gc, gerr := config.LoadGlobal(); gerr == nil {
		if runner, rerr := agentcli.NewRunner(gc.Agent.ResolveCLI()); rerr == nil {
			rev := agentstep.New(runner, a.Store.DocIndex, a.RepoRoot, "")
			rev.SetLogDir(filepath.Join(a.RuntimeDir, "logs"), logRotation(a))
			// A gated transition legitimately blocks for minutes while the nested
			// reviewer runs — emit progress to stderr so it is visibly distinct from
			// a hang (sty_6c88ca10). stderr keeps stdout's JSON payload clean.
			rev.SetProgress(func(msg string) { fmt.Fprintln(os.Stderr, msg) })
			// Queryable gate progress on the engagement lease (sty_598a8e1b).
			// Best-effort: a SetActivity failure must never fail a transition.
			leases := a.Store.Leases
			rev.SetActivity(func(itemID string, act agentstep.Activity) {
				if leases == nil || itemID == "" {
					return
				}
				_ = leases.SetActivity(context.Background(), itemID, act.Label, act.Index, act.Total)
			})
			if aerr := applyAgentGrants(rev, a, agents); aerr != nil {
				_ = a.Close()
				return aerr
			}
			rev.SetChildrenResolver(childrenResolver(a))
			// Attachment payload injection (sty_58fa970e): Bash-less reviewers
			// receive plan/step-summary bodies in the transition payload.
			rev.SetDocsResolver(docsResolver(a))
			// Prior-verdict injection (sty_0f5e600c): a re-reviewed edge carries
			// what it already judged, so the gate judges the delta.
			rev.SetPriorVerdictsResolver(priorVerdictsResolver())
			rev.SetArtifactAttacher(verb.AttachItemDoc)
			// Structured retry/failure/timeout telemetry (sty_b73c3236): the engine
			// sees each dispatch ATTEMPT (a killed/timed-out subprocess) the verb
			// layer never does, so it writes those events itself via this sink.
			rev.SetTelemetry(func(ctx context.Context, storyID, actor, kind string, data map[string]any) {
				_ = verb.AppendTelemetry(ctx, storyID, actor, kind, data)
			})
			verb.SetTransitionGater(rev)
			// Stamp the governing workflow on every story at create — independent of
			// create-gating (sty_3800ac23).
			verb.SetWorkflowResolver(rev)
			// Named-agent executor dispatch (sty_fd427546): a workflow node's
			// agent=<name> allocation runs that binding's harness at the transition.
			// agents.toml defines WHO, the DOT defines WHERE, the binary only runs it.
			rev.SetNamedAgents(agents.NamedBinding)
			// Rate-limit secondary failover (sty_5bf61f89): per-binding secondary=
			// or [defaults] secondary names a fallback binding for one retry.
			agentsCfg := agents
			rev.SetSecondaryResolver(func(section string, b config.AgentBinding) (config.AgentBinding, string, bool) {
				return agentsCfg.ResolveSecondary(section, b)
			})
			verb.SetExecutorDispatcher(rev)
			// The retrospective dispatcher (sty_b53730e2): `satelle story retrospect`
			// runs the [retrospective] agent over a finished story to file proposals.
			verb.SetRetrospector(rev)
			// The amendment gate (sty_81aa4d8f): `satelle story amend` runs the
			// skill the workflow's amend_review hook declares. Always wired — a repo
			// that declares no such hook still refuses the amendment (the engine
			// returns ungated and the verb treats an unjudged amendment as refused),
			// so wiring it never lifts a freeze on its own.
			verb.SetAmendReviewer(rev)
			// Point a structure-guard refusal at the story the indexer already
			// raised about the document that will not parse (sty_88d40a60). The
			// engine stays store-free; this is the injection.
			storyStore := a.Store.Stories
			rev.SetTrackingStoryResolver(func(ctx context.Context, kind, name string) string {
				return docstory.IDForDoc(docstory.Open(ctx, storyStore.List), kind, name)
			})
			// The summariser recaps gated transitions; inert until gating is active.
			verb.SetStepSummariser(rev)
			// Create-gating is opt-in per repo (satelle.toml [review] gate_create):
			// the rubric ships embedded, but enforcing it is the operator's choice.
			// Always set (or clear) so a prior command that left the package-global
			// wired cannot leak into an ungated repo in the same process.
			if a.Config.Review.GateCreate {
				verb.SetCreateReviewer(rev)
			} else {
				verb.SetCreateReviewer(nil)
			}
		}
	}
	ctx := context.WithValue(cmd.Context(), appCtxKey{}, a)
	if drain != nil {
		ctx = context.WithValue(ctx, uiDrainCtxKey{}, drain)
	}
	cmd.SetContext(ctx)
	return nil
}

// closeAppForCmd drains pending UI pushes (if any), then closes the bootstrap
// stashed on the command context. Safe to call twice (error path + PostRun):
// drain is once-guarded; close clears the app from context.
func closeAppForCmd(cmd *cobra.Command) {
	if cmd == nil {
		return
	}
	// Drain BEFORE close so snapshot build still reads open stores (AC2).
	if d, ok := cmd.Context().Value(uiDrainCtxKey{}).(*uiDrain); ok && d != nil {
		d.flush()
	}
	if a, ok := cmd.Context().Value(appCtxKey{}).(*app.App); ok && a != nil {
		_ = a.Close()
		// Clear so a second closeAppForCmd (ExecuteC cleanup after PostRun) is a no-op.
		cmd.SetContext(context.WithValue(cmd.Context(), appCtxKey{}, (*app.App)(nil)))
	}
}

// appFrom returns the opened *app.App from the command context. It is present
// for any command carrying the storeAnnotation (the pre-run opened it).
func appFrom(cmd *cobra.Command) (*app.App, error) {
	a, ok := cmd.Context().Value(appCtxKey{}).(*app.App)
	if !ok || a == nil {
		return nil, fmt.Errorf("internal: store not initialised for %q", cmd.CommandPath())
	}
	return a, nil
}

// engineForCmd builds a agentstep.Engine over the opened store and the install-time
// agent CLI — the concrete reviewer used by the read paths (the per-noun `satelle <noun> validate`,
// `satelle <object> create`) that need structure verdicts directly.
func engineForCmd(cmd *cobra.Command) (*agentstep.Engine, *app.App, error) {
	a, err := appFrom(cmd)
	if err != nil {
		return nil, nil, err
	}
	gc, err := config.LoadGlobal()
	if err != nil {
		return nil, nil, err
	}
	runner, err := agentcli.NewRunner(gc.Agent.ResolveCLI())
	if err != nil {
		return nil, nil, fmt.Errorf("an agent CLI is required: %w", err)
	}
	rev := agentstep.New(runner, a.Store.DocIndex, a.RepoRoot, "")
	rev.SetLogDir(filepath.Join(a.RuntimeDir, "logs"), logRotation(a))
	// Same prior-verdict injection as the transition gater (sty_0f5e600c): this
	// read-path engine gates `create` and the per-noun validates too. It
	// deliberately wires no docs resolver — that omission is this site's own
	// question, out of scope here.
	rev.SetPriorVerdictsResolver(priorVerdictsResolver())
	eff, err := requireAgents(a)
	if err != nil {
		return nil, nil, err
	}
	if err := applyAgentGrants(rev, a, eff.Agents); err != nil {
		return nil, nil, err
	}
	return rev, a, nil
}

// logRotation builds the shared flat-log rotation config from the repo's resolved
// satelle.toml (logs_max_size_kb / logs_max_files), used for operations.log and
// reviewer.log alike (sty_a67e6e8c).
func logRotation(a *app.App) logfile.Config {
	return logfile.Config{
		MaxSizeBytes: a.Config.ResolveLogsMaxSizeBytes(),
		MaxFiles:     a.Config.ResolveLogsMaxFiles(),
	}
}

// childrenResolver lists a parent's child stories (id + status) from the DB, for
// the container close gate's payload — so a parent/epic close is judged from the
// database, never an on-disk story mirror (sty_fa1e02e1).
func childrenResolver(a *app.App) func(ctx context.Context, parentID string) []agentstep.ChildState {
	return func(ctx context.Context, parentID string) []agentstep.ChildState {
		if parentID == "" {
			return nil
		}
		kids, err := a.Store.Stories.List(ctx, workitem.ListFilter{ParentID: parentID})
		if err != nil {
			return nil
		}
		out := make([]agentstep.ChildState, 0, len(kids))
		for _, k := range kids {
			out = append(out, agentstep.ChildState{ID: k.ID, Status: k.Status})
		}
		return out
	}
}

// docsResolver lists an item's attachments for transition-payload injection
// (sty_58fa970e) — plan/step summaries ride in the payload so Bash-less
// reviewers need no disk path. verb.ItemDocs uses the package-global storyDir
// wired by SetStoryDir above.
func docsResolver(a *app.App) func(ctx context.Context, itemID string) []agentstep.DocState {
	return func(ctx context.Context, itemID string) []agentstep.DocState {
		if itemID == "" {
			return nil
		}
		docs, err := verb.ItemDocs(ctx, itemID)
		if err != nil {
			return nil
		}
		out := make([]agentstep.DocState, 0, len(docs))
		for _, d := range docs {
			out = append(out, agentstep.DocState{
				Name:        d.Name,
				Type:        d.Type,
				Body:        d.Body,
				Binary:      d.Binary,
				ContentType: d.ContentType,
				Size:        d.Size,
				SHA256:      d.SHA256,
			})
		}
		return out
	}
}

// priorVerdictsResolver lists the verdicts already recorded on the edge under
// review for transition-payload injection (sty_0f5e600c), so a re-review judges
// the delta instead of re-reading the artefact with no memory. verb.PriorVerdicts
// reads the package-global ledger store wired above; a read failure degrades to
// no prior verdicts and never fails the transition.
func priorVerdictsResolver() func(ctx context.Context, itemID, from, to string) []agentstep.PriorVerdict {
	return func(ctx context.Context, itemID, from, to string) []agentstep.PriorVerdict {
		if itemID == "" {
			return nil
		}
		verdicts, err := verb.PriorVerdicts(ctx, itemID, from, to)
		if err != nil || len(verdicts) == 0 {
			return nil
		}
		out := make([]agentstep.PriorVerdict, 0, len(verdicts))
		for _, v := range verdicts {
			out = append(out, agentstep.PriorVerdict{
				Skill:     v.Skill,
				Decision:  v.Decision,
				Notes:     v.Notes,
				CreatedAt: v.CreatedAt,
			})
		}
		return out
	}
}

// skillResolver returns a predicate reporting whether a skill name resolves in
// the substrate (project ∪ embedded), for the deterministic workflow structure
// check's executor-skill actionability. Used by the per-noun `satelle <noun> validate` and `reindex`.
func skillResolver(a *app.App) func(skill string) bool {
	return func(skill string) bool {
		_, err := a.Store.DocIndex.Get(context.Background(), "skills", skill)
		return err == nil
	}
}

// requireAgents loads the EFFECTIVE agents layer for an INITIALIZED repo and
// refuses when it is broken (sty_d0d6bb67): a malformed file, or an absent one —
// `satelle init` always seeds it, so absence means a broken deployment, not "use
// compiled defaults". The error names the file and the fix; the compiled defaults
// remain only the pre-init bootstrap (a repo with no .satelle at all never
// reaches this — init is not store-backed).
//
// "Effective" means the repo file folded with the machine-wide profile catalog
// by the documented precedence (sty_c7dfeedf) — resolved through the single
// config.LoadEffectiveAgents site, never merged here. A repo that references no
// profile resolves exactly as before, catalog present or not.
func requireAgents(a *app.App) (config.EffectiveAgents, error) {
	// agents.toml is authored substrate — always under DataDir, never RuntimeDir
	// (sty_4660bbe1: the DB leaving the repo must not take the agents layer with it).
	dataDir := a.DataDir
	if dataDir == "" {
		dataDir = a.Config.ResolveDataDir(a.RepoRoot)
	}
	// AgentsPath prefers the canonical workflows/ location and falls back to the
	// legacy one, so an unconverted repo still runs (sty_10f732ed). The message
	// names the CANONICAL path — where the file belongs, not where it used to be.
	rel := config.DefaultDataDir + "/" + config.AgentsRel
	agentsPath, _ := config.AgentsPath(dataDir)
	if _, err := os.Stat(agentsPath); os.IsNotExist(err) {
		if _, lerr := os.Stat(filepath.Join(dataDir, config.ActorsConfigName)); lerr == nil {
			return config.EffectiveAgents{}, fmt.Errorf(
				"missing %s but found the retired %s/%s — rename it to %s (the legacy filename is no longer loaded)",
				rel, config.DefaultDataDir, config.ActorsConfigName, config.AgentsConfigName)
		}
		return config.EffectiveAgents{}, fmt.Errorf(
			"missing %s — an initialized repo must define its agents layer; run `satelle init` to seed the default", rel)
	}
	eff, err := config.LoadEffectiveAgents(dataDir, a.Config.Vars)
	if err != nil {
		return config.EffectiveAgents{}, fmt.Errorf("broken %s: %w — fix it, or delete it and run `satelle init` to reseed the default", rel, err)
	}
	// Resolve every binding's env ${VAR} against the LAYERED [vars] KV ONCE, here
	// at load — fail-fast, the same style as the broken-file refusal above: an
	// unknown var in any binding refuses the command with an actionable message
	// rather than dispatching an agent with a blank credential later (sty_001558ce).
	resolved, err := config.ResolveAgentEnvs(eff.Agents, eff.Vars)
	if err != nil {
		return config.EffectiveAgents{}, fmt.Errorf("%s: %w", rel, err)
	}
	eff.Agents = resolved
	return eff, nil
}

// applyAgentGrants binds the loaded agents layer onto the engine: the reviewer's
// binding (tools/model/env/principles/role), constitution order-zero, and harness.
// A broken harness value is an error — the configuration executes as defined or
// refuses (sty_d0d6bb67).
func applyAgentGrants(rev *agentstep.Engine, a *app.App, agents config.AgentsConfig) error {
	rb := agents.ReviewerBinding()
	// Store the whole binding as the single resolution shape for Invoke
	// (sty_ba860c8a); scalar caches are synced from it.
	rev.SetReviewerBinding(rb)
	// Project constitution rides order-zero in isolated briefings whenever
	// principles ≠ none (design §5.3) — SessionStart parity with cmd_hook.
	if a != nil {
		rev.SetConstitution(readConstitution(a.Config.ResolveConstitution(a.RepoRoot)))
	}
	// Select the reviewer's agent CLI from the agents-layer command binding
	// (default claude). An unset/in-loop command keeps the global [agent] cli
	// configured at construction; an unresolvable one refuses.
	r, err := agentcli.RunnerFromBinding(rb.ResolvedInterface(), rb.CommandTemplate())
	if err != nil {
		return fmt.Errorf("broken %s/%s: reviewer command: %w",
			config.DefaultDataDir, config.AgentsRel, err)
	}
	if r != nil {
		rev.SetRunner(r)
	}
	return nil
}
