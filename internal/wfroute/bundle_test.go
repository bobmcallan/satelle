package wfroute

import (
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/wfdot"
)

// sty_23e10d92: a step's `bundle` declaration reaches the route the orchestrator
// reads, so it is told a bundled edge from the route and not from a skill's prose.

func bundleRoute(t *testing.T, bundleLine string) (Route, Step) {
	t.Helper()
	steps := `[raised]
status = "backlog"
start = true

[readied]
status = "plan"
agent = "planner"
skills = ["plan"]
reviewers = ["gate-a", "gate-b"]
` + bundleLine + `
requires = ["raised"]

[closed]
status = "done"
terminal = true
requires = ["readied"]
`
	spec, err := wfdot.ParseRoute("[feature]\nobligations = [\"raised\", \"readied\", \"closed\"]\n", steps, "feature", nil)
	if err != nil {
		t.Fatal(err)
	}
	route := Build(spec, "default", nil, nil, nil)
	for _, s := range route.Steps {
		if s.Status == "plan" {
			return route, s
		}
	}
	t.Fatal("no plan step")
	return route, Step{}
}

func TestRouteCarriesBundleFromTheStep(t *testing.T) {
	route, plan := bundleRoute(t, "bundle = true")
	if !plan.Bundle {
		t.Fatalf("plan step = %+v, want bundle", plan)
	}
	if out := route.Render("backlog"); !strings.Contains(out, "bundled") {
		t.Errorf("a bundled edge must say so on the route:\n%s", out)
	}
	route, plan = bundleRoute(t, "")
	if plan.Bundle || strings.Contains(route.Render("backlog"), "bundled") {
		t.Errorf("an unbundled step must not claim it:\n%s", route.Render("backlog"))
	}
}
