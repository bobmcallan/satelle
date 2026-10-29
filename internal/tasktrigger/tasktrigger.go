// Package tasktrigger runs a task's local triggers: the schedule and story-state
// conditions its file declares (workitem.TriggerDecl). One pass reads every task,
// and for each trigger that is due it creates one execution of the task, tagged
// with the trigger and carrying it as inputs, and drives that execution through
// the task's own route in this process. Nothing here consults placement or a
// remote: a trigger is local by construction.
package tasktrigger

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/bobmcallan/satelle/internal/drive"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// Fired is one trigger that fired in a pass.
type Fired struct {
	Task    string
	Trigger workitem.TriggerDecl
	// Execution is the execution the firing made; empty when it could not be made.
	Execution string
	// Drive is where the execution's drive stopped.
	Drive drive.Result
	// Err is why the execution could not be made or driven. One firing failing
	// does not stop the pass.
	Err error
}

// Evaluate runs one pass at now: every trigger of every task that is due fires
// one execution, driven with opts. An error is a pass that could not read the
// tasks or the state a trigger judges; a firing's own failure is on its Fired.
func Evaluate(ctx context.Context, now time.Time, opts drive.Options) ([]Fired, error) {
	var tasks []workitem.Item
	if err := dispatchJSON(ctx, "task-list", map[string]any{}, &tasks); err != nil {
		return nil, err
	}
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].ID < tasks[j].ID })
	var fired []Fired
	for _, task := range tasks {
		for _, trig := range task.Triggers {
			due, err := isDue(ctx, task, trig, now)
			if err != nil {
				return fired, fmt.Errorf("%s trigger %s: %w", task.ID, trig.Name, err)
			}
			if due {
				fired = append(fired, fire(ctx, task, trig, opts))
			}
		}
	}
	return fired, nil
}

// isDue reports whether trig fires now. A schedule fires when it has made no
// execution within its interval. A count fires when its threshold of stories sit
// at its status and no execution it made is still running, so a condition that
// holds across passes runs again only after the previous run has concluded.
func isDue(ctx context.Context, task workitem.Item, trig workitem.TriggerDecl, now time.Time) (bool, error) {
	var made []workitem.Item
	if err := dispatchJSON(ctx, "execution-list", map[string]any{"parent_id": task.ID, "tag": trig.Tag(task.ID)}, &made); err != nil {
		return false, err
	}
	switch trig.Kind {
	case workitem.TriggerSchedule:
		for _, e := range made {
			if now.Sub(e.CreatedAt) < trig.Every {
				return false, nil
			}
		}
		return true, nil
	case workitem.TriggerCount:
		for _, e := range made {
			if !verb.EndsEngagementForGood(ctx, e, e.Status) {
				return false, nil
			}
		}
		// The list verb caps its page, so a threshold above that cap never fires.
		var at []workitem.Item
		if err := dispatchJSON(ctx, "story-list", map[string]any{"status": trig.Status, "limit": trig.AtLeast}, &at); err != nil {
			return false, err
		}
		return len(at) >= trig.AtLeast, nil
	}
	return false, fmt.Errorf("unknown trigger kind %q", trig.Kind)
}

// fire creates the trigger's execution of task and drives it.
func fire(ctx context.Context, task workitem.Item, trig workitem.TriggerDecl, opts drive.Options) Fired {
	f := Fired{Task: task.ID, Trigger: trig}
	var exec workitem.Item
	f.Err = dispatchJSON(ctx, "execution-create", map[string]any{
		"title": trig.Name + " trigger of " + task.Title,
		"body": fmt.Sprintf("One run of %s fired by its local trigger %q, source %s (%s).",
			task.ID, trig.Name, trig.Kind, workitem.FormatTriggers([]workitem.TriggerDecl{trig})),
		"category":  workitem.RunCategory,
		"parent_id": task.ID,
		"tags":      []string{trig.Tag(task.ID)},
		"inputs": workitem.MapInputs(task.Inputs, map[string]string{
			workitem.InputTrigger: trig.Name, workitem.InputTriggerSource: trig.Kind,
		}),
	}, &exec)
	if f.Err != nil {
		return f
	}
	f.Execution = exec.ID
	f.Drive, f.Err = drive.Story(ctx, exec.ID, opts)
	return f
}

var dispatchJSON = verb.DispatchJSON
