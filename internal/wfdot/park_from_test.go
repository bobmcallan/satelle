package wfdot

import (
	"strings"
	"testing"
)

func TestParkFromStarMaterializesInboundEdges(t *testing.T) {
	body := "```dot\ndigraph w {\n" +
		"  backlog [shape=Mdiamond]\n" +
		"  plan [agent=executor, prompt=\"@skill:plan\"]\n" +
		"  in_progress [agent=executor, prompt=\"@skill:code\"]\n" +
		"  integration [agent=executor, prompt=\"@skill:integrate\"]\n" +
		"  release [agent=executor, prompt=\"@skill:release\"]\n" +
		"  done [shape=Msquare]\n" +
		"  cancelled [agent=reviewer, prompt=\"@skill:cancel\"]\n" +
		"  blocked [agent=reviewer, prompt=\"@skill:park\", from=\"*\"]\n" +
		"  backlog -> plan -> in_progress -> integration -> release -> done\n" +
		"  blocked -> cancelled\n" +
		"}\n```"
	spec, ok := Parse(body)
	if !ok {
		t.Fatal("parse failed")
	}
	if len(Validate(spec)) > 0 {
		t.Fatalf("validate: %v", Validate(spec))
	}
	for _, src := range []string{"plan", "in_progress", "integration", "release"} {
		if !spec.HasEdge(src, "blocked") {
			t.Errorf("missing park edge %s→blocked", src)
		}
	}
	// backlog is start, not performing — not a park source for *
	if spec.HasEdge("backlog", "blocked") {
		t.Error("backlog must not park via from=*")
	}
	// resume edge is NOT materialized
	if spec.HasEdge("blocked", "integration") {
		t.Error("resume edges must not be materialized from from=")
	}
	// park node skill gates inbound
	for _, tr := range spec.Transitions {
		if tr.To == "blocked" && tr.From == "integration" {
			if tr.Skill != "park" {
				t.Errorf("inbound park skill = %q, want park", tr.Skill)
			}
		}
	}
}

func TestWildcardEdgeEndpointRejected(t *testing.T) {
	body := "```dot\ndigraph w {\n" +
		"  backlog [shape=Mdiamond]\n" +
		"  done [shape=Msquare]\n" +
		"  blocked [agent=reviewer, prompt=\"@skill:park\"]\n" +
		"  * -> blocked\n" +
		"  backlog -> done\n" +
		"}\n```"
	spec, ok := Parse(body)
	if !ok {
		t.Fatal("parse failed")
	}
	probs := Validate(spec)
	joined := strings.Join(probs, "\n")
	if !strings.Contains(joined, `wildcard "*" is not a legal edge endpoint`) {
		t.Fatalf("expected wildcard edge reject, got: %v", probs)
	}
	// No phantom * node
	for _, st := range spec.States {
		if st.Name == "*" {
			t.Error("wildcard must not register as a state")
		}
	}
	if spec.Start() == "*" {
		t.Error("Start must not be *")
	}
}

func TestParkFromExplicitList(t *testing.T) {
	body := "```dot\ndigraph w {\n" +
		"  backlog [shape=Mdiamond]\n" +
		"  in_progress [agent=executor, prompt=\"@skill:code\"]\n" +
		"  integration [agent=executor, prompt=\"@skill:i\"]\n" +
		"  done [shape=Msquare]\n" +
		"  blocked [agent=reviewer, prompt=\"@skill:park\", from=\"integration\"]\n" +
		"  backlog -> in_progress -> integration -> done\n" +
		"}\n```"
	spec, ok := Parse(body)
	if !ok {
		t.Fatal("parse")
	}
	if !spec.HasEdge("integration", "blocked") {
		t.Error("want integration→blocked")
	}
	if spec.HasEdge("in_progress", "blocked") {
		t.Error("in_progress must not park when from=integration only")
	}
}
