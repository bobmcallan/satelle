// `satelle remote create|validate|test` author, check and locally test-run an
// optional remote definition: a defined version of satelle, with its own
// workflows, bindings, host commands, identity label and triggers, held under
// .satelle/config/remotes/<name>/. Nothing else in satelle reads it, so a repo
// with no remote is untouched.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/agentstep"
	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/buildinfo"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/remotedef"
	"github.com/bobmcallan/satelle/internal/remotetest"
	"github.com/bobmcallan/satelle/internal/semver"
)

func init() {
	remote := &cobra.Command{
		Use:   "remote",
		Short: "Create, validate and test-run an optional remote definition (a defined version of satelle that runs elsewhere)",
		Long: `A remote is a defined version of satelle: a pinned satelle version, its own
workflows, skills, principles and agent bindings, the commands that reach its
host, the identity label that host runs under, the lifecycle segments it serves
and its triggers. It lives in .satelle/config/remotes/<name>/ and is read only
from there — never from this repo's .satelle/workflows.

The definition holds no secret value: it names the .env keys
(.satelle/config/.env) its agents may read, and a ${VAR} reference where a
credential-shaped key needs one. Host commands, the identity label and trigger
kinds are opaque to satelle. Local satelle needs no remote; with none defined,
nothing here runs.`,
	}

	create := &cobra.Command{
		Use:   "create <name>",
		Short: "Scaffold a new remote definition and validate it",
		Long: `Writes .satelle/config/remotes/<name>/: a remote.toml and the remote's own
workflows (done.toml, step.toml, agents.toml), a skill and a principle. Every
host command, label and agent command in it is a placeholder to replace. Refuses
a name that already exists.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if !remotedef.ValidName(name) {
				return fmt.Errorf("remote name %q must be lower-case letters, digits, - or _, starting with a letter or digit", name)
			}
			dir := remotedef.Dir(remoteDataDir(), name)
			version := "0.0.0"
			if v := buildinfo.Resolve().Version; semver.Valid(v) {
				version = v
			}
			if err := remotedef.Scaffold(dir, version); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "created %s/\n", filepath.ToSlash(filepath.Join(config.DefaultDataDir, config.RemotesDir, name)))
			return reportRemote(cmd, name, dir)
		},
	}

	validate := &cobra.Command{
		Use:   "validate [<name>]",
		Short: "Check one remote definition, or every one, naming each error by file and key",
		Long: `Checks the named remote, or every remote when no name is given. Every error is
reported in one run, each naming its file (relative to the remote's directory)
and its key. Exits non-zero when any remote has an error.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dataDir := remoteDataDir()
			names := args
			if len(args) == 0 {
				all, err := remotedef.Names(dataDir)
				if err != nil {
					return err
				}
				if len(all) == 0 {
					fmt.Fprintln(cmd.OutOrStdout(), "no remotes defined")
					return nil
				}
				names = all
			} else {
				if !remotedef.ValidName(args[0]) {
					return fmt.Errorf("remote name %q must be lower-case letters, digits, - or _, starting with a letter or digit", args[0])
				}
				if _, err := os.Stat(remotedef.Dir(dataDir, args[0])); err != nil {
					return fmt.Errorf("no remote %q under %s/%s", args[0], config.DefaultDataDir, config.RemotesDir)
				}
			}
			failed := 0
			for _, n := range names {
				if err := reportRemote(cmd, n, remotedef.Dir(dataDir, n)); err != nil {
					failed++
				}
			}
			if failed > 0 {
				return fmt.Errorf("%d of %d remote(s) failed validation", failed, len(names))
			}
			return nil
		},
	}

	var testStory, testBase string
	var testJSON bool
	test := &cobra.Command{
		Use:         "test <name> --story <id> --base <ref>",
		Short:       "Run a remote definition's own workflow locally on a sample story, before it is published",
		Annotations: needsStore(),
		Long: `Walks every segment of the remote's own workflow on the sample story: each
edge's performer runs, then its reviewers judge it, in a scratch worktree cut
from --base (required; never defaults to HEAD) and removed afterwards. The
remote's own bindings really run, so this spends tokens.

Host commands are shown, never executed. The story is not written, and a cloud
binding is refused before anything runs. One remote_test ledger row per edge.

Each edge prints from→to, skill, binding, host identity and a verdict: accept or
reject (reviewers), performed (no reviewer), no-op (neither). Exits non-zero when
the definition cannot run, with the reasons, or an edge is rejected.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRemoteTest(cmd, args[0], testStory, testBase, testJSON)
		},
	}
	test.Flags().StringVar(&testStory, "story", "", "the sample story to run the definition on (required)")
	test.Flags().StringVar(&testBase, "base", "", "the ref the scratch worktree is cut from (required; never defaults to HEAD)")
	test.Flags().BoolVar(&testJSON, "json", false, "print the report as JSON")
	_ = test.MarkFlagRequired("story")
	_ = test.MarkFlagRequired("base")

	remote.AddCommand(create, validate, test)
	remote.AddCommand(remoteStartCommands()...)
	register(remote)
}

// remoteTestNewRunner and remoteTestReviewerRunner replace the agent CLIs a test
// run starts. Tests only: nil keeps the real ones.
var (
	remoteTestNewRunner      func(iface, command string) (agentcli.Runner, error)
	remoteTestReviewerRunner agentcli.Runner
)

func runRemoteTest(cmd *cobra.Command, name, storyID, base string, asJSON bool) error {
	if !remotedef.ValidName(name) {
		return fmt.Errorf("remote name %q must be lower-case letters, digits, - or _, starting with a letter or digit", name)
	}
	dir := remotedef.Dir(remoteDataDir(), name)
	if _, err := os.Stat(dir); err != nil {
		return fmt.Errorf("no remote %q under %s/%s", name, config.DefaultDataDir, config.RemotesDir)
	}
	a, err := appFrom(cmd)
	if err != nil {
		return err
	}
	item, err := a.Store.Stories.Get(cmd.Context(), storyID)
	if err != nil {
		return fmt.Errorf("sample story %q: %w", storyID, err)
	}
	rep, runErr := remotetest.Run(cmd.Context(), name, dir, item, remotetest.Options{
		Base:           base,
		MainRoot:       a.ProcessRoot,
		Include:        a.PlaneConfig().Worktree.Include,
		NewEngine:      remoteTestEngine(a),
		NewRunner:      remoteTestNewRunner,
		ReviewerRunner: remoteTestReviewerRunner,
		StoryDocs:      docsResolver(a),
		Ledger: func(ctx context.Context, in ledger.AppendInput) error {
			_, err := a.Store.Ledger.Append(ctx, in, time.Now())
			return err
		},
	})
	return printRemoteTest(cmd, rep, runErr, asJSON)
}

// remoteTestEngine builds the engine a test run walks on, the way a normal
// dispatch builds it but from the REMOTE's own agents layer: the wiring guard,
// the reviewer binding and the named bindings. The local repo's constitution is
// not carried — a remote has its own principles and no other process.
func remoteTestEngine(a *app.App) func(agentstep.DocGetter, string, config.AgentsConfig) (*agentstep.Engine, error) {
	return func(docs agentstep.DocGetter, root string, agents config.AgentsConfig) (*agentstep.Engine, error) {
		rev := agentstep.New(nil, docs, root, "")
		if err := applyAgentGrants(rev, a, agents); err != nil {
			return nil, err
		}
		rev.SetConstitution("")
		rev.SetNamedAgents(agents.NamedBinding)
		rev.SetLiveNamedAgents(agents.LiveBinding)
		rev.SetDefaultBudget(agents.Defaults.Budget())
		rev.SetIdleTimeoutResolver(func(_ string, b config.AgentBinding) (time.Duration, error) {
			return agents.ResolveIdleTimeout(b, agentstep.DefaultIdleTimeout)
		})
		rev.SetBusyTimeoutResolver(func(_ string, b config.AgentBinding) (time.Duration, error) {
			return agents.ResolveBusyTimeout(b, config.DefaultBusyTimeout)
		})
		return rev, nil
	}
}

// printRemoteTest prints a run's report and returns its error. A definition that
// could not run prints each reason; a run prints one block per edge it ran.
func printRemoteTest(cmd *cobra.Command, rep remotetest.Report, runErr error, asJSON bool) error {
	out := cmd.OutOrStdout()
	if asJSON {
		b, err := json.MarshalIndent(rep, "", "  ")
		if err != nil {
			return err
		}
		fmt.Fprintln(out, string(b))
		return runErr
	}
	printPreflight(out, rep)
	var refused *remotetest.RefusedError
	if errors.As(runErr, &refused) {
		for _, r := range refused.Reasons {
			fmt.Fprintf(out, "FAIL  remote %s: %s\n", rep.Remote, r)
		}
		return runErr
	}
	fmt.Fprintf(out, "remote %s on %s (identity %s)\n", rep.Remote, rep.Story, rep.Identity)
	for _, s := range rep.Steps {
		fmt.Fprintf(out, "  %s→%s  skill=%s  binding=%s  identity=%s  verdict=%s\n",
			s.From, s.To, dash(s.Skill), dash(s.Binding), s.Identity, s.Verdict)
		if s.Reviewer != "" {
			fmt.Fprintf(out, "      reviewed by %s\n", s.Reviewer)
		}
		if s.OnHost {
			fmt.Fprintf(out, "      would run on the host as: %s  (not run)\n", s.HostCommand)
		}
		if s.Notes != "" {
			fmt.Fprintf(out, "      notes: %s\n", s.Notes)
		}
	}
	return runErr
}

// printPreflight prints one line per preflight check, whether it passed or not.
func printPreflight(out io.Writer, rep remotetest.Report) {
	if len(rep.Preflight) == 0 {
		return
	}
	fmt.Fprintf(out, "preflight for remote %s\n", rep.Remote)
	for _, c := range rep.Preflight {
		verdict := "pass"
		if !c.Pass {
			verdict = "FAIL"
		}
		fmt.Fprintf(out, "  %s  %s  expect=%s  %s  %s  %s\n",
			dash(c.Step), c.Kind, dash(c.Expect), c.Outcome, verdict, strings.Join(c.Argv, " "))
		if c.Detail != "" {
			fmt.Fprintf(out, "      %s\n", c.Detail)
		}
	}
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// remoteDataDir is this repo's data dir, where remotes are kept.
func remoteDataDir() string {
	return filepath.Join(initRepoRoot(""), config.DefaultDataDir)
}

// reportRemote validates one remote, prints one line per finding (or a single
// line when it is clean) and returns an error when there are findings.
func reportRemote(cmd *cobra.Command, name, dir string) error {
	out := cmd.OutOrStdout()
	findings := remotedef.Validate(dir)
	if len(findings) == 0 {
		fmt.Fprintf(out, "remote %s: valid\n", name)
		return nil
	}
	for _, f := range findings {
		fmt.Fprintf(out, "FAIL  remote %s: %s\n", name, f.Detail)
	}
	return fmt.Errorf("remote %s: %d error(s)", name, len(findings))
}
