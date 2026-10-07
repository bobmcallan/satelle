package verb

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// CodeState says whether a story can be carrying code that exists only on this
// machine, and where to look (sty_7361569a). It answers from the route and the
// ledger only; asking git is the caller's job.
type CodeState struct {
	// Eligible is true when the story is non-terminal and has entered a code-bearing
	// state of its workflow (Spec.CodeBearingStates): at or after the freeze step,
	// whichever agent performs it, or an executor state on a route with no freeze
	// step. A story that never entered one has no code of its own.
	Eligible bool
	// Tree is the working tree the story was engaged from.
	Tree string
	// Unavailable is why Tree could not be named on this machine ("" when it can).
	Unavailable string
}

// WorkState is what a story's route and ledger say about code it has produced
// (sty_78e20d15). Like CodeState it reads the ledger only; asking git is the
// caller's job.
type WorkState struct {
	// Unavailable is why the story could not be classified ("" when it can).
	Unavailable string
	// Skip is true when the story's status is a cancel sink of its route (a park
	// that is not a resume park) or, when no route resolves, the cancelled
	// status. Such a story claims no code.
	Skip bool
	// Terminal is true when the status is a terminal state of the route.
	Terminal bool
	// Entered is true when the story is in, or has ever been in, an executor
	// state of its route.
	Entered bool
	// InFlight is true when the status is itself an executor state.
	InFlight bool
	// EnteredAt is when the story entered its current executor state (zero when
	// not in flight or the ledger has no such transition).
	EnteredAt time.Time
	// LeftWork is true when the ledger records the story leaving an executor state.
	LeftWork bool
	// PostWorkHead is the head recorded on the latest change_record written when
	// the story left an executor state. A change record's head is read at
	// transition time, so the row for ENTERING a state holds the commit the work
	// started from; only a row for leaving one holds code the work produced.
	PostWorkHead string
	// BaselineTree is the working tree the story was first engaged from ("" when
	// no baseline records one).
	BaselineTree string
}

// StoryWorkState walks item's governing route and ledger. A story whose route
// does not resolve is Unavailable ("workflow not resolved").
func StoryWorkState(ctx context.Context, item workitem.Item) (WorkState, error) {
	if item.Kind != workitem.KindStory {
		return WorkState{Skip: true}, nil
	}
	route, _, ok := governingRoute(ctx, item)
	if !ok {
		return WorkState{
			Unavailable: "workflow not resolved",
			Skip:        item.Status == workitem.StatusCancelled,
		}, nil
	}
	spec := route.Spec
	ws := WorkState{
		Skip:     spec.IsParkState(item.Status) && !spec.IsResumePark(item.Status),
		Terminal: spec.IsTerminalState(item.Status),
	}
	executor := map[string]bool{}
	for _, s := range spec.CodeBearingStates() {
		executor[s] = true
	}
	ws.InFlight = executor[item.Status]
	ws.Entered = ws.InFlight

	led, err := requireLedger()
	if err != nil {
		return WorkState{}, err
	}
	transitions, err := led.ListByStory(ctx, item.ID, ledger.KindStatusTransition)
	if err != nil {
		return WorkState{}, err
	}
	for _, e := range transitions {
		to, from := TransitionTo(e), TransitionFrom(e)
		if executor[to] {
			ws.Entered = true
			if ws.InFlight && to == item.Status {
				ws.EnteredAt = e.CreatedAt
			}
		}
		if executor[from] {
			ws.LeftWork = true
		}
	}
	changes, err := led.ListByStory(ctx, item.ID, ledger.KindChangeRecord)
	if err != nil {
		return WorkState{}, err
	}
	for _, e := range changes {
		var p changeRecordPayload
		if len(e.Payload) == 0 || json.Unmarshal(e.Payload, &p) != nil || !executor[p.From] {
			continue
		}
		ws.LeftWork = true
		ws.PostWorkHead = p.HeadSHA
	}
	if base, _, _, berr := firstEngagementBaseline(ctx, item.ID); berr == nil {
		ws.BaselineTree = base.Worktree
	}
	return ws, nil
}

// StoryCodeState classifies item for the work-state push hold. fallbackTree
// stands in for a baseline recorded before it anchored its working tree.
func StoryCodeState(ctx context.Context, item workitem.Item, fallbackTree string) (CodeState, error) {
	if item.Kind != workitem.KindStory {
		return CodeState{}, nil
	}
	ws, err := StoryWorkState(ctx, item)
	if err != nil {
		return CodeState{}, err
	}
	if ws.Unavailable != "" {
		return CodeState{Unavailable: ws.Unavailable}, nil
	}
	if ws.Terminal || !ws.Entered {
		return CodeState{}, nil
	}
	base, _, _, err := firstEngagementBaseline(ctx, item.ID)
	if err != nil {
		return CodeState{Eligible: true, Unavailable: "no engagement baseline"}, nil
	}
	if base.HeadSHA == "" {
		return CodeState{Eligible: true, Unavailable: "git was unavailable when the story was engaged"}, nil
	}
	tree := base.Worktree
	if tree == "" {
		tree = fallbackTree
	}
	if _, serr := os.Stat(tree); tree == "" || serr != nil {
		return CodeState{Eligible: true, Unavailable: fmt.Sprintf("engagement tree %q is not on this machine", tree)}, nil
	}
	return CodeState{Eligible: true, Tree: tree}, nil
}

// RouteRest says whether a story is at rest on its governing route: in a
// terminal state, or parked (blocked, cancelled) (sty_52eb8c2f). Status names
// are never compared here; the route decides. Known is false when the route
// could not be resolved, and a caller must then treat the story as in flight.
type RouteRest struct {
	Known    bool
	Terminal bool
	Parked   bool
}

// StoryRouteRest classifies item's status on its governing route.
func StoryRouteRest(ctx context.Context, item workitem.Item) RouteRest {
	route, _, ok := governingRoute(ctx, item)
	if !ok {
		return RouteRest{}
	}
	return RouteRest{
		Known:    true,
		Terminal: route.Spec.IsTerminalState(item.Status),
		Parked:   route.Spec.IsParkState(item.Status),
	}
}
