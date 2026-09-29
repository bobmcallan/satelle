package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/fixlane"
)

func init() {
	fix := &cobra.Command{
		Use:   "fix",
		Short: "The scoped in-loop fix lane: record a bounded claim, then make that one edit",
		Long: `The in-loop fix lane. For a small, self-evident fix found mid-step, where a
full engage is the wrong size. Record a claim BEFORE the edit; the edit gate
then allows that one edit. The lane changes who may edit, never what a gate
judges. See: satelle help fix-lane.`,
	}

	var reason, provingTest string
	var lines int
	claim := &cobra.Command{
		Use:   "claim <path>",
		Short: "Record an in-loop-fix claim before editing <path>",
		Long: `Record a typed claim: the path, why the fix is self-evident (--reason), the size
bound in changed lines (--lines) and the named test that proves it (--test).
Refused — and the refusal recorded — when the path is product surface, a gate
skill, a reviewer rubric, a workflow, a principle or the constitution, when no
proving test is named, or when no story is engaged. One claim licenses ONE edit
and dies at the story's next transition.`,
		Args:        cobra.ExactArgs(1),
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := appFrom(cmd)
			if err != nil {
				return err
			}
			if currentDispatchMarker() != (dispatchMarker{}) || currentRelayMarker() != (relayMarker{}) {
				return errors.New("fix lane: only the driving session may record a claim, not a dispatched performer")
			}
			info, engaged, err := resolveSeat(true, config.ResolveSession())
			if err != nil {
				return err
			}
			// A relative path the driver types means relative to WHERE THEY ARE, not
			// to the repo root: from inside internal/, "foo.go" is internal/foo.go.
			// Resolving it against the root instead would judge the wrong file and
			// let a product path through as an innocuous root-level name.
			target, err := filepath.Abs(args[0])
			if err != nil {
				return fmt.Errorf("fix lane: cannot resolve %q against the working directory: %w", args[0], err)
			}
			in := fixlane.Input{
				Path: target, Reason: reason, BoundLines: lines, ProvingTest: provingTest,
			}
			// Live seat only: an unengaged claim has no edge to judge the fix, so
			// it is recorded as a refusal (no story) and never granted.
			if engaged && info.Engaged && !info.Stale && !info.InFlight {
				in.StoryID, in.Status = info.ItemID, info.StoryStatus
			}
			c, err := fixlane.Record(cmd.Context(), a.Store.Ledger, a.Config, a.RepoRoot, in, time.Now())
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "claim %s recorded — %s, up to %d lines, proved by %q; one edit, dies at the next transition of %s\n",
				c.ID, c.Path, c.BoundLines, c.ProvingTest, c.StoryID)
			return nil
		},
	}
	claim.Flags().StringVar(&reason, "reason", "", "why the fix is self-evident (required)")
	claim.Flags().IntVar(&lines, "lines", 0, "size bound: the most changed lines the edit may be (required)")
	claim.Flags().StringVar(&provingTest, "test", "", "the named test that proves the fix (required)")

	var since, until string
	var asJSON bool
	report := &cobra.Command{
		Use:   "report",
		Short: "Analyse the fix lane over a window: claims, sizes, refusals by class, rejects by gate",
		Long: `Compute, from the ledger rows alone, over --since/--until (YYYY-MM-DD or RFC3339):
the claim count, the declared size distribution, the refused-claim count by
class, and the reject count per gate. This is what makes the bound enforceable:
a lane whose exception rate is unmeasured is indistinguishable from no lane.`,
		Args:        cobra.NoArgs,
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := appFrom(cmd)
			if err != nil {
				return err
			}
			from, err := parseFixWindow(since)
			if err != nil {
				return err
			}
			to, err := parseFixWindow(until)
			if err != nil {
				return err
			}
			f, err := fixlane.Report(cmd.Context(), a.Store.Ledger, from, to)
			if err != nil {
				return err
			}
			if asJSON {
				b, _ := json.MarshalIndent(f, "", "  ")
				fmt.Fprintln(cmd.OutOrStdout(), string(b))
				return nil
			}
			printFixFigures(cmd, f)
			return nil
		},
	}
	report.Flags().StringVar(&since, "since", "", "only rows at/after this date (YYYY-MM-DD or RFC3339)")
	report.Flags().StringVar(&until, "until", "", "only rows at/before this date (YYYY-MM-DD or RFC3339)")
	report.Flags().BoolVar(&asJSON, "json", false, "print the figures as JSON")

	fix.AddCommand(claim, report)
	register(fix)
}

func parseFixWindow(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, nil
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("fix report: %q is not YYYY-MM-DD or RFC3339", s)
}

func printFixFigures(cmd *cobra.Command, f fixlane.Figures) {
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "claims: %d (granted %d, refused %d); consumed by an edit: %d\n", f.Claims(), f.Granted, f.Refused, f.Consumed)
	fmt.Fprintf(out, "declared size (lines): n=%d min=%d median=%d max=%d total=%d\n", f.Bound.Count, f.Bound.Min, f.Bound.Median, f.Bound.Max, f.Bound.Total)
	fmt.Fprintf(out, "edited size (lines):   n=%d min=%d median=%d max=%d total=%d\n", f.Used.Count, f.Used.Min, f.Used.Median, f.Used.Max, f.Used.Total)
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "REFUSED CLASS\tCLAIMS")
	for _, k := range sortedKeys(f.RefusedByClass) {
		fmt.Fprintf(tw, "%s\t%d\n", k, f.RefusedByClass[k])
	}
	fmt.Fprintln(tw, "GATE\tREJECTS")
	for _, k := range sortedKeys(f.RejectsByGate) {
		fmt.Fprintf(tw, "%s\t%d\n", k, f.RejectsByGate[k])
	}
	_ = tw.Flush()
}

func sortedKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
