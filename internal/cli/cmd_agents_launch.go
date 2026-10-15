// `satelle agents launch <harness> -- args` is what a launcher installed by
// `satelle agents install` runs (sty_b8310408, epic:agent-cloud-identity). It
// runs the launch steps the repo's in-loop agent profile declares, then
// REPLACES itself with the harness: the process is exec'd, never detached, so
// the harness is the same process the operator started. satelle knows no tool
// a step runs; what the steps do is the profile's configuration.
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
	"github.com/bobmcallan/satelle/internal/config"
)

// errLaunchConfigUnloadable marks a repo or catalog that could not be read, as
// distinct from steps that are declared but cannot be resolved or run. The first
// launches as today; the second refuses to start.
var errLaunchConfigUnloadable = errors.New("configuration could not be loaded")

// launchStepError is a launch step that failed. Its message names only the step
// index and the base name of its command — never its argv or environment, which
// may carry a secret — and the process exits with the step's own status.
type launchStepError struct {
	index int
	name  string
	code  int
}

func (e *launchStepError) Error() string {
	return fmt.Sprintf("launch: step %d (%s) exited %d", e.index, e.name, e.code)
}

// launchExec replaces the process with the harness. A seam so tests observe the
// final environment and argv without exec'ing the test binary away.
var launchExec = func(path string, argv, env []string) error {
	return syscall.Exec(path, argv, env)
}

// launchEnv is the process environment a launch composes onto.
var launchEnv = os.Environ

// runLaunchStep runs one launch step. A seam: TestMain replaces it with a
// refusal, so a real step never runs under `go test`; a test that drives fake
// step scripts opts in to realRunLaunchStep for its own duration.
var runLaunchStep = realRunLaunchStep

