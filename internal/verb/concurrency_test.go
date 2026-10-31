package verb_test

import (
	"strings"
	"testing"
)

// sty_9c782f0e AC4: an epic whose container step sets sequential = true admits
// one performing child at a time — a second child's engage is refused naming the
// epic and the performing child, from the children's status, so finishing the
// first admits the second. Nothing held is consulted.
func TestSequentialEpicAdmitsOnePerformingChild(t *testing.T) {
	wireWithWorkflows(t, waveSequentialWF())
	epic := waveEpic(t)
	a := waveChild(t)
	if err := setStatus(t, a.ID, "in_progress"); err != nil {
		t.Fatalf("first child must engage: %v", err)
	}
	b := waveChild(t)
	err := setStatus(t, b.ID, "in_progress")
	if err == nil {
		t.Fatal("a second child of a sequential epic must be refused while the first performs")
	}
	for _, want := range []string{b.ID, epic.ID, "sequential", a.ID} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal missing %q: %v", want, err)
		}
	}
	if isPerforming(t, b.ID) {
		t.Fatal("the refused child must not perform")
	}

	if err := setStatus(t, a.ID, "done"); err != nil {
		t.Fatalf("finish the first child: %v", err)
	}
	if err := setStatus(t, b.ID, "in_progress"); err != nil {
		t.Fatalf("with the first child done the second must engage: %v", err)
	}
}

// Without sequential = true the same epic's children perform together: the
// default concurrency is unlimited.
func TestParallelEpicChildrenPerformTogether(t *testing.T) {
	wireWithWorkflows(t, waveWF(""))
	waveEpic(t)
	a := waveChild(t)
	b := waveChild(t)
	for _, c := range []string{a.ID, b.ID} {
		if err := setStatus(t, c, "in_progress"); err != nil {
			t.Fatalf("%s must engage alongside its sibling: %v", c, err)
		}
	}
	if !isPerforming(t, a.ID) || !isPerforming(t, b.ID) {
		t.Fatal("both children must perform at once")
	}
}
