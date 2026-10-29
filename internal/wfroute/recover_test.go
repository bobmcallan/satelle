package wfroute

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/wfdot"
)

// TestRouteRendersTheRecoverEdges (sty_50357766): a declared recover is a way
// back along the route, so `satelle story route` names it — on the JSON and in
// the rendered markdown — instead of leaving it to `workflow show`.
func TestRouteRendersTheRecoverEdges(t *testing.T) {
	r := Build(derived(t, nil), "wf", nil, nil, nil)
	if len(r.Recovers) != 1 {
		t.Fatalf("Recovers = %+v; want the one declared recover", r.Recovers)
	}
	if got := r.Recovers[0]; got.Status != "in_progress" || strings.Join(got.From, ",") != "integration,release" {
		t.Errorf("recover = %+v; want back to in_progress from integration, release", got)
	}
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"recovers":[{"status":"in_progress","from":["integration","release"]}]`) {
		t.Errorf("route JSON must carry the recover edges: %s", raw)
	}
	if line := stepLine(r.Render("release"), "in_progress"); !strings.HasSuffix(line, "· recover: back here from integration, release") {
		t.Errorf("the in_progress step line must name the way back; got %q", line)
	}
}

// stepLine returns the rendered line of the step with this status.
func stepLine(out, status string) string {
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "**"+status+"**") {
			return line
		}
	}
	return ""
}

// TestRouteWithoutRecoverRendersNone: forward edges are the spine, not ways back.
func TestRouteWithoutRecoverRendersNone(t *testing.T) {
	spec := wfdot.Spec{
		States: []wfdot.State{{Name: "merging"}, {Name: "release"}, {Name: "done", Shape: "Msquare"}},
		Transitions: []wfdot.Transition{
			{From: "merging", To: "release"},
			{From: "release", To: "done"},
		},
	}
	r := Build(spec, "wf", nil, nil, nil)
	if len(r.Recovers) != 0 {
		t.Fatalf("Recovers = %+v; want none on a forward-only route", r.Recovers)
	}
	if out := r.Render("merging"); strings.Contains(out, "recover:") {
		t.Errorf("a route with no way back must render no recover line:\n%s", out)
	}

	spec.Transitions = append(spec.Transitions, wfdot.Transition{From: "release", To: "merging"})
	out := Build(spec, "wf", nil, nil, nil).Render("release")
	if line := stepLine(out, "merging"); !strings.HasSuffix(line, "· recover: back here from release") {
		t.Errorf("a release → merging edge must render on the merging line; got %q", line)
	}
	if line := stepLine(out, "release"); strings.Contains(line, "recover:") {
		t.Errorf("the edge's source must not claim the way back; got %q", line)
	}
}
