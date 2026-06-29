// `satelle validate` runs each authored doc's DETERMINISTIC structure check
// (internal/structure) over the indexed substrate and reports pass/fail — for
// manual or CI use. Read-only; it never mutates and needs no agent CLI (the
// checks are code, not an LLM rubric). Exit is non-zero if any doc fails.
package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/docindex"
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
			docs, err := a.Store.DocIndex.List(context.Background(), kindFilter)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			resolve := skillResolver(a)
			validated, failed := 0, 0
			for _, d := range docs {
				if nameFilter != "" && d.Name != nameFilter {
					continue
				}
				switch {
				case d.Kind == "documents":
					// Free-form documents get the deterministic OKF conformance
					// check (a concept doc needs a non-empty `type`).
					validated++
					if err := docindex.OKFConformance(d.Name, d.Body); err != nil {
						failed++
						fmt.Fprintf(out, "FAIL  documents/%s (okf) — %s\n", d.Name, err)
					} else {
						fmt.Fprintf(out, "PASS  documents/%s (okf)\n", d.Name)
					}
				case structure.Checked(d.Kind):
					validated++
					if problems := structure.Doc(d.Kind, d.Name, d.Body, resolve); len(problems) > 0 {
						for _, p := range problems {
							failed++
							fmt.Fprintf(out, "FAIL  %s/%s — %s\n", d.Kind, d.Name, p)
						}
					} else {
						fmt.Fprintf(out, "PASS  %s/%s\n", d.Kind, d.Name)
					}
				}
			}
			fmt.Fprintf(out, "\nvalidated %d, failed %d\n", validated, failed)
			if failed > 0 {
				return fmt.Errorf("%d doc(s) failed structure validation", failed)
			}
			return nil
		},
	}
	register(cmd)
}
