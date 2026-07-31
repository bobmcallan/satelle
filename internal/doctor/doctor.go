// Package doctor answers one question — is satelle ready to govern this
// repository? — by COMPOSING the validation authorities that already exist
// (sty_e9da28e2).
//
// It decides nothing new. Every rule it reports comes from an existing owner:
// `agentvalidate` for bindings, profile resolution, workflow allocations and
// lifecycle hooks; `structure` for authored-substrate contracts;
// `agentstep.WorkflowConsistency` for cross-workflow ambiguity; `config` for
// resolution and provenance. Doctor's contribution is composition, classification
// into stable `health` identifiers, and rendering — never a gate, never a repo
// opinion. A rule that lived only here could not be changed without a binary
// release, which is exactly what the constitution forbids.
//
// The deterministic pass makes no paid or network model call. Live provider
// probes are opt-in (see live.go) and bounded.
package doctor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/agentstep"
	"github.com/bobmcallan/satelle/internal/agentvalidate"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/health"
	"github.com/bobmcallan/satelle/internal/structure"
)

// DefaultLiveTimeout bounds ONE live provider probe.
type Opts struct {
	// RepoRoot is the repository being checked; DataDir is its authored plane.
	// An empty DataDir is derived as <RepoRoot>/.satelle.
	RepoRoot string
	DataDir  string
	// Live opts into bounded provider probes. Off by default: ordinary doctor
	// spawns no provider and makes no network call.
	Live bool
	// LiveTimeout bounds one probe; zero means DefaultLiveTimeout.
	LiveTimeout time.Duration
	// ScaffoldDrift reports deployed-vs-canonical harness scaffold mismatches.
	// INJECTED rather than imported: the canonical wrapper bodies live beside the
	// code that writes them (internal/cli), and doctor must not import cli. Nil
	// means the scaffold check is skipped — a caller that cannot supply the
	// authority gets no scaffold findings rather than a wrong verdict.
	ScaffoldDrift func(repoRoot string) health.Findings
	// probe overrides the live prober in tests. Nil uses the real one.
	probe func(ctx context.Context, g agentvalidate.Grant, timeout time.Duration) health.Findings
}

// DefaultLiveTimeout is the per-probe deadline when Opts.LiveTimeout is zero.
const DefaultLiveTimeout = 20 * time.Second

// Report is one repository's health.
type Report struct {
	Repo     string                         `json:"repo"`
	OK       bool                           `json:"ok"`
	Findings health.Findings                `json:"findings"`
	Grants   []agentvalidate.Grant          `json:"grants,omitempty"`
	Gates    []agentvalidate.GateAllocation `json:"gates,omitempty"`
	Sources  map[string]map[string]string   `json:"sources,omitempty"`
	Env      map[string][]EnvKey            `json:"env,omitempty"`
	Live     bool                           `json:"live"`
}

// EnvKey names one binding environment key and whether it resolved. VALUES are
// never carried: an env value may be a credential, and doctor's payload is
// printed, piped, and pasted into issues.
type EnvKey struct {
	Key      string `json:"key"`
	Resolved bool   `json:"resolved"`
}

// resolveDataDir derives the authored plane for a repo root.
func (o Opts) resolveDataDir() string {
	if d := strings.TrimSpace(o.DataDir); d != "" {
		return d
	}
	return filepath.Join(o.RepoRoot, config.DefaultDataDir)
}

