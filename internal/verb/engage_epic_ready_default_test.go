package verb_test

import (
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// sty_25064a6c: a repo with no .satelle/workflows runs the SHIPPED route, whose
// epic-parent now carries a readiness step. The gate that refuses a child's first
// engagement until its epic is readied therefore applies there too, and
// `satelle story ready <epic>` runs a round instead of failing with "declares no
// readiness step". No workflow document is written, so the embedded halves alone
// govern: a regression that drops the step from the shipped route fails here.

// wireShippedRoute wires a store with NO workflow documents, a performer that
// does the round and a gater that accepts it.
func wireShippedRoute(t *testing.T) *readyRig {
	t.Helper()
	withWiring(t)
	wireWithWorkflowsStore(t, nil)
	verb.SetStoryDir(t.TempDir())
	r := &readyRig{
		d: &roundDispatcher{t: t, calls: map[string]int{}, docName: "ready"},
		g: &roundGater{calls: map[string]int{}},
	}
	r.g.verd = func(workitem.Item, string) verb.GateDecision { return acceptAll() }
	verb.SetExecutorDispatcher(r.d)
	verb.SetTransitionGater(r.g)
	activeRig = r
	return r
}

// shippedEpicWithChild files an epic-parent and a child of its set, both in the
// backlog the shipped route starts them in.
func shippedEpicWithChild(t *testing.T) (epic, child workitem.Item) {
	t.Helper()
	epic = waveEpic(t)
	child = mkEpicStory(t, map[string]any{"category": "feature", "parent_id": epic.ID, "tags": []string{"epic:w"}})
	return epic, child
}

// AC2: on the shipped route an unreadied epic's child is refused its first
// engaging status, the refusal names the readying command, and nothing moves.
func TestShippedRouteRefusesChildOfUnreadiedEpic(t *testing.T) {
	wireShippedRoute(t)
	epic, child := shippedEpicWithChild(t)

	// in_progress is the child's first engaging status on the shipped working
	// lane (backlog -> in_progress -> done); the shipped route declares no plan.
	wantEpicNotReady(t, setStatus(t, child.ID, "in_progress"), epic.ID)
	if got := statusOf(t, child.ID).Status; got != "backlog" {
		t.Errorf("a refused child stays in backlog, got %q", got)
	}
}

// AC2: `satelle story ready <epic>` runs the shipped round, and once it holds the
// child engages.
func TestShippedRouteReadyRunsTheRound(t *testing.T) {
	r := wireShippedRoute(t)
	epic, child := shippedEpicWithChild(t)
	wantEpicNotReady(t, setStatus(t, child.ID, "in_progress"), epic.ID)

	// The child is a shipped-lane story, which has no readiness step of its own, so
	// the epic is not marked; the epic-level round is what the gate reads.
	res := readyCall(t, epic.ID)[0]
	if res.Outcome == verb.ReadyRefused || strings.Contains(res.Reason, epic.ID) {
		t.Fatalf("the shipped epic route must declare a readiness step, got %+v", res)
	}
	if r.d.calls[epic.ID] != 1 {
		t.Errorf("the round must dispatch the performer once, got %d", r.d.calls[epic.ID])
	}
	var accepted bool
	for _, e := range allRows(t, epic.ID) {
		if e.Kind == ledger.KindReadinessAccept {
			accepted = true
		}
	}
	if !accepted {
		t.Error("the round must record a readiness accept on the epic")
	}

	if err := setStatus(t, child.ID, "in_progress"); err != nil {
		t.Fatalf("a child of a readied epic must engage on the shipped route: %v", err)
	}
}
