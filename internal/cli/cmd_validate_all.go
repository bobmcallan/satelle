package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/config"
)

func init() {
	register(validateAllCmd())
}

// validateAllCmd is the top-level `satelle validate` — runs every per-kind
// deterministic check plus the cross-substrate wikilink audit.
func validateAllCmd() *cobra.Command {
	return &cobra.Command{
		Use:         "validate",
		Short:       "Validate all authored substrate (per-kind checks + wikilink resolution)",
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := appFrom(cmd)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			var failed int
			for _, kind := range []string{"workflows", "skills", "principles", "tasks"} {
				fmt.Fprintf(out, "# %s\n", kind)
				if err := validateKind(cmd, a, kind, ""); err != nil {
					failed++
				}
				fmt.Fprintln(out)
			}
			// Wikilink audit (whole substrate).
			fmt.Fprintln(out, "# wikilinks")
			dataDir := a.Config.ResolveDataDir(a.RepoRoot)
			problems := auditWikilinks(dataDir, config.EmbeddedDefaults())
			if len(problems) == 0 {
				fmt.Fprintln(out, "PASS  wikilinks (all [[refs]] resolve)")
			} else {
				for _, p := range problems {
					failed++
					fmt.Fprintf(out, "FAIL  wikilinks — %s\n", p)
				}
			}
			fmt.Fprintf(out, "\nwikilink problems: %d\n", len(problems))
			if failed > 0 {
				return fmt.Errorf("validate failed")
			}
			return nil
		},
	}
}
