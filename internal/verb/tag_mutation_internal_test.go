package verb

import (
	"reflect"
	"testing"
)

func TestApplyTagMutationAddPreservesExisting(t *testing.T) {
	got := applyTagMutation(
		[]string{"workflow:satelle-project-workflow", "sprint:3", "order:1"},
		[]string{"sprint:4"},
		nil,
	)
	want := []string{"workflow:satelle-project-workflow", "sprint:3", "order:1", "sprint:4"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestApplyTagMutationRemoveExactAndGroup(t *testing.T) {
	cur := []string{"workflow:wf", "sprint:3", "sprint:4", "order:1", "area:init"}
	got := applyTagMutation(cur, []string{"sprint:5"}, []string{"sprint:*", "order:1"})
	want := []string{"workflow:wf", "area:init", "sprint:5"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	got2 := applyTagMutation(cur, nil, []string{"sprint:"})
	if len(got2) != 3 || got2[0] != "workflow:wf" {
		t.Fatalf("sprint: group remove: %v", got2)
	}
}

func TestApplyTagMutationDedupeAndStable(t *testing.T) {
	got := applyTagMutation(
		[]string{"a", "b", "a"},
		[]string{"b", "c", "c"},
		nil,
	)
	want := []string{"a", "b", "c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}
