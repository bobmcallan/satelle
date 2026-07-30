package guard

import (
	"strings"
	"testing"

	"example.com/guard/internal/workflow"
)

const dot = `digraph lifecycle {
  backlog [shape=Mdiamond]
  plan [agent=planner]
  in_progress [agent=executor]
  done [shape=Msquare]
  backlog -> plan -> in_progress -> done
}`

func graph(t *testing.T) workflow.Graph {
	t.Helper()
	g, err := workflow.Parse(dot)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func TestDecideDeniesNonPerformingAndTerminal(t *testing.T) {
	g := graph(t)
	for _, state := range []string{"backlog", "done"} {
		if d := Decide(g, Request{State: state}); d.Allowed {
			t.Fatalf("%s allowed edits: %+v", state, d)
		}
	}
	if d := Decide(g, Request{State: "in_progress"}); !d.Allowed {
		t.Fatalf("performing state denied: %+v", d)
	}
}

func TestDecideDeniesClosedOnResolutionFailure(t *testing.T) {
	d := Decide(graph(t), Request{State: "nonexistent"})
	if d.Allowed || !strings.Contains(d.Reason, "restamp") {
		t.Fatalf("resolution failure must deny closed with recovery text: %+v", d)
	}
}

func TestDecideMatchesTheDispatchedPerformer(t *testing.T) {
	g := graph(t)
	if d := Decide(g, Request{State: "plan", Agent: "planner"}); !d.Allowed {
		t.Fatalf("matching performer denied: %+v", d)
	}
	if d := Decide(g, Request{State: "plan", Agent: "executor"}); d.Allowed {
		t.Fatalf("mismatched performer allowed: %+v", d)
	}
}
