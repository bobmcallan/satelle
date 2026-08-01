// `satelle migrate` — one deterministic verb converging a repo to the current
// substrate-planes structure (sty_a3915840 / epic:substrate-planes). Composes
// existing mechanism: home-keyed runtime relocation, legacy residue removal,
// unedited-seed prune, gitignore managed-block convergence, then deployment
// validation. Dry-run by default; --yes applies.
package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/wfdot"
	"github.com/bobmcallan/satelle/internal/wfgovern"
)

func init() {
	var yes, allowLive bool
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Converge this repo to the current satelle structure (dry-run default)",
		Long: `migrate composes the structural upgrade steps for substrate-planes:

  1. runtime relocation   — copy legacy .satelle/satelle.db (+ logs/backups/stories)
                            into ~/.satelle/<repo-key>/ (non-destructive)
  2. legacy residue       — remove in-repo runtime leftovers once home-keyed
                            (satelle.db*, logs/, backups/, stories/ — including
                            attachment residue recreated under .satelle/stories/,
                            agents.*.bak)
  3. substrate prune      — remove unedited embedded-default seed copies
  4. gitignore converge   — rewrite the managed .gitignore block to the current form
  5. config converge      — append ".gitignore" to [gate] edit_exempt_paths when a
                            non-empty list predates the managed-output exemption
                            (operator additions kept; empty list left alone)
  6. validate             — deployed-system check (same as end of satelle init)

Dry-run by default: prints the full plan and applies nothing. Pass --yes to apply.
Idempotent: a converged repo reports "already on current structure".

LIVE RUNTIME (sty_5308eb60): if the legacy DB holds a fresh engagement lease
or a satelle serve is responding on this repo's web port, migrate REFUSES to
relocate/remove unless --allow-live is set. Relocating under a live session
strands every write made after the VACUUM INTO snapshot. Prefer waiting for
the session to park/finish (satelle story seat) and stopping serve.

Edited/authored substrate is never touched. Runtime migrate leaves the legacy
DB in place until residue removal (only after a successful home-keyed copy).
See decision-substrate-planes-local-first and sty_a3915840 / sty_f115e6bf.
` + migrateSplitBrainHelp,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, cfgPath, err := config.Load("")
			if err != nil && err != config.ErrNotFound {
				return err
			}
			repoRoot := "."
			if cfgPath != "" {
				repoRoot = config.RepoRootFromConfigPath(cfgPath)
			} else if wd, e := os.Getwd(); e == nil {
				repoRoot = wd
			}
			dataDir := cfg.ResolveDataDir(repoRoot)
			a := &app.App{
				Config:     cfg,
				RepoRoot:   repoRoot,
				DataDir:    dataDir,
				RuntimeDir: cfg.ResolveRuntimeDir(repoRoot).Dir,
				DBPath:     cfg.ResolveDB(repoRoot),
			}
			return runMigrate(cmd.OutOrStdout(), a, yes, allowLive)
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "apply the migration (default is dry-run)")
	cmd.Flags().BoolVar(&allowLive, "allow-live", false, "UNSAFE: relocate even when a live engagement lease or satelle serve is detected (strands post-snapshot writes)")
	register(cmd)
}

// migratePlan is the pure plan for one repo (no IO beyond what planning needs).
type migratePlan struct {
	RuntimeRelocate bool     // need runtime migrate (legacy DB present, home not yet)
	Residue         []string // dataDir-relative paths/globs to remove
	PruneSeeds      []string // dataDir-relative unedited seed paths
	Gitignore       bool     // managed block needs converge
	ExemptGitignore bool     // [gate] edit_exempt_paths needs .gitignore (sty_f115e6bf)
	// WorkflowRetire lists DOT workflow files superseded by an authored,
	// PARSEABLE done.md + step.md (sty_9835070d). Actionable work: migrate
	// removes them.
	WorkflowRetire []string
	// WorkflowConvertPending lists DOT workflow files present while the route
	// source is absent or does not parse. A NOTICE, never work: migrate cannot
	// author a route, because deriving obligations from a graph is interpretation
	// that has to be authored and reviewed. It is deliberately outside empty() —
	// nothing here is actionable — but it is never silent either, because
	// "already on current structure" would then read as "nothing to do" about a
	// conversion that is outstanding.
	WorkflowConvertPending []string
}