// Check runs the deterministic readiness pass for one repository, plus the
// opt-in live probes when Opts.Live is set. It never returns an error: an
// unreadable input is itself a finding, because a diagnostic that refuses to
// report is worse than one reporting a defect.
func Check(ctx context.Context, o Opts) Report {
	dataDir := o.resolveDataDir()
	rep := Report{Repo: o.RepoRoot, Live: o.Live}

	// 1. The effective agents layer, resolved against the machine-wide catalog,
	//    with every binding/allocation/hook rule owned by agentvalidate.
	repoAgents, agentsErr := config.LoadAgents(dataDir)
	if agentsErr != nil {
		rep.Findings = append(rep.Findings, health.Error(health.IDAgentsLoad, "Unreadable agents layer",
			fmt.Sprintf("%s/%s: %v", config.DefaultDataDir, config.AgentsConfigName, agentsErr)).
			WithRemediation("fix the file, or delete it and run `satelle init` to reseed the default").
			About(config.DefaultDataDir+"/"+config.AgentsConfigName))
	} else if !fileExists(filepath.Join(dataDir, config.AgentsConfigName)) {
		rep.Findings = append(rep.Findings, health.Error(health.IDAgentsLoad, "Missing agents layer",
			fmt.Sprintf("missing %s/%s — an initialized repo must define its agents layer",
				config.DefaultDataDir, config.AgentsConfigName)).
			WithRemediation("run `satelle init` to seed the default").
			About(config.DefaultDataDir+"/"+config.AgentsConfigName))
	}
	global, globalErr := config.LoadGlobalAgents()
	if globalErr != nil {
		rep.Findings = append(rep.Findings, health.Error(health.IDAgentsProfileBroken, "Unreadable profile catalog",
			globalErr.Error()).
			WithRemediation("fix "+config.GlobalAgentsLabel+"; it is machine-wide EXECUTION configuration only").
			About(config.GlobalAgentsLabel))
	}

	// Two workflow sets, deliberately: ALLOCATION checks judge everything that
	// governs (authored files plus the embedded defaults none of them shadows),
	// while the CONSISTENCY check judges only what the operator authored. An
	// on-disk wildcard workflow legitimately overrides the embedded wildcard
	// baseline, so feeding both to the ambiguity check would report every repo as
	// misconfigured for doing the normal thing.
	authored := WorkflowDocs(dataDir)
	governing := GoverningWorkflows(dataDir)
	resolve := SkillResolver(dataDir)
	vars := RepoVars(dataDir)

	av := agentvalidate.ValidateEffective(repoAgents, global, vars, governing)
	rep.Findings = append(rep.Findings, av.Findings...)
	rep.Grants = av.Grants
	rep.Gates = av.Gates
	rep.Sources = av.Provenance
	rep.Env = envKeys(repoAgents, config.LayerVars(global.Vars, vars))

	// 2. Authored substrate contracts, store-free (doctor runs before indexing).
	for _, kind := range []string{"workflows", "skills", "principles", "tasks"} {
		rep.Findings = append(rep.Findings, checkAuthoredDir(kind, filepath.Join(dataDir, kind), resolve)...)
	}

	// 3. Cross-workflow consistency — ambiguity, unresolved gate and HOOK skills.
	for _, p := range agentstep.WorkflowConsistency(authored, resolve) {
		rep.Findings = append(rep.Findings, health.Error(health.IDWorkflowConsistency,
			"Workflow consistency", p).
			WithRemediation("fix the workflow's applies_to or the skill it names"))
	}

	// 4. Required binaries: every isolated binding's executable must be runnable.
	rep.Findings = append(rep.Findings, checkBinaries(av.Grants)...)

	// 5. Hook scaffold integrity (injected authority; skipped when unavailable).
	if o.ScaffoldDrift != nil && strings.TrimSpace(o.RepoRoot) != "" {
		rep.Findings = append(rep.Findings, o.ScaffoldDrift(o.RepoRoot)...)
	}

	// 6. Live probes — opt-in, bounded, and never part of the deterministic pass.
	if o.Live {
		rep.Findings = append(rep.Findings, probeGrants(ctx, o, av.Grants)...)
	}

	rep.OK = rep.Findings.OK()
	return rep
}

// checkBinaries reports whether each isolated binding's executable can be found.
// An in-loop binding runs no subprocess, so it is exempt. Command TEMPLATES are
// parsed to argv[0] only — no expansion, no execution.
func checkBinaries(grants []agentvalidate.Grant) health.Findings {
	var out health.Findings
	seen := map[string]bool{}
	for _, g := range grants {
		if g.Backend == "in-loop" || g.Backend == "invalid" {
			continue
		}
		fields := strings.Fields(g.Command)
		if len(fields) == 0 {
			out = append(out, health.Error(health.IDBinaryMalformed, "Malformed command",
				fmt.Sprintf("agents.toml [%s] command is empty — no executable to run", g.Name)).
				About(g.Name).WithRemediation("set a full command template on ["+g.Name+"]"))
			continue
		}
		bin := fields[0]
		if strings.ContainsAny(bin, "{}$") {
			out = append(out, health.Error(health.IDBinaryMalformed, "Malformed command",
				fmt.Sprintf("agents.toml [%s] command starts with %q — the first token must be the executable, not a placeholder", g.Name, bin)).
				About(g.Name).WithRemediation("put the binary first in the ["+g.Name+"] command template"))
			continue
		}
		if seen[bin] {
			continue
		}
		seen[bin] = true
		if _, err := exec.LookPath(bin); err != nil {
			// ADVISORY, not an error. A repo is legitimately initialised before its
			// agent CLI is installed — satelle's gates stay inert until one is, and
			// CI initialises repos with no provider CLI at all. Making this blocking
			// would refuse `satelle init` and every engagement on such a machine for
			// a condition the operator can fix later, and which dispatch already
			// refuses loudly at the moment it actually matters. A MALFORMED command
			// below stays an error: no environment can make it work.
			out = append(out, health.Warn(health.IDBinaryMissing, "Missing executable",
				fmt.Sprintf("agents.toml [%s] needs %q, which is not on PATH", g.Name, bin)).
				About(g.Name).WithRemediation("install "+bin+", or point ["+g.Name+"] at a CLI you have"))
		}
	}
	return out
}

