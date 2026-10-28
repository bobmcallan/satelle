package wfdot

import (
	"strings"
	"testing"
)

// A container readiness step's `ready_children` declares whether readying the
// container also readies each child. Absent resolves to true; the key is legal
// only on a waits_on_children step.

func TestReadyChildrenResolvesOntoStepAndState(t *testing.T) {
	for _, tc := range []struct {
		name, line string
		want       bool
	}{
		{"absent", "", true},
		{"true", "ready_children = true\n", true},
		{"false", "ready_children = false\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := strings.Replace(afterChildrenSteps(""), "waits_on_children = true\n", "waits_on_children = true\n"+tc.line, 1)
			cat, err := ParseSteps(body)
			if err != nil {
				t.Fatalf("ParseSteps: %v", err)
			}
			if got := stepProviding(t, cat, "ready").ReadiesChildren(); got != tc.want {
				t.Errorf("Step.ReadiesChildren = %v, want %v", got, tc.want)
			}
			lists, err := ParseDone(afterChildrenDone)
			if err != nil {
				t.Fatal(err)
			}
			for _, l := range lists {
				if l.Category != "epic-parent" {
					continue
				}
				spec, err := BuildRoute(l, cat, nil)
				if err != nil {
					t.Fatalf("BuildRoute: %v", err)
				}
				st, ok := spec.StateNamed("ready")
				if !ok {
					t.Fatal("no ready state")
				}
				if st.ReadyChildren != tc.want {
					t.Errorf("State.ReadyChildren = %v, want %v", st.ReadyChildren, tc.want)
				}
			}
		})
	}
}

func TestReadyChildrenRefusedOffAContainerStep(t *testing.T) {
	body := strings.Replace(afterChildrenSteps(""), "[coded]\nstatus = \"in_progress\"\n", "[coded]\nstatus = \"in_progress\"\nready_children = false\n", 1)
	_, err := ParseSteps(body)
	if err == nil || !strings.Contains(err.Error(), "ready_children") || !strings.Contains(err.Error(), "waits_on_children") {
		t.Fatalf("want a refusal naming ready_children and waits_on_children, got %v", err)
	}
}

func TestReadyChildrenRefusesANonBoolean(t *testing.T) {
	body := strings.Replace(afterChildrenSteps(""), "waits_on_children = true\n", "waits_on_children = true\nready_children = \"no\"\n", 1)
	_, err := ParseSteps(body)
	if err == nil || !strings.Contains(err.Error(), "ready_children") {
		t.Fatalf("want a refusal naming ready_children, got %v", err)
	}
}