// realRunLaunchStep runs one launch step to completion, in dir, with exactly
// env, and stdio inherited so an interactive sign-in can prompt.
func realRunLaunchStep(ctx context.Context, dir string, argv, env []string) error {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir, cmd.Env = dir, env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

// launchSelectionAt resolves the in-loop launch steps of the repo whose process
// data dir and root are given. A load failure wraps errLaunchConfigUnloadable.
func launchSelectionAt(dataDir, root string) (config.LaunchSelection, bool, error) {
	eff, err := config.LoadEffectiveAgents(dataDir, nil)
	if err != nil {
		return config.LaunchSelection{}, false, fmt.Errorf("%w: %v", errLaunchConfigUnloadable, err)
	}
	global, err := config.LoadGlobalAgents()
	if err != nil {
		return config.LaunchSelection{}, false, fmt.Errorf("%w: %v", errLaunchConfigUnloadable, err)
	}
	return config.InLoopLaunchSteps(eff, global, config.LocalVarsAt(root))
}

// launchSelectionFromCwd is launchSelectionAt for the repo containing the
// working directory, without opening the store (a launch must stay cheap and
// must not write into a repo). Outside a satelle repo there is nothing to select.
func launchSelectionFromCwd() (config.LaunchSelection, string, bool, error) {
	loc, locPath, err := config.Load("")
	if err != nil {
		if errors.Is(err, config.ErrNotFound) {
			// Zero-config is supported: a repo may carry .satelle/ and no
			// satelle.toml. Outside any .satelle/ there is nothing to select.
			dir, found := config.FindDataDir("")
			if !found {
				return config.LaunchSelection{}, "", false, nil
			}
			root := filepath.Dir(dir)
			sel, ok, err := launchSelectionAt(dir, root)
			return sel, root, ok, err
		}
		return config.LaunchSelection{}, "", false, fmt.Errorf("%w: %v", errLaunchConfigUnloadable, err)
	}
	proc, root, _, err := config.LoadProcess(loc, locPath)
	if err != nil {
		return config.LaunchSelection{}, "", false, fmt.Errorf("%w: %v", errLaunchConfigUnloadable, err)
	}
	sel, ok, err := launchSelectionAt(config.ResolveProcessDataDir(proc, root), root)
	return sel, root, ok, err
}

// launchPlan is what planLaunch decides to exec.
type launchPlan struct {
	Binary string
	Args   []string
	Env    []string
}

// planLaunch composes the launch of harness: it runs the steps the repo's
// in-loop profile declares, in order, then returns the harness environment.
// Repo or catalog config that cannot load launches the harness as today (stderr
// says why the steps were skipped); steps that are declared but cannot be
// resolved or that fail refuse, because starting the harness without them would
// run it in the wrong environment, silently.
func planLaunch(ctx context.Context, harness string, args []string, stderr func(string)) (launchPlan, error) {
	if reason := agentcli.LaunchStepsUnavailable(harness); reason != "" {
		return launchPlan{}, fmt.Errorf("launch steps: unavailable (%s)", reason)
	}
	harness = strings.ToLower(strings.TrimSpace(harness))
	plan := launchPlan{Binary: agentcli.LauncherBinary(harness), Args: args}
	set := map[string]string{config.LauncherEnv: harness}
	// The 0.0.672 marker name is always cleared: an enclosing session's value
	// would claim steps this launch did not run.
	unset := []string{config.LegacyLaunchProfileEnv}

	sel, root, ok, err := launchSelectionFromCwd()
	switch {
	case err != nil && errors.Is(err, errLaunchConfigUnloadable):
		stderr(fmt.Sprintf("satelle: launch steps not applied — %v", err))
		ok = false
	case err != nil:
		return launchPlan{}, fmt.Errorf("launch steps for profile %q: %w", sel.Profile, err)
	}
	if ok {
		if err := os.MkdirAll(sel.Dir, 0o700); err != nil {
			return launchPlan{}, fmt.Errorf("launch steps for profile %q: %w", sel.Profile, err)
		}
		for i, step := range sel.Steps {
			env := composeStepEnv(launchEnv(), sel.Dir, step)
			if err := runLaunchStep(ctx, root, step.Run, env); err != nil {
				return launchPlan{}, stepFailure(i, step.Run[0], err)
			}
		}
		for k, v := range sel.Set {
			set[k] = v
		}
		set[config.ProfileDirVar] = sel.Dir
		set[config.LaunchProfileEnv] = sel.Profile
		unset = append(unset, sel.Unset...)
	} else {
		// A marker inherited from an enclosing launched session would claim steps
		// this one did not run.
		unset = append(unset, config.LaunchProfileEnv)
	}
	plan.Env = agentcli.ComposeLaunchEnv(launchEnv(), set, unset)
	return plan, nil
}

// composeStepEnv is a step's process environment: the launcher's own, the
// profile directory, the step's literal env, and last its resolved secrets. The
// secrets reach this process alone; the harness environment is composed apart.
func composeStepEnv(base []string, dir string, step config.ResolvedStep) []string {
	overlay := map[string]string{config.ProfileDirVar: dir}
	for k, v := range step.Env {
		overlay[k] = v
	}
	for k, v := range step.Secrets {
		overlay[k] = v
	}
	return agentcli.ComposeLaunchEnv(base, overlay, nil)
}

// stepFailure maps a step's error to the one launchStepError a launch reports.
// A command that could not be started exits 127, as a shell would.
func stepFailure(index int, command string, err error) *launchStepError {
	code := 127
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if code = ee.ExitCode(); code <= 0 {
			code = 1
		}
	}
	return &launchStepError{index: index, name: filepath.Base(command), code: code}
}

func newAgentsLaunchCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "launch <" + strings.Join(agentcli.LauncherHarnesses(), "|") + "> [-- harness args...]",
		Short: "Start a harness after running the launch steps its agent profile declares (run by the satelle launchers)",
		Long: `Run by the launchers from 'satelle agents install'. Runs the launch steps the
repo's in-loop profile declares, in order, then replaces itself with the
harness. With nothing declared it just starts the harness. Setup and examples:
satelle help global-agents.

A step's secrets reach that step's process only: satelle never places a [vars]
secret in the harness's environment or argv, in the repo or in logs.

` + config.LaunchStepLimits,
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