func planMigrate(cfg config.Config, repoRoot, dataDir string) migratePlan {
	p := migratePlan{}
	legacyDB := filepath.Join(dataDir, config.DefaultDBName)
	homeDB := filepath.Join(config.GlobalDir(), config.RepoKey(repoRoot), config.DefaultDBName)
	if fileExists(legacyDB) && !fileExists(homeDB) {
		p.RuntimeRelocate = true
	}
	// Residue is scheduled only when the home-keyed ledger exists or will after
	// relocate — never when removing would destroy the only DB copy.
	if fileExists(homeDB) || p.RuntimeRelocate {
		p.Residue = listLegacyResidue(dataDir)
	}
	p.PruneSeeds = listUneditedSeeds(dataDir)
	p.Gitignore = gitignoreNeedsConverge(repoRoot)
	p.ExemptGitignore = editExemptGitignoreNeedsConverge(dataDir)
	p.WorkflowRetire, p.WorkflowConvertPending = workflowConversionState(dataDir)
	return p
}

// workflowConversionState splits the on-disk DOT workflows into the ones a
// working route source supersedes (retire) and the ones still waiting for one
// (pending). Exactly one of the two is ever non-empty.
//
// The route source must PARSE before anything is retired: deleting the only
// remaining lifecycle because a half-authored done.md happened to be on disk is
// the one failure this step must not have.
func workflowConversionState(dataDir string) (retire, pending []string) {
	wfDir := filepath.Join(dataDir, "workflows")
	entries, err := os.ReadDir(wfDir)
	if err != nil {
		return nil, nil
	}
	var dots []string
	var doneBody, stepBody string
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".md") ||
			strings.EqualFold(e.Name(), "README.md") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(wfDir, e.Name()))
		if err != nil {
			continue
		}
		switch strings.TrimSuffix(e.Name(), filepath.Ext(e.Name())) {
		case wfgovern.RouteSourceDone:
			doneBody = string(body)
			continue
		case wfgovern.RouteSourceStep:
			stepBody = string(body)
			continue
		}
		// Textual, because there is no DOT parser left to ask (sty_d953c5d8).
		// migrate is the one place that must still RECOGNISE a graph: it is what
		// tells an unconverted repo what it owes.
		if strings.Contains(string(body), "```dot") {
			dots = append(dots, e.Name())
		}
	}
	if len(dots) == 0 {
		return nil, nil
	}
	sort.Strings(dots)
	if doneBody == "" || stepBody == "" {
		return nil, dots
	}
	if _, err := wfdot.ParseDone(doneBody); err != nil {
		return nil, dots
	}
	if _, err := wfdot.ParseSteps(stepBody); err != nil {
		return nil, dots
	}
	return dots, nil
}

// printConversionPending states an outstanding conversion wherever migrate
// reports, including the converged path — silence there would read as "nothing
// to do" about work that has not been done.
func printConversionPending(out io.Writer, pending []string) {
	if len(pending) == 0 {
		return
	}
	fmt.Fprintf(out, "\nworkflow conversion OUTSTANDING — %d DOT workflow(s) and no route source:\n", len(pending))
	for _, f := range pending {
		fmt.Fprintf(out, "  - workflows/%s\n", f)
	}
	fmt.Fprintln(out, "  until they are converted this repo REFUSES transitions rather than running them ungated.")
	fmt.Fprintln(out, "  read `satelle help workflow-convert` — it maps each node, edge gate and park/cancel sink")
	fmt.Fprintln(out, "  onto the two files, then author workflows/done.md (a `## <category>` section per category,")
	fmt.Fprintln(out, "  its obligations in order) and workflows/step.md (a `## <step>` per status, a `## gate <skill>`")
	fmt.Fprintln(out, "  per always-on gate), and re-run migrate to retire the graphs. migrate does not derive them:")
	fmt.Fprintln(out, "  turning a graph into obligations is interpretation, and it is authored and reviewed.")
}

