// `satelle agents launch <harness> -- args` is what a launcher installed by
// `satelle agents install` runs (sty_b61d2066, epic:agent-cloud-identity). It
// signs the session in as the cloud identity the repo's in-loop agent profile
// names, then REPLACES itself with the harness: the process is exec'd, never
// detached, so the harness is the same process the operator started.
package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/cloudid"
	"github.com/bobmcallan/satelle/internal/config"
)

// errCloudConfigUnloadable marks a repo or catalog that could not be read, as
// distinct from an identity that is named but cannot be resolved. The first
// launches as today; the second refuses to start.
var errCloudConfigUnloadable = errors.New("configuration could not be loaded")

// launchExec replaces the process with the harness. A seam so tests observe the
// final environment and argv without exec'ing the test binary away.
var launchExec = func(path string, argv, env []string) error {
	return syscall.Exec(path, argv, env)
}

// launchEnv is the process environment a launch composes onto.
var launchEnv = os.Environ

// cloudSelectionAt resolves the in-loop cloud identity of the repo whose
// process data dir and root are given. A load failure wraps
// errCloudConfigUnloadable.
func cloudSelectionAt(dataDir, root string) (config.CloudSelection, bool, error) {
	eff, err := config.LoadEffectiveAgents(dataDir, nil)
	if err != nil {
		return config.CloudSelection{}, false, fmt.Errorf("%w: %v", errCloudConfigUnloadable, err)
	}
	global, err := config.LoadGlobalAgents()
	if err != nil {
		return config.CloudSelection{}, false, fmt.Errorf("%w: %v", errCloudConfigUnloadable, err)
	}
	return config.InLoopCloudIdentity(eff, global, config.LocalVarsAt(root))
}

// cloudSelectionFromCwd is cloudSelectionAt for the repo containing the working
// directory, without opening the store (a launch must stay cheap and must not
// write into a repo). Outside a satelle repo there is nothing to select.
func cloudSelectionFromCwd() (config.CloudSelection, bool, error) {
	loc, locPath, err := config.Load("")
	if err != nil {
		if errors.Is(err, config.ErrNotFound) {
			// Zero-config is supported: a repo may carry .satelle/ and no
			// satelle.toml. Outside any .satelle/ there is nothing to select.
			dir, found := config.FindDataDir("")
			if !found {
				return config.CloudSelection{}, false, nil
			}
			return cloudSelectionAt(dir, filepath.Dir(dir))
		}
		return config.CloudSelection{}, false, fmt.Errorf("%w: %v", errCloudConfigUnloadable, err)
	}
	proc, root, _, err := config.LoadProcess(loc, locPath)
	if err != nil {
		return config.CloudSelection{}, false, fmt.Errorf("%w: %v", errCloudConfigUnloadable, err)
	}
	return cloudSelectionAt(config.ResolveProcessDataDir(proc, root), root)
}

// launchPlan is what planLaunch decides to exec.
type launchPlan struct {
	Binary string
	Args   []string
	Env    []string
}

