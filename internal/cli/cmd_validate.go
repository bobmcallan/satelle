// `satelle validate` runs each authored doc's DETERMINISTIC structure check
// (internal/structure) over the indexed substrate and reports pass/fail — for
// manual or CI use. Read-only; it never mutates and needs no agent CLI (the
// checks are code, not an LLM rubric). Exit is non-zero if any doc fails.
package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/reviewer"
	"github.com/bobmcallan/satelle/internal/structure"
)

func init() {
	cmd := &cobra.Command{
		Use:   "validate [kind] [name]",
		Short: "Validate authored docs against their deterministic structure checks",
		Long: `validate runs the deterministic structure check for each authored doc kind
(skills, workflows, principles) and reports pass/fail. Documents get the OKF
conformance check. With no argument it validates everything; pass a kind (and
optionally a name) to narrow. Exit is non-zero if any doc fails.`,
		Args:        cobra.MaximumNArgs(2),
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			var kindFilter, nameFilter string
			if len(args) > 0 {
				kindFilter = args[0]
			}
			if len(args) > 1 {
				nameFilter = args[1]
			}

			a, err := appFrom(cmd)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			resolve := skillResolver(a)
			dirs := a.AuthoredDirs()
			validated, failed, exempt := 0, 0, 0
			// File-based (sty_fbd059d3): walk the authored markdown FILES, not just
			// the indexed docs, so a malformed or never-indexed doc is caught instead
			// of silently skipped. Reserved keep-files (README.md, index.md, log.md)
			// are recognised and exempted — they are not authored concept docs.
			for _, kind := range config.AuthoredKinds {
				if kindFilter != "" && kindFilter != kind {
					continue
				}
				entries, derr := os.ReadDir(dirs[kind])
				if derr != nil {
					continue // an absent authored dir has nothing to validate
				}
				for _, e := range entries {
					fn := e.Name()
					if e.IsDir() || !strings.HasSuffix(fn, ".md") {
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
					body, rerr := os.ReadFile(filepath.Join(dirs[kind], fn))
					if rerr != nil {
						failed++
						fmt.Fprintf(out, "FAIL  %s/%s — read: %v\n", kind, name, rerr)
						continue
					}
					validated++
					if kind == "documents" {
						// Free-form documents get the OKF conformance check (a concept
						// doc needs a non-empty `type`).
						if err := docindex.OKFConformance(name, string(body)); err != nil {
							failed++
							fmt.Fprintf(out, "FAIL  documents/%s (okf) — %s\n", name, err)
						} else {
							fmt.Fprintf(out, "PASS  documents/%s (okf)\n", name)
						}
						continue
					}
					if problems := structure.Doc(kind, name, string(body), resolve); len(problems) > 0 {
						for _, p := range problems {
							failed++
							fmt.Fprintf(out, "FAIL  %s/%s — %s\n", kind, name, p)
						}
					} else {
						fmt.Fprintf(out, "PASS  %s/%s\n", kind, name)
					}
				}
			}
			// Cross-workflow consistency (sty_4c0c7246): ambiguous applies_to and
			// unresolved referenced skills — a whole-set check, so only when
			// validating the workflows kind (all, or `workflows`) without a name.
			if nameFilter == "" && (kindFilter == "" || kindFilter == "workflows") {
				wfs, lerr := a.Store.DocIndex.List(context.Background(), "workflows")
				if lerr != nil {
					return lerr
				}
				for _, p := range reviewer.WorkflowConsistency(wfs, resolve) {
					failed++
					fmt.Fprintf(out, "FAIL  workflows (consistency) — %s\n", p)
				}
			}

			// Enforce the actors.toml→agents.toml rename (sty_7db2ed7d): the legacy
			// filename is no longer loaded, so a repo still carrying it is silently
			// running on defaults — flag it as a failure with the fix.
			if kindFilter == "" {
				dataDir := filepath.Dir(a.DBPath)
				if _, statErr := os.Stat(filepath.Join(dataDir, config.ActorsConfigName)); statErr == nil {
					failed++
					fmt.Fprintf(out, "FAIL  agents-layer — deprecated %s/%s: rename it to %s (the legacy filename is no longer loaded)\n",
						config.DefaultDataDir, config.ActorsConfigName, config.AgentsConfigName)
				}
			}

			fmt.Fprintf(out, "\nvalidated %d, failed %d, exempt %d\n", validated, failed, exempt)
			if failed > 0 {
				return fmt.Errorf("%d doc(s) failed structure validation", failed)
			}
			return nil
		},
	}
	register(cmd)
}

// reservedKeepFile reports whether fn is a reserved, non-authored keep-file that
// validate exempts (sty_fbd059d3): the per-dir README, and the documents layer's
// reserved index.md/log.md. Everything else under the authored dirs must comply.
func reservedKeepFile(fn string) bool {
	switch fn {
	case "README.md", "index.md", "log.md":
		return true
	default:
		return false
	}
}
