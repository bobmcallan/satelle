package cli

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/doctor"
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
			// The machine catalog must load: a retired table or an unknown key is
			// a load error, the same one `agent validate` reports.
			fmt.Fprintln(out, "# agent catalog")
			if _, gerr := config.LoadGlobalAgents(); gerr != nil {
				failed++
				fmt.Fprintf(out, "FAIL  %v\n", gerr)
			} else {
				fmt.Fprintln(out, "PASS  agent catalog")
			}
			fmt.Fprintln(out)
			// Command rules (sty_a74551d6): a malformed rule is refused at config
			// load; an allow entry naming no state of the route is doctor's finding.
			fmt.Fprintln(out, "# command rules")
			if proc, _, _, _, perr := config.LoadInvokingProcess(); perr != nil {
				failed++
				fmt.Fprintf(out, "FAIL  %v\n", perr)
			} else if fs := doctor.CheckCommandRuleAllowNames(proc, doctor.GoverningWorkflowsAt(filepath.Join(dataDir, "workflows"))); len(fs) > 0 {
				for _, f := range fs {
					failed++
					fmt.Fprintf(out, "FAIL  %s: %s\n", f.Title, f.Detail)
				}
			} else {
				fmt.Fprintln(out, "PASS  command rules")
			}
			fmt.Fprintln(out)
			// Command policy subscriptions (sty_041bf275): a malformed one is refused
			// at config load, reported above. What follows is the cache only — no
			// network call — and a list that is missing, stale or failed is a warning,
			// never a validation failure.
			fmt.Fprintln(out, "# command policy")
			reportPolicyCache(out)
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
