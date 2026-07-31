// Per-noun validation: `satelle workflow|skill|principle|task validate [name]`.
// "validate" is a SEMANTIC, kind-specific check — a valid workflow PROCESS, a
// valid skill contract — run DETERMINISTICALLY (code, not an LLM rubric) over the
// on-disk files, reporting pass/fail and exiting non-zero on any failure. It is
// distinct from OKF conformance, which `satelle reindex` owns and self-heals.
// There is no generic top-level `satelle validate`: each noun validates its own.
package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/agentstep"
	"github.com/bobmcallan/satelle/internal/agentvalidate"
	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/doctor"
	"github.com/bobmcallan/satelle/internal/structure"
)

// authoredValidateCmd builds the per-noun `<noun> validate [name]` subcommand for
// an authored kind (workflows/skills/principles/tasks).
func authoredValidateCmd(kind string) *cobra.Command {
	return &cobra.Command{
		Use:         "validate [name]",
		Short:       "Validate authored " + kind + " against the deterministic " + strings.TrimSuffix(kind, "s") + " check",
		Args:        cobra.MaximumNArgs(1),
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := ""
			if len(args) > 0 {
				name = args[0]
			}
			a, err := appFrom(cmd)
			if err != nil {
				return err
			}
			return validateKind(cmd, a, kind, name)
		},
	}
}

// validateKind runs the deterministic check for one kind over its on-disk files
// (nameFilter narrows to one), printing pass/fail and returning an error if any
// fail. Workflows also get the cross-workflow consistency check.
func validateKind(cmd *cobra.Command, a *app.App, kind, nameFilter string) error {
	out := cmd.OutOrStdout()

	dataDir := a.DataDir
	if dataDir == "" {
		dataDir = a.Config.ResolveDataDir(a.RepoRoot)
	}
	// Disk-backed, through the same helper `satelle doctor` uses, so the two can
	// never contradict each other on an unchanged tree (sty_540cfcd3). The doc
	// index lags an authored edit, and validating right after an edit is exactly
	// what an author does.
	resolve := doctor.SkillResolver(dataDir)
	dir := a.AuthoredDirs()[kind]
	if kind == "tasks" {
		// Tasks are authored substrate under DataDir — never RuntimeDir (sty_4660bbe1).
		dir = filepath.Join(dataDir, "tasks")
	}
	validated, failed, exempt := validateAuthoredDir(out, kind, dir, nameFilter, resolve)

	// Cross-workflow consistency (ambiguous applies_to, unresolved referenced
	// skills) — a whole-set check, so only for the workflows kind without a name.
	if kind == "workflows" && nameFilter == "" {
		// CONSISTENCY judges the AUTHORED set; ALLOCATION judges the GOVERNING set
		// (authored ∪ unshadowed embedded). doctor has always drawn that line —
		// an on-disk wildcard workflow legitimately shadows the embedded wildcard
		// baseline, so feeding both to the ambiguity check would report every repo
		// as broken, while allocation must still see embedded defaults that really
		// do govern a repo which has not overridden them.
		for _, p := range agentstep.WorkflowConsistency(doctor.WorkflowDocs(dataDir), resolve) {
			failed++
			fmt.Fprintf(out, "FAIL  workflows (consistency) — %s\n", p)
		}
		// Per-gate effective model surface (sty_19456622) — same allocation view as
		// `satelle agent validate`, informational (not a hard fail).
		if eff, aerr := config.LoadEffectiveAgents(dataDir, a.Config.Vars); aerr == nil {
			report := agentvalidate.Validate(eff.Agents, eff.Vars, doctor.GoverningWorkflows(dataDir))
			if len(report.Gates) > 0 {
				fmt.Fprintln(out, "Gate/node effective models:")
				for _, ga := range report.Gates {
					fmt.Fprintf(out, "  NODE [%s] %s gate=%s agent=%s effective_model=%q\n",
						ga.Workflow, ga.Node, ga.Skill, ga.Agent, ga.EffectiveModel)
				}
			}
		}
	}

	// Named form: the whole-set block above is skipped, which used to mean a
	// workflow naming a gate skill that does not resolve validated clean on the
	// very path an author uses while authoring it — and then advanced ungated at
	// run time (sty_d59ec6a9). Report the per-workflow half here.
	//
	// WARN, not FAIL: a repo mid-authoring writes the workflow before it writes
	// the gate skills, and the embedded satelle-workflow-advisor skill drives
	// exactly this named form. Blocking would make the ordinary authoring
	// sequence impossible. The whole-set path keeps FAIL — it answers a
	// different question (is my substrate coherent as shipped).
	//
	// The ambiguity check is NOT run here: it compares repo workflows against
	// each other, so it is whole-set by nature (AC3).
	if kind == "workflows" && nameFilter != "" {
		for _, w := range doctor.WorkflowDocs(dataDir) {
			if w.Name != nameFilter {
				continue
			}
			problems := agentstep.WorkflowSkillProblems(w, resolve)
			for _, p := range problems {
				fmt.Fprintf(out, "WARN  workflows/%s — %s\n", w.Name, p)
			}
			if len(problems) > 0 {
				// Name the CONSEQUENCE, not just the missing file. A warning that
				// only says "skill not found" is the same silence one level up:
				// the reader has to already know that an unresolved gate degrades
				// to advisory and lets the transition through.
				fmt.Fprintln(out, "      an edge whose gate skill does not resolve is ADVISORY — it advances UNGATED, with no reviewer and no verdict, until the skill exists")
			}
			break
		}
	}

	// Placement invariants (order:6): embedded_sha, residency markers,
	// no principle scope:, resident set under SessionStart ceiling. Whole-set
	// only — same anchor pattern as workflow consistency.
	if kind == "principles" && nameFilter == "" {
		constitution := readConstitution(a.Config.ResolveConstitution(a.RepoRoot))
		for _, p := range auditPlacement(dataDir, config.EmbeddedDefaults(), constitution) {
			failed++
			fmt.Fprintf(out, "FAIL  principles (placement) — %s\n", p)
		}
		// Wikilink resolution: every [[target]] in substrate must resolve.
		// Anchored here so `satelle principle validate` and the top-level
		// `satelle validate` both surface danglers.
		for _, p := range auditWikilinks(dataDir, config.EmbeddedDefaults()) {
			failed++
			fmt.Fprintf(out, "FAIL  principles (wikilinks) — %s\n", p)
		}
	}

	fmt.Fprintf(out, "\nvalidated %d, failed %d, exempt %d\n", validated, failed, exempt)
	if failed > 0 {
		return fmt.Errorf("%d %s failed validation", failed, kind)
	}
	return nil
}

