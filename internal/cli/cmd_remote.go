// `satelle remote create|validate` author and check an optional remote
// definition: a defined version of satelle, with its own workflows, bindings,
// host commands, identity label and triggers, held under
// .satelle/config/remotes/<name>/. Nothing else in satelle reads it, so a repo
// with no remote is untouched.
package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/buildinfo"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/remotedef"
	"github.com/bobmcallan/satelle/internal/semver"
)

func init() {
	remote := &cobra.Command{
		Use:   "remote",
		Short: "Create and validate an optional remote definition (a defined version of satelle that runs elsewhere)",
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

	remote.AddCommand(create, validate, newRemoteTriggerCmd(), newRemoteWakeCmd(), newRemoteRetireCmd())
	register(remote)
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