func (p migratePlan) empty() bool {
	return !p.RuntimeRelocate && len(p.Residue) == 0 && len(p.PruneSeeds) == 0 &&
		!p.Gitignore && !p.ExemptGitignore && len(p.WorkflowRetire) == 0
}

func runMigrate(out io.Writer, a *app.App, yes, allowLive bool) error {
	cfg := a.Config
	repoRoot := a.RepoRoot
	dataDir := a.DataDir
	if dataDir == "" {
		dataDir = cfg.ResolveDataDir(repoRoot)
	}
	plan := planMigrate(cfg, repoRoot, dataDir)

	if plan.empty() {
		fmt.Fprintln(out, "already on current structure")
		printConversionPending(out, plan.WorkflowConvertPending)
		return nil
	}

	// Live-runtime check before any plan print that implies apply is safe
	// (sty_5308eb60). Covers relocate AND residue: a live session writing the
	// legacy DB must not race residue removal even when home already exists.
	legacyDB := filepath.Join(dataDir, config.DefaultDBName)
	needLiveCheck := plan.RuntimeRelocate || len(plan.Residue) > 0
	var liveHolders []liveHolder
	if needLiveCheck {
		holders, err := ensureLiveOK(out, cfg, repoRoot, legacyDB, allowLive, false)
		if err != nil {
			return err
		}
		liveHolders = holders
	}

	// Report plan.
	fmt.Fprintln(out, "migrate plan:")
	if plan.RuntimeRelocate {
		home := filepath.Join(config.GlobalDir(), config.RepoKey(repoRoot))
		fmt.Fprintf(out, "  runtime relocate: %s → %s\n",
			filepath.Join(dataDir, config.DefaultDBName), home)
	} else {
		fmt.Fprintln(out, "  runtime relocate: (none)")
	}
	if needLiveCheck {
		printLivePlanLine(out, liveHolders)
	}
	if len(plan.Residue) == 0 {
		fmt.Fprintln(out, "  legacy residue:   (none)")
	} else {
		fmt.Fprintf(out, "  legacy residue:   %d path(s)\n", len(plan.Residue))
		for _, rel := range plan.Residue {
			fmt.Fprintf(out, "    - %s\n", rel)
		}
	}
	if len(plan.PruneSeeds) == 0 {
		fmt.Fprintln(out, "  substrate prune:  (none)")
	} else {
		fmt.Fprintf(out, "  substrate prune:  %d unedited seed(s)\n", len(plan.PruneSeeds))
		for _, rel := range plan.PruneSeeds {
			fmt.Fprintf(out, "    - %s\n", rel)
		}
	}
	if plan.Gitignore {
		fmt.Fprintln(out, "  gitignore:        converge managed block")
	} else {
		fmt.Fprintln(out, "  gitignore:        (already current)")
	}
	if plan.ExemptGitignore {
		fmt.Fprintln(out, "  config:           append .gitignore to [gate] edit_exempt_paths")
	} else {
		fmt.Fprintln(out, "  config:           (edit_exempt_paths current)")
	}
	if len(plan.WorkflowRetire) == 0 {
		fmt.Fprintln(out, "  workflows:        (none superseded)")
	} else {
		fmt.Fprintf(out, "  workflows:        retire %d superseded DOT workflow(s) (done.md + step.md govern)\n",
			len(plan.WorkflowRetire))
		for _, f := range plan.WorkflowRetire {
			fmt.Fprintf(out, "    - workflows/%s\n", f)
		}
	}
	fmt.Fprintln(out, "  validate:         deployed system check")
	printConversionPending(out, plan.WorkflowConvertPending)

	if !yes {
		if len(liveHolders) > 0 {
			fmt.Fprintln(out, "\ndry-run only — would REFUSE apply (live runtime); re-run with --yes after idle, or --yes --allow-live (UNSAFE)")
		} else {
			fmt.Fprintln(out, "\ndry-run only — re-run with --yes to apply")
		}
		return nil
	}

	// Refuse live runtime on apply (unless --allow-live).
	if needLiveCheck {
		if _, err := ensureLiveOK(out, cfg, repoRoot, legacyDB, allowLive, true); err != nil {
			return err
		}
	}

	// Apply.
	if plan.RuntimeRelocate {
		fmt.Fprintln(out, "\n→ runtime relocate")
		if err := runRuntimeMigrate(out, a, false, allowLive, false); err != nil {
			return fmt.Errorf("migrate: runtime: %w", err)
		}
		// Home-keyed path is now authoritative — re-resolve so later steps
		// (prune backups, residue safety) never write under legacy dataDir
		// (code-ac-review finding, sty_a3915840 AC3/AC4).
		rt := cfg.ResolveRuntimeDir(repoRoot)
		a.RuntimeDir = rt.Dir
		a.DBPath = cfg.ResolveDB(repoRoot)
		// Refresh plan residue after relocate (home now has the DB).
		plan.Residue = listLegacyResidue(dataDir)
	}

	if len(plan.Residue) > 0 {
		// Safety: never remove residue if home-keyed DB is missing.
		homeDB := filepath.Join(config.GlobalDir(), config.RepoKey(repoRoot), config.DefaultDBName)
		if !fileExists(homeDB) {
			return fmt.Errorf("migrate: refusing residue removal — home-keyed database missing at %s", homeDB)
		}
		fmt.Fprintln(out, "\n→ remove legacy residue")
		for _, rel := range plan.Residue {
			path := filepath.Join(dataDir, filepath.FromSlash(rel))
			if err := removePath(path); err != nil {
				return fmt.Errorf("migrate: remove %s: %w", rel, err)
			}
			fmt.Fprintf(out, "  removed %s\n", rel)
		}
	}

	if len(plan.PruneSeeds) > 0 {
		fmt.Fprintln(out, "\n→ substrate prune")
		opts := ResolveBackupOpts(cfg)
		// Always re-resolve: even when RuntimeRelocate was false, the App may
		// have been constructed with a stale/empty RuntimeDir.
		opts.BackupsDir = cfg.ResolveRuntimeDir(repoRoot).Dir
		if err := runSubstratePrune(out, strings.NewReader(""), dataDir, opts, true); err != nil {
			return fmt.Errorf("migrate: prune: %w", err)
		}
		// Prune backups go under the home-keyed runtime dir — never re-create
		// .satelle/backups/ residue that would fail the next migrate plan.
	}

	if plan.Gitignore {
		fmt.Fprintln(out, "\n→ gitignore converge")
		changed, err := ensureGitignore(repoRoot)
		if err != nil {
			return fmt.Errorf("migrate: gitignore: %w", err)
		}
		if changed {
			fmt.Fprintln(out, "  updated .gitignore managed block")
		} else {
			fmt.Fprintln(out, "  .gitignore already current")
		}
	}

	if len(plan.WorkflowRetire) > 0 {
		// Removal only. The route source is authored substrate this step never
		// writes: baking a repo's lifecycle into the binary so migrate could
		// install it is the config-over-code violation the constitution exists to
		// prevent (sty_9835070d).
		fmt.Fprintln(out, "\n→ retire superseded DOT workflows")
		for _, f := range plan.WorkflowRetire {
			path := filepath.Join(dataDir, "workflows", f)
			if err := removePath(path); err != nil {
				return fmt.Errorf("migrate: remove workflows/%s: %w", f, err)
			}
			fmt.Fprintf(out, "  removed workflows/%s\n", f)
		}
	}

	if plan.ExemptGitignore {
		fmt.Fprintln(out, "\n→ config converge")
		changed, err := ensureEditExemptGitignore(dataDir)
		if err != nil {
			return fmt.Errorf("migrate: edit_exempt_paths: %w", err)
		}
		if changed {
			fmt.Fprintln(out, "  updated .satelle/satelle.toml [gate] edit_exempt_paths")
		} else {
			fmt.Fprintln(out, "  edit_exempt_paths already current")
		}
	}

	fmt.Fprintln(out, "\n→ validate")
	if err := validateDeployment(out, dataDir); err != nil {
		return fmt.Errorf("migrate: validation failed: %w", err)
	}
	fmt.Fprintln(out, "\nmigrate complete")
	return nil
}

