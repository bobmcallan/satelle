package wfdot

import "testing"

func TestEditCapableStatesAreDOTDerived(t *testing.T) {
	const body = "```dot\n" + `digraph w {
  backlog     [shape=Mdiamond]
  plan        [agent=planner, prompt="@skill:plan"]
  in_progress [agent=executor, prompt="@skill:code"]
  integration [agent=executor, prompt="@skill:integrate"]
  release     [agent=executor, prompt="@skill:release"]
  parked      [agent=reviewer]
  augment     [agent=executor, on="in_progress", applies_to="surface:ui"]
  step        [agent=reviewer, prompt="@skill:satelle-step-summary"]
  done        [shape=Msquare]
  backlog -> plan -> in_progress -> integration -> release -> done
  in_progress -> parked
  parked -> in_progress
}` + "\n```\n"
	spec, ok := Parse(body)
	if !ok {
		t.Fatal("Parse returned false")
	}
	got := spec.EditCapableStates()
	want := []string{"in_progress", "integration", "release"}
	if len(got) != len(want) {
		t.Fatalf("EditCapableStates = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("EditCapableStates = %v, want %v", got, want)
		}
	}
	for name, allowed := range map[string]bool{
		"in_progress": true, "integration": true, "release": true,
		"backlog": false, "plan": false, "parked": false, "augment": false,
		"step": false, "done": false, "unknown": false,
	} {
		if got := spec.IsEditCapableState(name); got != allowed {
			t.Errorf("IsEditCapableState(%q) = %v, want %v", name, got, allowed)
		}
	}
	if agent, found := spec.StateAgent("plan"); !found || agent != "planner" {
		t.Errorf("StateAgent(plan) = %q, %v", agent, found)
	}
	if _, found := spec.StateAgent("unknown"); found {
		t.Error("unknown state reported found")
	}
}
