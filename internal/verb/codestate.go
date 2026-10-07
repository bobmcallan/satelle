package verb

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// CodeState says whether a story can be carrying code that exists only on this
// machine, and where to look (sty_7361569a). It answers from the route and the
// ledger only; asking git is the caller's job.
type CodeState struct {
	// Eligible is true when the story is non-terminal and has entered a code-bearing
	// state of its workflow: one allocated to the in-loop executor
	// (Spec.EditCapableStates). A story that never entered one has no code of
	// its own.
	Eligible bool
	// Tree is the working tree the story was engaged from.
	Tree string
	// Unavailable is why Tree could not be named on this machine ("" when it can).
	Unavailable string
}

// StoryCodeState classifies item for the work-state push hold. fallbackTree
// stands in for a baseline recorded before it anchored its working tree.
func StoryCodeState(ctx context.Context, item workitem.Item, fallbackTree string) (CodeState, error) {
	if item.Kind != workitem.KindStory {
		return CodeState{}, nil
	}
	route, _, ok := governingRoute(ctx, item)
	if !ok {
		return CodeState{Unavailable: "workflow not resolved"}, nil
	}
	spec := route.Spec
	if spec.IsTerminalState(item.Status) {
		return CodeState{}, nil
	}
	executor := map[string]bool{}
	for _, s := range spec.EditCapableStates() {
		executor[s] = true
	}
	entered := executor[item.Status]
	if !entered {
		led, err := requireLedger()
		if err != nil {
			return CodeState{}, err
		}
		entries, err := led.ListByStory(ctx, item.ID, ledger.KindStatusTransition)
		if err != nil {
			return CodeState{}, err
		}
		for _, e := range entries {
			var p struct {
				To string `json:"to"`
			}
			if json.Unmarshal(e.Payload, &p) == nil && executor[p.To] {
				entered = true
				break
			}
		}
	}
	if !entered {
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
