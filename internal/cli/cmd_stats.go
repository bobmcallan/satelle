package cli

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/throughput"
	"github.com/bobmcallan/satelle/internal/workspace"
)

// Seams a test replaces: the workspace registry and the clock the window ends at.
var (
	statsRegistry = workspace.Registered
	statsFind     = workspace.FindRegistered
	statsNow      = time.Now
)

func init() {
	var (
		since  string
		repo   string
		asJSON bool
	)
	c := &cobra.Command{
		Use:   "stats",
		Short: "Report finished stories and epics, and cycle times, across registered repos",
		Long: `Report, for each repo in the workspace registry and in total, how many stories
and epics reached a terminal state per UTC day and ISO week, the median and p90
cycle time of each phase, and the median time spent in each status.

Phase 1 runs from the start state to the step the route marks pr_step = true;
phase 2 from there to the terminal state. A route that marks none reports phase
2 as n/a. A figure the ledger does not record reads "unavailable" (null with a
reason in --json), never 0. Every repo is opened read-only.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			window, err := parseStatsSince(since)
			if err != nil {
				return err
			}
			roots, err := statsRoots(repo)
			if err != nil {
				return err
			}
			until := statsNow().UTC()
			rep := throughput.Build(cmd.Context(), roots, throughput.Window{Since: until.Add(-window), Until: until})
			if asJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(rep)
			}
			throughput.Text(cmd.OutOrStdout(), rep)
			return nil
		},
	}
	c.Flags().StringVar(&since, "since", "168h", "window length back from now: a Go duration (168h) or whole days (7d)")
	c.Flags().StringVar(&repo, "repo", "", "report only the registered repo with this name")
	c.Flags().BoolVar(&asJSON, "json", false, "emit the report as one JSON document")
	register(c)
}

// parseStatsSince reads --since: whole days (7d) or a Go duration (168h).
func parseStatsSince(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	var d time.Duration
	if n, ok := strings.CutSuffix(s, "d"); ok {
		days, err := strconv.Atoi(n)
		if err != nil {
			return 0, fmt.Errorf("stats: invalid --since %q: use whole days like 7d or a duration like 168h", s)
		}
		d = time.Duration(days) * 24 * time.Hour
	} else {
		parsed, err := time.ParseDuration(s)
		if err != nil {
			return 0, fmt.Errorf("stats: invalid --since %q: use whole days like 7d or a duration like 168h", s)
		}
		d = parsed
	}
	if d <= 0 {
		return 0, fmt.Errorf("stats: --since %q must be positive", s)
	}
	return d, nil
}

// statsRoots is the registered repos to report on, each once: all of them, or
// the one named by --repo.
func statsRoots(name string) ([]string, error) {
	if name != "" {
		roots, err := statsFind(name)
		if err != nil {
			return nil, fmt.Errorf("stats: read the workspace registry: %w", err)
		}
		switch len(roots) {
		case 0:
			return nil, fmt.Errorf("stats: repo %s is not in the workspace registry (satelle workspace list)", name)
		case 1:
			return roots, nil
		default:
			return nil, fmt.Errorf("stats: ambiguous repo name %s, registered as %s", name, strings.Join(roots, ", "))
		}
	}
	repos, err := statsRegistry()
	if err != nil {
		return nil, fmt.Errorf("stats: read the workspace registry: %w", err)
	}
	var roots []string
	seen := map[string]bool{}
	for _, r := range repos {
		if canon := config.CanonicalRepoRoot(r); !seen[canon] {
			seen[canon] = true
			roots = append(roots, canon)
		}
	}
	return roots, nil
}