// checkAuthoredDir applies each authored kind's structure contract, returning
// findings instead of printing. It mirrors the loop `satelle validate` runs, so
// init and doctor judge the same files by the same rules.
func checkAuthoredDir(kind, dir string, resolve func(string) bool) health.Findings {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil // an absent kind is not a defect; init seeds what it needs
	}
	var out health.Findings
	for _, e := range entries {
		fn := e.Name()
		if e.IsDir() || !strings.HasSuffix(fn, ".md") {
			continue
		}
		if kind == "tasks" && !strings.HasPrefix(fn, "tsk_") {
			continue
		}
		name := strings.TrimSuffix(fn, ".md")
		if reservedKeepFile(fn) {
			continue
		}
		body, rerr := os.ReadFile(filepath.Join(dir, fn))
		if rerr != nil {
			out = append(out, health.Error(health.IDWorkflowStructure, "Unreadable substrate",
				fmt.Sprintf("%s/%s — read: %v", kind, name, rerr)).About(kind+"/"+name))
			continue
		}
		var problems []string
		switch kind {
		case "documents":
			if derr := docindex.OKFConformance(name, string(body)); derr != nil {
				problems = []string{derr.Error()}
			}
		case "tasks":
			problems = structure.CheckTask(string(body))
		default:
			problems = structure.Doc(kind, name, string(body), resolve)
		}
		for _, p := range problems {
			out = append(out, health.Error(health.IDWorkflowStructure, "Substrate structure",
				fmt.Sprintf("%s/%s — %s", kind, name, p)).
				About(kind+"/"+name).WithRemediation("fix the authored file under .satelle/"+kind))
		}
	}
	return out
}

// envKeys lists each binding's environment KEY NAMES with whether the value
// resolved. Values never appear — an env value may be a credential, and this
// payload is printed and pasted.
func envKeys(agents config.AgentsConfig, vars map[string]string) map[string][]EnvKey {
	out := map[string][]EnvKey{}
	add := func(section string, b config.AgentBinding) {
		if len(b.Env) == 0 {
			return
		}
		keys := make([]EnvKey, 0, len(b.Env))
		for _, k := range sortedKeys(b.Env) {
			_, err := config.ExpandVars(b.Env[k], vars)
			keys = append(keys, EnvKey{Key: k, Resolved: err == nil})
		}
		out[section] = keys
	}
	add("executor", agents.Executor)
	add("reviewer", agents.Reviewer)
	for _, name := range sortedBindingNames(agents.Agents) {
		add(name, agents.Agents[name])
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// WorkflowDocs reads deployed workflow files as docs — store-free, because
// doctor must work on a repo that has never been indexed. Exported so init and
// its tests read the deployed set through the SAME function doctor judges, not a
// second copy that could drift.
func WorkflowDocs(dataDir string) []docindex.Doc {
	dir := filepath.Join(dataDir, "workflows")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var docs []docindex.Doc
	for _, e := range entries {
		fn := e.Name()
		if e.IsDir() || !strings.HasSuffix(fn, ".md") || reservedKeepFile(fn) {
			continue
		}
		body, rerr := os.ReadFile(filepath.Join(dir, fn))
		if rerr != nil {
			continue
		}
		docs = append(docs, docindex.Doc{Kind: "workflows", Name: strings.TrimSuffix(fn, ".md"), Body: string(body)})
	}
	return docs
}

// GoverningWorkflows is the set of workflows that actually GOVERN this repo: the
// authored files on disk, plus the binary's embedded defaults that no on-disk
// file shadows by name.
//
// Reading only the authored dir would leave a repo governed by an embedded
// default with no allocation or lifecycle-hook checking at all — doctor would
// report it healthy while never having looked at the workflow that runs. The
// same union is what the resolution surfaces see once the substrate is indexed;
// doctor computes it store-free so it also works before the first reindex.
func GoverningWorkflows(dataDir string) []docindex.Doc {
	docs := WorkflowDocs(dataDir)
	onDisk := map[string]bool{}
	for _, d := range docs {
		onDisk[d.Name] = true
	}
	for _, e := range config.EmbeddedDefaults() {
		if e.Kind != "workflows" || onDisk[e.Name] {
			continue
		}
		docs = append(docs, docindex.Doc{Kind: "workflows", Name: e.Name, Body: e.Body})
	}
	return docs
}

// SkillResolver reports whether a skill resolves on disk or as an embedded
// default (the virtual sparse defaults rule). Exported for the same reason as
// WorkflowDocs.
func SkillResolver(dataDir string) func(string) bool {
	emb := map[string]bool{}
	for _, d := range config.EmbeddedDefaults() {
		if d.Kind == "skills" {
			emb[d.Name] = true
		}
	}
	return func(skill string) bool {
		if fileExists(filepath.Join(dataDir, "skills", skill+".md")) {
			return true
		}
		return emb[skill]
	}
}

// RepoVars loads the repo's [vars] KV (with its gitignored overlay).
func RepoVars(dataDir string) map[string]string {
	cfg, _, err := config.Load(filepath.Join(dataDir, config.ConfigName))
	if err != nil {
		return nil
	}
	return cfg.Vars
}

// reservedKeepFile mirrors the authored-dir convention: a README is a keep-file,
// not substrate to validate.
func reservedKeepFile(fn string) bool {
	switch strings.ToLower(fn) {
	case "readme.md", ".gitkeep", ".keep":
		return true
	}
	return false
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sortStrings(out)
	return out
}

func sortedBindingNames(m map[string]config.AgentBinding) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sortStrings(out)
	return out
}
