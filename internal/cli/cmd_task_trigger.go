package cli

import (
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/drive"
	"github.com/bobmcallan/satelle/internal/tasktrigger"
	"github.com/bobmcallan/satelle/internal/workitem"
)

const taskTriggerLong = `A task declares local triggers on the triggers: line of its file, a comma list of

  name=schedule:<duration>     fires when it has made no execution within <duration> (e.g. 24h)
  name=count:<status>>=<n>     fires when at least n stories sit at <status> and no
                               execution it made is still running

Each firing creates one execution of the task, tagged trigger:<task>:<name>, with
the trigger's name and source (schedule|count) in its trigger and trigger_source
inputs when the task declares them, and drives it along the task's route in this
process. No placement or remote is consulted.`

func taskTriggerCommand() *cobra.Command {
	parent := &cobra.Command{
		Use:   "trigger",
		Short: "Run the local triggers tasks declare (schedule or story-state condition)",
		Long:  taskTriggerLong,
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Help()
			}
			return fmt.Errorf("unknown command %q for %q", args[0], cmd.CommandPath())
		},
	}
	runOnce := &cobra.Command{
		Use:   "run-once",
		Short: "Evaluate every task's triggers once and run an execution for each that fires",
		Long: taskTriggerLong + `

run-once is one pass, for an OS timer (cron, a systemd timer) or a test to call.
Exit 1 when a firing's execution could not be made or driven.`,
		Args:        cobra.NoArgs,
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if handled, herr := handOffGate(cmd, "task-trigger-run-once", ""); handled {
				return herr
			}
			opts, err := triggerDriveOptions(cmd)
			if err != nil {
				return err
			}
			return runTriggerPass(cmd, opts)
		},
	}
	var every time.Duration
	watch := &cobra.Command{
		Use:   "watch",
		Short: "Run a trigger pass every --every until interrupted",
		Long: taskTriggerLong + `

watch runs run-once's pass, then again every --every, until interrupted. A pass
that fails is reported and the next one still runs.`,
		Args:        cobra.NoArgs,
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if every <= 0 {
				return fmt.Errorf("--every must be a positive duration")
			}
			if handled, herr := handOffGate(cmd, "task-trigger-watch", ""); handled {
				return herr
			}
			opts, err := triggerDriveOptions(cmd)
			if err != nil {
				return err
			}
			tick := time.NewTicker(every)
			defer tick.Stop()
			for {
				if err := runTriggerPass(cmd, opts); err != nil {
					fmt.Fprintln(cmd.ErrOrStderr(), err)
				}
				select {
				case <-cmd.Context().Done():
					return nil
				case <-tick.C:
				}
			}
		},
	}
	watch.Flags().DurationVar(&every, "every", 0, "how often to run a pass (e.g. 5m); required")
	_ = watch.MarkFlagRequired("every")
	parent.AddCommand(runOnce, watch)
	return parent
}

// triggerDriveOptions is the drive a firing's execution runs with: the one
// `story drive` uses. Like it, the command is handed off whole to a detached run
// when an agent calls it; a pass has no single subject story.
func triggerDriveOptions(cmd *cobra.Command) (drive.Options, error) {
	eng, a, err := engineForCmd(cmd)
	if err != nil {
		return drive.Options{}, err
	}
	return driveOptions(cmd, eng, a, cmd.OutOrStdout()), nil
}

// runTriggerPass runs one evaluation and prints a line per firing.
func runTriggerPass(cmd *cobra.Command, opts drive.Options) error {
	out := cmd.OutOrStdout()
	fired, err := tasktrigger.Evaluate(cmd.Context(), time.Now(), opts)
	failed := printFired(out, fired)
	if err != nil {
		return err
	}
	if len(fired) == 0 {
		fmt.Fprintln(out, "no trigger fired")
	}
	if failed > 0 {
		return &ExitError{Code: 1, Err: fmt.Errorf("%d trigger firing(s) failed", failed), Quiet: true}
	}
	return nil
}

// printFired writes one line per firing and returns how many failed.
func printFired(out io.Writer, fired []tasktrigger.Fired) int {
	failed := 0
	for _, f := range fired {
		src := f.Task + " trigger " + workitem.FormatTriggers([]workitem.TriggerDecl{f.Trigger})
		var stopped *drive.Stopped
		switch {
		case f.Err != nil && errors.As(f.Err, &stopped):
			failed++
			fmt.Fprintf(out, "fired %s -> %s\n", src, stopped.Error())
		case f.Err != nil && f.Execution == "":
			failed++
			fmt.Fprintf(out, "fired %s -> no execution: %v\n", src, f.Err)
		case f.Err != nil:
			failed++
			fmt.Fprintf(out, "fired %s -> %s: %v\n", src, f.Execution, f.Err)
		default:
			fmt.Fprintf(out, "fired %s -> %s\n", src, driveSummary(f.Drive))
		}
	}
	return failed
}