// listLegacyResidue returns dataDir-relative paths that are obsolete once
// runtime is home-keyed: satelle.db (+wal/shm), logs/, backups/, stories/,
// and agents.*.bak siblings.
func listLegacyResidue(dataDir string) []string {
	var out []string
	for _, name := range []string{
		config.DefaultDBName,
		config.DefaultDBName + "-wal",
		config.DefaultDBName + "-shm",
		"logs",
		"backups",
		"stories",
	} {
		p := filepath.Join(dataDir, name)
		if st, err := os.Stat(p); err == nil {
			if st.IsDir() {
				out = append(out, name+"/")
			} else {
				out = append(out, name)
			}
		}
	}
	// agents.*.bak (and similar) next to agents.toml
	ents, err := os.ReadDir(dataDir)
	if err == nil {
		for _, e := range ents {
			n := e.Name()
			if strings.HasPrefix(n, "agents.") && strings.HasSuffix(n, ".bak") {
				out = append(out, n)
			}
		}
	}
	sort.Strings(out)
	return out
}

// hasLegacyResidue is true when any obsolete in-repo runtime path exists.
func hasLegacyResidue(dataDir string) bool {
	return len(listLegacyResidue(dataDir)) > 0
}

// listUneditedSeeds returns dataDir-relative paths of unedited embedded seeds.
func listUneditedSeeds(dataDir string) []string {
	var out []string
	for _, d := range config.EmbeddedDefaults() {
		if d.Kind == "tasks" {
			continue
		}
		rel := d.Kind + "/" + d.Name + ".md"
		path := filepath.Join(dataDir, filepath.FromSlash(rel))
		body, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if isUneditedSeed(string(body), d.Body) {
			out = append(out, rel)
		}
	}
	sort.Strings(out)
	return out
}

