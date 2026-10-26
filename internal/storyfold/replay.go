package storyfold

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/wfdot"
	"github.com/bobmcallan/satelle/internal/wfgovern"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// Replay break reasons.
const (
	// ReplayNoVersion: the transition records a workflow hash the chain holds no
	// workflow_version event for, so the edge cannot be checked against its version.
	ReplayNoVersion = "no_workflow_version"
	// ReplayBadVersion: the archived source of the version does not hash to it, or
	// does not derive a route for the story.
	ReplayBadVersion = "bad_workflow_version"
	// ReplayNoEdge: the workflow version the transition recorded declares no edge
	// from its source to its destination.
	ReplayNoEdge = "no_edge"
	// ReplayStaleFrom: the transition's from is not the status the chain had
	// reached at its place — an event made before the chain moved on (an earlier
	// attempt's, pulled late) or linked out of time order. The fold skips it only
	// when the story had already ended (ReplayBreak.Skipped).
	ReplayStaleFrom = "stale_from"
)

// ReplayBreak is one transition that does not replay against the workflow version
// its event recorded.
type ReplayBreak struct {
	StoryID      string `json:"story_id"`
	EventID      string `json:"event_id"`
	From         string `json:"from"`
	To           string `json:"to"`
	WorkflowHash string `json:"workflow_hash"`
	Reason       string `json:"reason"`
	Detail       string `json:"detail"`
	// Skipped is set on a ReplayStaleFrom break the fold ignored; false means the
	// transition applied out of order.
	Skipped bool `json:"skipped,omitempty"`
}

// ReplayReport is the outcome of replaying one story's transitions.
type ReplayReport struct {
	// Transitions is how many status_transition events the chain holds.
	Transitions int `json:"transitions"`
	// Checked is how many of them were replayed against a workflow version.
	Checked int `json:"checked"`
	// Unrecorded counts transitions that record no workflow hash (events made
	// before the hash was recorded, or while no workflow governed): their version
	// is unknown and is not invented, so they are not checked.
	Unrecorded int `json:"unrecorded"`
	// Unreplayable counts transitions recorded under a legacy workflow document,
	// whose source this binary can no longer derive a route from.
	Unreplayable int           `json:"unreplayable"`
	Breaks       []ReplayBreak `json:"breaks"`
}

// Replay computes a story's progress against its workflow by replaying its
// transitions, each against the workflow version its own event recorded: the
// workflow_version event of the chain whose hash the transition carries. The route
// is derived from that version's archived source by the same derivation the engine
// uses (wfgovern.RouteSpecFor), for the category and tags the story had at that
// event, so two developers whose workflows differ can work one story and each
// transition is judged by the workflow it was made under. A transition whose
// version cannot be derived (no hash, or a legacy one) is classified by governing,
// the story's own route, so a stale transition is skipped or applied exactly as
// the fold of the story does. Pure.
func Replay(entries []ledger.Entry, governing Route) ReplayReport {
	rep := ReplayReport{Breaks: []ReplayBreak{}}
	versions := map[string]ledger.WorkflowSource{}
	for _, e := range entries {
		if e.Kind != ledger.KindWorkflowVersion {
			continue
		}
		var src ledger.WorkflowSource
		if json.Unmarshal(e.Payload, &src) == nil && src.Hash != "" {
			versions[src.Hash] = src
		}
	}
	st := newState()
	for _, e := range chainOrder(entries) {
		route := governing
		if e.Kind == ledger.KindStatusTransition && TransitionTo(e) != "" {
			rep.Transitions++
			if spec := replayOne(&rep, e, st, versions); spec != nil {
				route = Route{Park: spec.IsParkState, Final: spec.IsFinal}
			}
		}
		st.apply(e, route)
	}
	rep.Breaks = append(rep.Breaks, st.StaleTransitions...)
	return rep
}

// replayOne checks one transition against its recorded version and returns the
// route it derived (nil when it could not).
func replayOne(rep *ReplayReport, e ledger.Entry, st State, versions map[string]ledger.WorkflowSource) *wfdot.Spec {
	from, to := TransitionFrom(e), TransitionTo(e)
	if from == "" {
		from = st.Status
	}
	brk := func(reason, detail string) {
		rep.Breaks = append(rep.Breaks, ReplayBreak{
			StoryID: e.StoryID, EventID: e.ID, From: from, To: to,
			WorkflowHash: e.WorkflowHash, Reason: reason, Detail: detail,
		})
	}
	if e.WorkflowHash == "" {
		rep.Unrecorded++
		return nil
	}
	src, ok := versions[e.WorkflowHash]
	if !ok {
		brk(ReplayNoVersion, fmt.Sprintf("the chain holds no %s event for hash %s", ledger.KindWorkflowVersion, e.WorkflowHash))
		return nil
	}
	if src.Legacy != "" && src.Done == "" && src.Step == "" {
		rep.Unreplayable++
		return nil
	}
	if wfgovern.RouteHash(src.Done, src.Step) != src.Hash {
		brk(ReplayBadVersion, "the archived route source does not hash to the version it is archived under")
		return nil
	}
	item := workitem.Item{Kind: workitem.KindStory, Category: st.Category, Tags: st.Tags}
	route, err := wfgovern.RouteSpecFor(wfgovern.RouteSource{Done: src.Done, Step: src.Step}, wfgovern.WorkflowCategory(item), st.Tags)
	if err != nil {
		brk(ReplayBadVersion, strings.TrimPrefix(err.Error(), "wfgovern: "))
		return nil
	}
	rep.Checked++
	spec := route.Spec
	switch {
	case from == to, spec.HasEdge(from, to):
	case spec.IsParkState(from) && st.ParkOrigin != "" && to == st.ParkOrigin: // resume to where it parked from
	default:
		brk(ReplayNoEdge, fmt.Sprintf("workflow version %s declares no edge %s → %s", shortHash(e.WorkflowHash), from, to))
	}
	return &spec
}

func shortHash(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}