// validateAuthoredDir runs the deterministic per-file check for one kind over a
// directory's markdown files (nameFilter narrows to one), printing PASS/FAIL
// lines to out and returning the counts. It is file-based and store-free, so it
// serves both the per-noun `satelle <noun> validate` (validateKind) and the init
// deployment validation (sty_d0d6bb67), which runs before anything is indexed.
func validateAuthoredDir(out io.Writer, kind, dir, nameFilter string, resolve func(skill string) bool) (validated, failed, exempt int) {
	entries, derr := os.ReadDir(dir)
	if derr != nil {
		return 0, 0, 0
	}
	for _, e := range entries {
		fn := e.Name()
		if e.IsDir() || !strings.HasSuffix(fn, ".md") {
			continue
		}
		if kind == "tasks" && !strings.HasPrefix(fn, "tsk_") {
			continue
		}
		name := strings.TrimSuffix(fn, ".md")
		if nameFilter != "" && nameFilter != name {
			continue
		}
		if reservedKeepFile(fn) {
			exempt++
			fmt.Fprintf(out, "EXEMPT %s/%s (reserved keep-file)\n", kind, name)
			continue
		}
		body, rerr := os.ReadFile(filepath.Join(dir, fn))
		if rerr != nil {
			failed++
			fmt.Fprintf(out, "FAIL  %s/%s — read: %v\n", kind, name, rerr)
			continue
		}
		validated++
		var problems []string
		switch kind {
		case "documents":
			if err := docindex.OKFConformance(name, string(body)); err != nil {
				problems = []string{err.Error()}
			}
		case "tasks":
			problems = structure.CheckTask(string(body))
		default:
			problems = structure.Doc(kind, name, string(body), resolve)
		}
		if len(problems) > 0 {
			for _, p := range problems {
				failed++
				fmt.Fprintf(out, "FAIL  %s/%s — %s\n", kind, name, p)
			}
		} else {
			fmt.Fprintf(out, "PASS  %s/%s\n", kind, name)
		}
		// Advisory only: scoped-reviewer over-fire (and future) warnings never
		// increment failed / change exit (sty_9882b8c6).
		for _, w := range structure.DocWarnings(kind, name, string(body)) {
			fmt.Fprintf(out, "WARN  %s/%s — %s\n", kind, name, w)
		}
	}
	return validated, failed, exempt
}

// reservedKeepFile reports whether fn is a reserved, non-authored keep-file that
// validation exempts: the per-dir README, and the OKF layer's reserved
// index.md/log.md. Everything else under an authored dir must comply.
func reservedKeepFile(fn string) bool {
	switch fn {
	case "README.md", "index.md", "log.md":
		return true
	default:
		return false
	}
}