func removePath(path string) error {
	st, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if st.IsDir() {
		return os.RemoveAll(path)
	}
	return os.Remove(path)
}

// editExemptGitignoreNeedsConverge reports whether satelle.toml's non-empty
// [gate] edit_exempt_paths lacks the managed ".gitignore" entry (sty_f115e6bf).
func editExemptGitignoreNeedsConverge(dataDir string) bool {
	raw, err := os.ReadFile(filepath.Join(dataDir, config.ConfigName))
	if err != nil {
		return false
	}
	return editExemptNeedsGitignore(string(raw))
}

// ensureEditExemptGitignore appends ".gitignore" to a non-empty
// [gate] edit_exempt_paths list when missing. Operator entries and order are
// preserved. Returns true when the file was rewritten. An absent key or an
// explicitly empty list is left alone (init WARNs on absence; empty is opt-out).
func ensureEditExemptGitignore(dataDir string) (bool, error) {
	path := filepath.Join(dataDir, config.ConfigName)
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	content := string(raw)
	if !editExemptNeedsGitignore(content) {
		return false, nil
	}
	items := config.ListStringValues(content, "gate", "edit_exempt_paths")
	// Append-only merge.
	merged := append(append([]string{}, items...), managedEditExemptEntry)
	quoted := make([]string, 0, len(merged))
	for _, p := range merged {
		quoted = append(quoted, `"`+p+`"`)
	}
	value := "[" + strings.Join(quoted, ", ") + "]"
	next := config.UpsertKey(content, "gate", "edit_exempt_paths", value)
	if next == content {
		return false, nil
	}
	if err := os.WriteFile(path, []byte(next), 0o644); err != nil {
		return false, err
	}
	return true, nil
}