// planLaunch composes the launch of harness: the harness environment plus, when
// the repo's in-loop profile names a cloud identity, the sign-in each provider
// adapter performs. Repo or catalog config that cannot load launches the harness
// as today (stderr says why the identity was skipped); an identity that is named
// but cannot be resolved or signed in refuses, because launching as the
// operator's own identity instead would be the wrong identity, silently.
func planLaunch(ctx context.Context, harness string, args []string, stderr func(string)) (launchPlan, error) {
	if reason := agentcli.CloudIdentityUnavailable(harness); reason != "" {
		return launchPlan{}, fmt.Errorf("cloud identity: unavailable (%s)", reason)
	}
	harness = strings.ToLower(strings.TrimSpace(harness))
	plan := launchPlan{Binary: agentcli.LauncherBinary(harness), Args: args}
	set := map[string]string{config.LauncherEnv: harness}
	var unset []string

	sel, ok, err := cloudSelectionFromCwd()
	switch {
	case err != nil && errors.Is(err, errCloudConfigUnloadable):
		stderr(fmt.Sprintf("satelle: cloud identity not applied — %v", err))
		ok = false
	case err != nil:
		return launchPlan{}, fmt.Errorf("cloud identity for profile %q: %w", sel.Profile, err)
	}
	if ok {
		stateDir := filepath.Join(config.GlobalDir(), "cloud", sel.Profile)
		for _, name := range sel.ProviderNames() {
			p, found := cloudid.Lookup(name)
			if !found {
				return launchPlan{}, fmt.Errorf("cloud identity: no adapter for provider %q", name)
			}
			ov, err := p.Prepare(ctx, sel.Profile, sel.Providers[name], stateDir)
			if err != nil {
				return launchPlan{}, fmt.Errorf("cloud identity for profile %q: %w", sel.Profile, err)
			}
			for k, v := range ov.Set {
				set[k] = v
			}
			unset = append(unset, ov.Unset...)
		}
		set[config.CloudProfileEnv] = sel.Profile
	} else {
		// A marker inherited from an enclosing launched session would claim an
		// identity this one does not have.
		unset = append(unset, config.CloudProfileEnv)
	}
	plan.Env = agentcli.ComposeLaunchEnv(launchEnv(), set, unset)
	return plan, nil
}

func newAgentsLaunchCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "launch <" + strings.Join(agentcli.LauncherHarnesses(), "|") + "> [-- harness args...]",
		Short: "Start a harness as the cloud identity its agent profile names (run by the satelle launchers)",
		Long: `Run by the launchers from 'satelle agents install'. Signs the session in as the
cloud identity the repo's in-loop profile names, then replaces itself with the
harness. Setup: satelle help global-agents.

satelle never puts the long-lived secret in the agent's environment, argv, repo
or logs. What each provider keeps:
` + cloudid.PersistenceHelp() + config.CloudIdentityLimits,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			plan, err := planLaunch(cmd.Context(), args[0], args[1:], func(s string) {
				fmt.Fprintln(cmd.ErrOrStderr(), s)
			})
			if err != nil {
				return err
			}
			path, err := exec.LookPath(plan.Binary)
			if err != nil {
				return fmt.Errorf("launch: %s not found on PATH", plan.Binary)
			}
			return launchExec(path, append([]string{plan.Binary}, plan.Args...), plan.Env)
		},
	}
}

// newAgentsCredentialCmd is the hidden `agents credential aws <profile>`: the
// credential_process the satelle-owned AWS config names. It prints STS SESSION
// credentials as JSON and nothing else; the long-lived keys are resolved in
// memory, handed to one aws child through its environment, and never printed.
func newAgentsCredentialCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "credential aws <profile>",
		Short:  "Print AWS session credentials for a profile (credential_process; not for direct use)",
		Hidden: true,
		Args:   cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if args[0] != "aws" {
				return fmt.Errorf("credential: provider %q mints no session credentials (want aws)", args[0])
			}
			global, err := config.LoadGlobalAgents()
			if err != nil {
				return err
			}
			var local map[string]string
			if _, locPath, lerr := config.Load(""); lerr == nil {
				local = config.LocalVarsAt(config.InvokingRoot(locPath))
			} else if dir, found := config.FindDataDir(""); found {
				local = config.LocalVarsAt(filepath.Dir(dir))
			}
			sel, ok, err := config.ResolveCloudProfile(global, args[1], local)
			if err != nil {
				return err
			}
			table := sel.Providers["aws"]
			if !ok || table == nil {
				return fmt.Errorf("credential: profile %q carries no aws cloud identity", args[1])
			}
			out, err := cloudid.SessionCredentials(cmd.Context(), args[1], table)
			if err != nil {
				return err
			}
			_, err = cmd.OutOrStdout().Write(append(out, '\n'))
			return err
		},
	}
}
