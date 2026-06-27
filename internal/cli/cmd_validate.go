// `satelle validate` runs each authored doc's required-structure reviewer
// (satelle-<object>-review) over the indexed substrate and reports pass/fail with
// the reviewer's notes — for manual or CI use. Read-only; it never mutates. Exit
// is non-zero if any doc fails validation (sty_ccdf5a55).
package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/reviewer"
)

func init() {
	cmd := &cobra.Command{
		Use:   "validate [kind] [name]",
		Short: "Validate authored docs against their structure reviewers",
		Long: `validate runs the required-structure reviewer for each authored doc kind
(skills → satelle-skill-review, workflows → satelle-workflow-review,
principles → satelle-principle-review) and reports pass/fail. With no argument it
validates every reviewer-backed doc; pass a kind (and optionally a name) to
narrow. Exit is non-zero if any doc fails.`,
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

			g, a, err := gaterForCmd(cmd)
			if err != nil {
				return err
			}
			docs, err := a.Store.DocIndex.List(context.Background(), kindFilter)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			validated, failed := 0, 0
			for _, d := range docs {
				rev := reviewer.StructureReviewerFor(d.Kind)
				if rev == "" {
					continue // no structure reviewer for this kind (e.g. documents)
				}
				if nameFilter != "" && d.Name != nameFilter {
					continue
				}
				dec, err := g.ReviewStructure(context.Background(), rev, d.Kind, d.Name, d.Body)
				if err != nil {
					return err
				}
				validated++
				switch {
				case !dec.Gated:
					fmt.Fprintf(out, "SKIP  %s/%s — %s rubric not installed\n", d.Kind, d.Name, rev)
				case dec.Accept:
					fmt.Fprintf(out, "PASS  %s/%s\n", d.Kind, d.Name)
				default:
					failed++
					fmt.Fprintf(out, "FAIL  %s/%s — %s\n", d.Kind, d.Name, dec.Notes)
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
