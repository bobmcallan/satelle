package wfdot

import "testing"

func TestHasEdgeAndSuccessors(t *testing.T) {
	body := "```dot\ndigraph w {\n  backlog [shape=Mdiamond]\n  plan [agent=executor]\n  in_progress [agent=executor]\n  done [shape=Msquare]\n  blocked [agent=reviewer]\n  backlog -> plan\n  plan -> in_progress\n  in_progress -> done\n  in_progress -> blocked\n  blocked -> in_progress\n}\n```\n"
	spec, ok := Parse(body)
	if !ok {
		t.Fatal("parse")
	}
	if !spec.HasEdge("backlog", "plan") {
		t.Error("backlog→plan should exist")
	}
	if spec.HasEdge("backlog", "in_progress") {
		t.Error("backlog→in_progress must NOT exist (plan blow-through)")
	}
	if !spec.HasEdge("blocked", "in_progress") {
		t.Error("park resume must be a legal edge")
	}
	got := spec.Successors("backlog")
	if len(got) != 1 || got[0] != "plan" {
		t.Errorf("Successors(backlog) = %v, want [plan]", got)
	}
	got = spec.Successors("in_progress")
	if len(got) != 2 || got[0] != "blocked" || got[1] != "done" {
		t.Errorf("Successors(in_progress) = %v, want [blocked done]", got)
	}
}
