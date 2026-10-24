// Package runstatus is the vocabulary of a team remote's run for a story and the
// seam a local driver reads it through. A node reports its run in these states
// (internal/remotenode writes them), satelle.dev holds them, and a step that
// waits on a remote (wfdot.RemoteWait) reads them back to show where the story
// stands and to take the edge its configuration maps the conclusion to.
//
// It is a leaf: it imports nothing from this module, so the node that writes the
// states and the drive that reads them can both depend on it.
package runstatus

import (
	"context"
	"fmt"
	"strings"
)

// The run states a node reports for its run.
const (
	Queued    = "queued"
	Running   = "running"
	Concluded = "concluded"
)

// Stopped is the outcome of a run that ended without recording one: the remote's
// host reports how it ended (end_status) and why, and the reader names the outcome.
const Stopped = "stopped"

// Run is a team remote's run for one story, as the remote's host reports it.
type Run struct {
	// ID names this run. A later run for the same story has another ID, which is
	// how a driver tells a conclusion it already acted on from a new one.
	ID string `json:"run_id,omitempty"`
	// ConcludedAt is when the run concluded (RFC 3339), once Concluded.
	ConcludedAt string `json:"concluded_at,omitempty"`
	// State is Queued, Running or Concluded.
	State string `json:"run_state"`
	// Position is the run's place in the remote's queue while it is Queued
	// (1 is next); 0 when the host does not say.
	Position int `json:"position,omitempty"`
	// Step is the step the run is at while Running.
	Step string `json:"step,omitempty"`
	// Outcome is how the run ended, once Concluded. Its vocabulary is the
	// remote's; the workflow maps it to an edge.
	Outcome string `json:"outcome,omitempty"`
	// Findings is what the run recorded about that outcome, once Concluded.
	Findings string `json:"findings,omitempty"`
}

// Key identifies the run's conclusion, so a driver can record that it acted on it
// and not act again. It is the run's ID and conclusion time together with the
// outcome and findings: an ID may name only the node that ran (not the run), so a
// later conclusion on the same ID must still differ when what it concluded does.
func (r Run) Key() string {
	return r.ID + "@" + r.ConcludedAt + "\x00" + r.Outcome + "\x00" + r.Findings
}

// Source reads a story's run with a named team remote.
type Source interface {
	// StoryRun returns the run the remote holds for the story. ok is false when
	// the remote has no run for it.
	StoryRun(ctx context.Context, remote, storyID string) (run Run, ok bool, err error)
}

// Render is the one line that says where the story stands with remote.
func Render(r Run, ok bool, remote string) string {
	if !ok {
		return "no run with " + remote
	}
	switch r.State {
	case Queued:
		if r.Position > 0 {
			return fmt.Sprintf("queued with %s (position %d)", remote, r.Position)
		}
		return "queued with " + remote
	case Running:
		if r.Step != "" {
			return fmt.Sprintf("running with %s at step %s", remote, r.Step)
		}
		return "running with " + remote
	case Concluded:
		if r.Outcome != "" {
			return fmt.Sprintf("concluded with %s: %s", remote, r.Outcome)
		}
		return "concluded with " + remote
	}
	return fmt.Sprintf("%s run with %s", strings.TrimSpace(r.State), remote)
}
