// Per-noun validation: `satelle workflow|skill|principle|task validate [name]`.
// "validate" is a SEMANTIC, kind-specific check — a valid workflow PROCESS, a
// valid skill contract — run DETERMINISTICALLY (code, not an LLM rubric) over the
// on-disk files, reporting pass/fail and exiting non-zero on any failure. It is
// distinct from OKF conformance, which `satelle reindex` owns and self-heals.
// There is no generic top-level `satelle validate`: each noun validates its own.
package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/reviewer"
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
	resolve := skillResolver(a)
	validated, failed, exempt := 0, 0, 0

	dir := a.AuthoredDirs()[kind]
	if kind == "tasks" {
		dir = filepath.Join(filepath.Dir(a.DBPath), "tasks")
	}
	if entries, derr := os.ReadDir(dir); derr == nil {
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
		}
	}

	// Cross-workflow consistency (ambiguous applies_to, unresolved referenced
	// skills) — a whole-set check, so only for the workflows kind without a name.
	if kind == "workflows" && nameFilter == "" {
		wfs, lerr := a.Store.DocIndex.List(context.Background(), "workflows")
		if lerr != nil {
			return lerr
		}
		for _, p := range reviewer.WorkflowConsistency(wfs, resolve) {
			failed++
			fmt.Fprintf(out, "FAIL  workflows (consistency) — %s\n", p)
		}
	}

	fmt.Fprintf(out, "\nvalidated %d, failed %d, exempt %d\n", validated, failed, exempt)
	if failed > 0 {
		return fmt.Errorf("%d %s failed validation", failed, kind)
	}
	return nil
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
