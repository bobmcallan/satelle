// `satelle doctor` is the one diagnostic surface: is satelle ready to govern
// this repository, and optionally every registered one (sty_e9da28e2).
//
// It composes the validators that already exist rather than adding rules of its
// own, so init, engagement, and doctor cannot drift into three different
// opinions about the same repo.
package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/doctor"
	"github.com/bobmcallan/satelle/internal/health"
)

func init() {
	var (
		all     bool
		live    bool
		asJSON  bool
		verbose bool
		timeout time.Duration
	)
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose whether satelle can govern this repository (add --all for every registered repo)",
		Long: `doctor reports whether satelle is ready to govern a repository.

It COMPOSES the checks that already own their rules — the agents layer and its
machine-wide profile references, workflow structure, cross-workflow consistency,
lifecycle-hook allocations, skills, permission ceilings, required binaries, and
harness hook scaffolding — and prints each binding's effective value with the
source that supplied it. It adds no rule of its own, so it can never disagree
with what init validates or what engagement refuses.

TWO CONFIGURATION LAYERS, deliberately kept apart:

  repo workflow POLICY        .satelle/ — workflows, gates, lifecycle hooks, and
                              which logical agent runs each step. What THIS repo
                              enforces. Never machine-wide.
  machine-wide EXECUTION      ~/.satelle/agents.toml — reusable provider profiles
                              (command, transport, model, effort, tools). HOW a
                              command runs on this machine, shared by every repo.

SEVERITY
  FAIL  an error: the repo is not ready; engagement will refuse for the same reason
  WARN  advisory: printed, never blocking (the underlying test is a heuristic)
  INFO  context you asked for (a live probe that answered)

EXIT CODES
  0  healthy — warnings are allowed
  1  one or more error findings, in any checked repository
  2  doctor could not run (bad flag, unreadable global config)

LIVE PROBES (--live) SPAWN PROVIDER PROCESSES. Ordinary doctor performs no paid
and no network model call at all. --live starts each isolated binding's CLI: a
command-transport binding is asked for its version, and an ACP binding is taken
through a single initialize handshake. Neither opens a session or sends a prompt,
so neither is a paid model call — but both may consume provider authentication
and rate budget. Every probe is bounded by --timeout and its process group is
killed and reaped on the deadline or on cancellation.`,
		Args:        cobra.NoArgs,
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := appFrom(cmd)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			opts := doctor.Opts{
				RepoRoot:      a.RepoRoot,
				DataDir:       a.DataDir,
				Live:          live,
				LiveTimeout:   timeout,
				ScaffoldDrift: scaffoldFindings,
			}
			ctx := context.Background()

			var reports []doctor.Report
			if all {
				reports = doctor.CheckAll(ctx, opts)
				if len(reports) == 0 {
					fmt.Fprintln(out, "no registered repositories — add one with `satelle workspace add <path>`")
					return nil
				}
			} else {
				reports = []doctor.Report{doctor.Check(ctx, opts)}
			}

			if asJSON {
				if err := doctor.RenderJSON(out, reports); err != nil {
					return err
				}
			} else {
				doctor.RenderText(out, reports, !all || verbose, verbose)
			}
			if doctor.ExitCode(reports) != doctor.ExitHealthy {
				_, unhealthy := doctor.Summarise(reports)
				return fmt.Errorf("doctor: %d repositor(y|ies) unhealthy — fix the FAIL findings above", unhealthy)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "check every registered workspace repository, each independently")
	cmd.Flags().BoolVar(&live, "live", false, "additionally run bounded provider probes (spawns provider processes; no paid model call)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the machine-readable report")
	cmd.Flags().BoolVar(&verbose, "verbose", false, "also list every workflow node/edge allocation (and force detail under --all)")
	cmd.Flags().DurationVar(&timeout, "timeout", doctor.DefaultLiveTimeout, "per-probe deadline for --live")
	register(cmd)
}

// scaffoldFindings adapts this package's harness-scaffold drift detector to the
// shared finding vocabulary. The detector stays here, beside the code that
// WRITES the canonical wrappers, so writer and checker cannot drift apart;
// doctor consumes it as an injected authority rather than importing cli.
func scaffoldFindings(repoRoot string) health.Findings {
	var out health.Findings
	for _, f := range DetectScaffoldDrift(repoRoot) {
		id := health.IDScaffoldStale
		title := "Stale harness scaffolding"
		if f.Kind == "missing" {
			id = health.IDScaffoldMissing
			title = "Missing harness scaffolding"
		}
		out = append(out, health.Error(id, title,
			fmt.Sprintf("%s [%s]: %s", f.Path, f.Kind, f.Detail)).
			About(f.Path).WithRemediation("run `satelle init` to heal"))
	}
	return out
}
