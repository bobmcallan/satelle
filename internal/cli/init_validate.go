// The init deployment validation (sty_d0d6bb67): `satelle init` ends by PROVING
// the deployed system green, not just intending it — the agents layer must load
// (and its reviewer harness resolve), every deployed substrate artifact must pass
// its deterministic structure check, and the workflow set must be consistent. A
// repo that fails here would refuse to run, so init surfaces it immediately and
// exits non-zero. Deterministic and store-free: it reads the files init just
// wrote (or preserved), matching what `satelle <noun> validate` reports once the
// substrate is indexed.

package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/bobmcallan/satelle/internal/agentstep"
	"github.com/bobmcallan/satelle/internal/agentvalidate"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/docindex"
)

// validateDeployment validates the deployed system under dataDir and returns an
// error (init exits non-zero) when it does not validate green.
func validateDeployment(out io.Writer, dataDir string) error {
	fmt.Fprintln(out, "\nValidating the deployed system:")
	failed := 0

	// The agents layer: present, parseable, and every binding + workflow agent=
	// allocation validates green (shared with `satelle agent validate` and the
	// engage precondition — sty_93eec36d). The runtime refuses to run without a
	// loadable agents.toml (requireAgents), so init must not report success while
	// leaving the repo unrunnable.
	agentsRel := config.DefaultDataDir + "/" + config.AgentsConfigName
	wfDocs := deployedWorkflowDocs(dataDir)
	if _, statErr := os.Stat(filepath.Join(dataDir, config.AgentsConfigName)); os.IsNotExist(statErr) {
		failed++
		if _, lerr := os.Stat(filepath.Join(dataDir, config.ActorsConfigName)); lerr == nil {
			fmt.Fprintf(out, "FAIL  %s — missing; the retired %s is present — rename it to %s\n",
				agentsRel, config.ActorsConfigName, config.AgentsConfigName)
		} else {
			fmt.Fprintf(out, "FAIL  %s — missing (the agents layer must be defined)\n", agentsRel)
		}
	} else if agents, lerr := config.LoadAgents(dataDir); lerr != nil {
		failed++
		fmt.Fprintf(out, "FAIL  %s — %v\n", agentsRel, lerr)
	} else {
		report := agentvalidate.Validate(agents, deployedVars(dataDir), wfDocs)
		for _, w := range report.Warnings {
			fmt.Fprintf(out, "WARN  %s — %s\n", agentsRel, w)
		}
		for _, p := range report.Problems {
			failed++
			fmt.Fprintf(out, "FAIL  %s — %s\n", agentsRel, p)
		}
		if report.OK() {
			fmt.Fprintf(out, "PASS  %s\n", agentsRel)
		}
	}

	// Structure checks per deployed kind. Skills resolve from disk OR embedded
	// defaults (virtual sparse defaults — sty_29e5a9a5).
	resolve := skillResolves(dataDir)
	for _, kind := range []string{"workflows", "skills", "principles", "tasks"} {
		_, f, _ := validateAuthoredDir(out, kind, filepath.Join(dataDir, kind), "", resolve)
		failed += f
	}

	// Cross-workflow consistency over the deployed set (ambiguous applies_to,
	// unresolved referenced skills) — the whole-set check `satelle workflow
	// validate` runs.
	for _, p := range agentstep.WorkflowConsistency(wfDocs, resolve) {
		failed++
		fmt.Fprintf(out, "FAIL  workflows (consistency) — %s\n", p)
	}

	// Substrate analysis: placement/residency/tag-axis (fatal), unedited
	// constitution + config drift (advisory). Print advisory WARN lines even when
	// fatals will fail the init, so they stay visible.
	repoRoot := filepath.Dir(dataDir)
	if filepath.Base(dataDir) != config.DefaultDataDir {
		// dataDir may be absolute ".../.satelle"; parent is the repo root.
		repoRoot = filepath.Dir(dataDir)
	}
	defects := analyzeSubstrate(dataDir, repoRoot)
	failed += reportSubstrateAnalysis(out, defects)

	if failed > 0 {
		return fmt.Errorf("init: the deployed system failed validation (%d problem(s)) — fix the reported files and re-run `satelle init`", failed)
	}
	fmt.Fprintln(out, "PASS  deployed system validates green")
	return nil
}

// skillResolves reports whether a skill name is available as an on-disk file or
// as an embedded default (sty_29e5a9a5 virtual sparse defaults).
func skillResolves(dataDir string) func(skill string) bool {
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

// deployedVars loads the [vars] KV from the deployed satelle.toml (overlaid by the
// gitignored satelle.local.toml) for the init-time env-resolution check. A missing
// or unreadable config yields a nil map — the seeded default declares no env, so
// resolution is a no-op then (sty_001558ce).
func deployedVars(dataDir string) map[string]string {
	cfg, _, err := config.Load(filepath.Join(dataDir, config.ConfigName))
	if err != nil {
		return nil
	}
	return cfg.Vars
}

// deployedWorkflowDocs reads the deployed workflow files under dataDir as
// docindex.Docs for the consistency check — store-free (init runs before
// anything is indexed). Reserved keep-files are skipped.
func deployedWorkflowDocs(dataDir string) []docindex.Doc {
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
