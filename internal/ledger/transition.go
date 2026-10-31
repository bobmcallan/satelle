package ledger

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"
)

// InFlightTTL bounds how long a transition_started row stays open when its
// process cannot be checked (another host) or is alive but stuck. It must exceed
// a long multi-gate dispatch.
const InFlightTTL = 30 * time.Minute

// TransitionStartedPayload is the payload of a KindTransitionStarted row.
type TransitionStartedPayload struct {
	From    string `json:"from"`
	To      string `json:"to"`
	Pid     int    `json:"pid,omitempty"`
	Host    string `json:"host,omitempty"`
	Attempt string `json:"attempt,omitempty"`
	Session string `json:"session,omitempty"`
}

// TransitionEndedPayload is the payload of a KindTransitionEnded row: Start is
// the id of the transition_started row it closes.
type TransitionEndedPayload struct {
	Start   string `json:"start"`
	Outcome string `json:"outcome,omitempty"`
}

// TransitionActivityPayload is the payload of a KindTransitionActivity row.
// Label/Index/Total name the phase; Agent/Model/Pid the dispatch running it.
type TransitionActivityPayload struct {
	Start string `json:"start"`
	Label string `json:"label,omitempty"`
	Index int    `json:"index,omitempty"`
	Total int    `json:"total,omitempty"`
	Agent string `json:"agent,omitempty"`
	Model string `json:"model,omitempty"`
	Pid   int    `json:"pid,omitempty"`
}

// StopRequestedPayload is the payload of a KindStopRequested row.
type StopRequestedPayload struct {
	By     string `json:"by"`
	Reason string `json:"reason,omitempty"`
}

// Transition is one open transition: its start row, and the latest phase and
// dispatch activity recorded against it.
type Transition struct {
	ID        string
	StoryID   string
	StartedAt time.Time
	TransitionStartedPayload
	// Label/Index/Total and PhaseAt are the latest phase; zero when none was
	// recorded.
	Label   string
	Index   int
	Total   int
	PhaseAt time.Time
	// Agent/Model/AgentPid and AgentAt are the latest dispatch; empty when no
	// dispatch was recorded.
	Agent    string
	Model    string
	AgentPid int
	AgentAt  time.Time
}

// PidAlive reports whether pid is a live process on this host. Overridable in
// tests.
var PidAlive = defaultPidAlive

func defaultPidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	if _, err := os.Stat(fmt.Sprintf("/proc/%d", pid)); err == nil {
		return true
	}
	// Without /proc a live process cannot be told from a dead one; treat it as
	// alive and let InFlightTTL close it.
	if _, err := os.Stat("/proc"); errors.Is(err, os.ErrNotExist) {
		return true
	}
	return false
}

// Hostname names this host on a transition_started row. Overridable in tests.
var Hostname = func() string {
	h, _ := os.Hostname()
	return h
}

// Open reports whether a started transition still counts as in flight at now:
// its process is alive when it ran on this host, and it is younger than
// InFlightTTL.
func (t Transition) Open(now time.Time) bool {
	if t.Pid > 0 && t.Host != "" && t.Host == Hostname() && !PidAlive(t.Pid) {
		return false
	}
	return now.Sub(t.StartedAt) <= InFlightTTL
}

// OpenTransitions folds transition rows (any order, any stories) into the open
// transition per story at now. A story with several open starts keeps the
// newest. Rows of other kinds are ignored.
func OpenTransitions(rows []Entry, now time.Time) map[string]Transition {
	out := map[string]Transition{}
	for _, t := range unended(rows) {
		if !t.Open(now) {
			continue
		}
		if cur, ok := out[t.StoryID]; ok && !t.StartedAt.After(cur.StartedAt) {
			continue
		}
		out[t.StoryID] = t
	}
	return out
}

// LatestUnended is the newest started transition among one story's rows that no
// transition_ended row closes, whether or not it is still open — what a
// recovery report names when a process died mid-transition.
func LatestUnended(rows []Entry) (Transition, bool) {
	var out Transition
	found := false
	for _, t := range unended(rows) {
		if !found || t.StartedAt.After(out.StartedAt) {
			out, found = t, true
		}
	}
	return out, found
}

// unended folds transition rows into every start no transition_ended row closes,
// with its latest activity.
func unended(rows []Entry) []Transition {
	sorted := append([]Entry(nil), rows...)
	SortEntries(sorted)
	starts := map[string]Transition{}
	ended := map[string]bool{}
	for _, e := range sorted {
		switch e.Kind {
		case KindTransitionStarted:
			var p TransitionStartedPayload
			if json.Unmarshal(e.Payload, &p) != nil {
				continue
			}
			starts[e.ID] = Transition{ID: e.ID, StoryID: e.StoryID, StartedAt: e.CreatedAt, TransitionStartedPayload: p}
		case KindTransitionEnded:
			var p TransitionEndedPayload
			if json.Unmarshal(e.Payload, &p) == nil && p.Start != "" {
				ended[p.Start] = true
			}
		}
	}
	for _, e := range sorted {
		if e.Kind != KindTransitionActivity {
			continue
		}
		var p TransitionActivityPayload
		if json.Unmarshal(e.Payload, &p) != nil {
			continue
		}
		t, ok := starts[p.Start]
		if !ok {
			continue
		}
		if p.Label != "" {
			t.Label, t.Index, t.Total, t.PhaseAt = p.Label, p.Index, p.Total, e.CreatedAt
		}
		if p.Agent != "" {
			t.Agent, t.Model, t.AgentPid, t.AgentAt = p.Agent, p.Model, p.Pid, e.CreatedAt
		}
		starts[p.Start] = t
	}
	var out []Transition
	for id, t := range starts {
		if !ended[id] {
			out = append(out, t)
		}
	}
	return out
}

// InFlight is OpenTransitions for one story's rows: the open transition, if any.
func InFlight(rows []Entry, now time.Time) (Transition, bool) {
	for _, t := range OpenTransitions(rows, now) {
		return t, true
	}
	return Transition{}, false
}

// transitionKinds are the rows OpenTransitions reads.
var transitionKinds = []string{KindTransitionStarted, KindTransitionEnded, KindTransitionActivity}

// transitionWindow is how many of each transition kind ListOpenTransitions
// reads. Only rows younger than InFlightTTL can be open, so the newest window
// is enough.
const transitionWindow = 500

// ListOpenTransitions reads the newest transition rows (for one story, or every
// story when storyID is empty) and returns the open transition per story.
func (s *Store) ListOpenTransitions(ctx context.Context, storyID string, now time.Time) (map[string]Transition, error) {
	rows, err := s.TransitionRows(ctx, storyID)
	if err != nil {
		return nil, err
	}
	return OpenTransitions(rows, now), nil
}

// TransitionRows reads the newest rows of the transition kinds, for one story or
// every story when storyID is empty.
func (s *Store) TransitionRows(ctx context.Context, storyID string) ([]Entry, error) {
	if s == nil {
		return nil, errors.New("ledger: store not configured")
	}
	var rows []Entry
	for _, kind := range transitionKinds {
		es, _, err := s.ListNewest(ctx, ListFilter{StoryID: storyID, Kind: kind, Limit: transitionWindow})
		if err != nil {
			return nil, err
		}
		rows = append(rows, es...)
	}
	return rows, nil
}
