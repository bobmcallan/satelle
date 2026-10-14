package cli

import (
	"context"
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
		Use:   "validate",
		Short: "Validate all authored substrate (per-kind checks + wikilink resolution)",
		Long: `Run every per-kind structure check across this repo's authored substrate, then
resolve wikilinks between the files.

The cross-file half is what a per-kind validate cannot do: a skill that cites a
principle by name is only correct if that name resolves, and a rename breaks it
silently. Read-only, deterministic, no agent — run it after any substrate edit.`,
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
			dataDir := a.PlaneDir()
			problems := auditWikilinks(dataDir, config.EmbeddedDefaults())
			if len(problems) == 0 {
				fmt.Fprintln(out, "PASS  wikilinks (all [[refs]] resolve)")
			} else {
				for _, p := range problems {
					failed++
					fmt.Fprintf(out, "FAIL  wikilinks — %s\n", p)
				}
			}
			fmt.Fprintf(out, "\nwikilink problems: %d\n\n", len(problems))
			// The machine catalog's cloud identities (sty_b61d2066): a literal
			// secret or malformed entry is a load error, an undefined ${NAME} a
			// reference problem. Same check, same owner as `agent validate`; the
			// lines name the field, never the value.
			fmt.Fprintln(out, "# cloud identities")
			cloudFailed := 0
			global, gerr := config.LoadGlobalAgents()
			if gerr != nil {
				cloudFailed++
				fmt.Fprintf(out, "FAIL  %v\n", gerr)
			}
			for _, p := range cloudRefProblems(a, global) {
				cloudFailed++
				fmt.Fprintf(out, "FAIL  %s\n", p)
			}
			if cloudFailed == 0 {
				fmt.Fprintln(out, "PASS  cloud identities")
			}
			failed += cloudFailed
			fmt.Fprintln(out)
			// Informational: an exceeded budget warns and never counts as a failure.
			reportInjected(context.Background(), out, a)
			if failed > 0 {
				return fmt.Errorf("validate failed")
			}
			return nil
		},
	}
}
