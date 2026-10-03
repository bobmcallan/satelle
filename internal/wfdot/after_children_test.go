package wfdot

import (
	"strings"
	"testing"
)

// A container step's `after_children` names an obligation every child must have
// discharged on its own route before the container enters the step. The binary
// stores it as authored; absent stays absent.

func afterChildrenSteps(mergeExtra string) string {
	return `[raised]
status = "backlog"
start = true

[coded]
status = "in_progress"
agent = "executor"
requires = ["raised"]

[ready]
status = "ready"
agent = "ready-reviewer"
waits_on_children = true
requires = ["raised"]

[merged]
status = "merging"
` + mergeExtra + `
requires = ["ready"]

[children-resolved]
status = "done"
agent = "reviewer"
terminal = true
requires = ["merged"]
`
}

const afterChildrenDone = `["*"]
obligations = ["raised", "coded"]

[epic-parent]
obligations = ["raised", "ready", "merged", "children-resolved"]
`

func TestAfterChildrenParsesOntoStepAndSpec(t *testing.T) {
	body := afterChildrenSteps("agent = \"executor\"\nafter_children = \"coded\"")
	cat, err := ParseSteps(body)
	if err != nil {
		t.Fatalf("ParseSteps: %v", err)
	}
	if got := stepProviding(t, cat, "merged").AfterChildren; got != "coded" {
		t.Errorf("AfterChildren = %q, want coded", got)
	}
	if got := stepProviding(t, cat, "ready").AfterChildren; got != "" {
		t.Errorf("an undeclared step carries AfterChildren %q", got)
	}

	spec, err := ParseRoute(afterChildrenDone, body, "epic-parent", nil)
	if err != nil {
		t.Fatalf("ParseRoute: %v", err)
	}
	if got := spec.AfterChildren("merging"); got != "coded" {
		t.Errorf("Spec.AfterChildren(merging) = %q, want coded", got)
	}
	for _, other := range []string{"ready", "done", "no-such-status"} {
		if got := spec.AfterChildren(other); got != "" {
			t.Errorf("Spec.AfterChildren(%q) = %q, want empty", other, got)
		}
	}
	if st, ok := spec.StateNamed("merging"); !ok || st.Agent != "executor" || st.WaitsOnChildren {
		t.Errorf("the after_children step names its performer and is not the wait: %+v", st)
	}
}

func TestAfterChildrenAbsentStaysAbsent(t *testing.T) {
	cat, err := ParseSteps(afterChildrenSteps(""))
	if err != nil {
		t.Fatalf("ParseSteps: %v", err)
	}
	if got := stepProviding(t, cat, "merged").AfterChildren; got != "" {
		t.Errorf("absent after_children = %q, want empty", got)
	}
}

func TestAfterChildrenIsRefusedWhenItWouldBeASilentNoOp(t *testing.T) {
	cases := []struct {
		name, extra, want string
	}{
		{"unknown obligation", `after_children = "nothing-provides-this"`, "no step provides"},
		{"its own obligation", `after_children = "merged"`, "own obligation"},
		{"on the wait itself", `after_children = "coded"` + "\nwaits_on_children = true", "waits_on_children"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseSteps(afterChildrenSteps(tc.extra))
			if err == nil {
				t.Fatal("must be refused")
			}
			for _, want := range []string{`"merged"`, "after_children", tc.want} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("refusal %q must contain %q", err, want)
				}
			}
		})
	}
}

func TestAfterChildrenRefusedOffAContainerRoute(t *testing.T) {
	body := strings.Replace(afterChildrenSteps(`after_children = "coded"`), `requires = ["ready"]`, `requires = ["raised"]`, 1)
	_, err := ParseSteps(body)
	if err == nil || !strings.Contains(err.Error(), "waits_on_children") || !strings.Contains(err.Error(), `"merged"`) {
		t.Fatalf("a step no waits_on_children step precedes must be refused naming it: %v", err)
	}
}

func TestAfterChildrenAcceptsAWaitReachedThroughAnotherStep(t *testing.T) {
	body := `[raised]
status = "backlog"
start = true

[coded]
status = "in_progress"
agent = "executor"
requires = ["raised"]

[ready]
status = "ready"
agent = "ready-reviewer"
waits_on_children = true
requires = ["raised"]

[prepared]
status = "preparing"
requires = ["ready"]

[merged]
status = "merging"
after_children = "coded"
requires = ["prepared"]
`
	if _, err := ParseSteps(body); err != nil {
		t.Fatalf("the wait may be reached through other steps: %v", err)
	}
}

func TestSpineIndexAndObligationStepFollowRouteOrder(t *testing.T) {
	spec, err := ParseRoute(afterChildrenDone, afterChildrenSteps(`after_children = "coded"`), "epic-parent", nil)
	if err != nil {
		t.Fatalf("ParseRoute: %v", err)
	}
	if st, ok := spec.ObligationStep("ready"); !ok || st.Name != "ready" {
		t.Errorf("ObligationStep(ready) = %+v, %v", st, ok)
	}
	if _, ok := spec.ObligationStep("coded"); ok {
		t.Error("the epic-parent route has no step providing coded")
	}
	if a, b := spec.SpineIndex("ready"), spec.SpineIndex("merging"); a < 0 || b <= a {
		t.Errorf("SpineIndex ready=%d merging=%d, want ready before merging", a, b)
	}
	if got := spec.SpineIndex("blocked-or-unknown"); got != -1 {
		t.Errorf("SpineIndex of a status off the route = %d, want -1", got)
	}
}
