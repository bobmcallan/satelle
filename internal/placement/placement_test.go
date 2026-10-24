package placement_test

import (
	"context"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/placement"
	"github.com/bobmcallan/satelle/internal/placement/placementtest"
	"github.com/bobmcallan/satelle/internal/wfdot"
)

var declared = wfdot.State{
	Agent:       "coder",
	RemoteAgent: "coder-cloud",
	LocalTags:   []string{"lane:trunk"},
}

func TestDecide(t *testing.T) {
	cases := []struct {
		name       string
		schedule   string
		signedIn   bool
		step       wfdot.State
		tags       []string
		orphan     bool
		wantAgent  string
		wantRemote bool
		wantNote   string
	}{
		{name: "parallel child signed in is remote", schedule: "parallel", signedIn: true, step: declared, wantAgent: "coder-cloud", wantRemote: true},
		{name: "nothing set is parallel and remote", schedule: "", signedIn: true, step: declared, wantAgent: "coder-cloud", wantRemote: true},
		{name: "sequential = true stays local", schedule: placementtest.SequentialKey, signedIn: true, step: declared, wantAgent: "coder"},
		{name: "local tag pins the child local", schedule: "parallel", signedIn: true, step: declared, tags: []string{"lane:trunk"}, wantAgent: "coder"},
		{name: "sequential epic stays local", schedule: "sequential", signedIn: true, step: declared, wantAgent: "coder"},
		{name: "not an epic child stays local", schedule: "parallel", signedIn: true, step: declared, orphan: true, wantAgent: "coder"},
		{name: "no remote_agent stays local", schedule: "parallel", signedIn: true, step: wfdot.State{Agent: "coder"}, wantAgent: "coder"},
		{name: "not signed in falls back with a note", schedule: "parallel", step: declared, wantAgent: "coder", wantNote: placement.NotSignedIn},
		{name: "not signed in but pinned local has no note", schedule: "parallel", step: declared, tags: []string{"lane:trunk"}, wantAgent: "coder"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := placementtest.Wire(t, tc.schedule)
			epic := placementtest.Epic(t, db)
			if tc.signedIn {
				placementtest.SignIn(t)
			} else {
				// A git email is what the CLI's actor resolver yields local-only; it
				// must never read as signed in.
				placementtest.LocalOnly(t, "dev@example.com")
			}
			item := placementtest.Child(t, db, epic, tc.tags...)
			if tc.orphan {
				item = placementtest.Orphan(t, db, tc.tags...)
			}
			got := placement.Decide(context.Background(), item, tc.step, nil)
			if got.Agent != tc.wantAgent || got.Remote != tc.wantRemote || got.Note != tc.wantNote {
				t.Fatalf("Decide = %+v, want agent %q remote %v note %q", got, tc.wantAgent, tc.wantRemote, tc.wantNote)
			}
		})
	}
}

// A binding's own placement (sty_7028d996) places the step when the step's
// remote_agent rule does not; a binding declaring none is placed as before.
func TestDecideReadsTheBindingPlacement(t *testing.T) {
	bindings := func(name string) (config.AgentBinding, bool) {
		switch name {
		case "far":
			return config.AgentBinding{Placement: "remote", Remote: "r1"}, true
		case "team":
			return config.AgentBinding{Placement: "team:acme/ops"}, true
		case "plain":
			return config.AgentBinding{}, true
		}
		return config.AgentBinding{}, false
	}
	cases := []struct {
		name  string
		step  wfdot.State
		binds placement.Bindings
		want  placement.Decision
	}{
		{name: "remote binding is a channel placement", step: wfdot.State{Agent: "far"}, binds: bindings,
			want: placement.Decision{Agent: "far", Remote: true, Channel: true}},
		{name: "team binding names its team", step: wfdot.State{Agent: "team"}, binds: bindings,
			want: placement.Decision{Agent: "team", Team: "acme/ops"}},
		{name: "no placement is local", step: wfdot.State{Agent: "plain"}, binds: bindings,
			want: placement.Decision{Agent: "plain"}},
		{name: "unresolved binding is local", step: wfdot.State{Agent: "ghost"}, binds: bindings,
			want: placement.Decision{Agent: "ghost"}},
		{name: "executor is never placed", step: wfdot.State{Agent: "executor"}, binds: bindings,
			want: placement.Decision{Agent: "executor"}},
		{name: "no resolver is local", step: wfdot.State{Agent: "far"}, binds: nil,
			want: placement.Decision{Agent: "far"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := placementtest.Wire(t, "parallel")
			item := placementtest.Child(t, db, placementtest.Epic(t, db))
			if got := placement.Decide(context.Background(), item, tc.step, tc.binds); got != tc.want {
				t.Fatalf("Decide = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// AC5: a binding that declares no placement yields the same Decision whether or
// not a resolver is supplied, for a remote_agent step and a plain one.
func TestDecideWithoutPlacementIsUnchanged(t *testing.T) {
	plain := func(string) (config.AgentBinding, bool) { return config.AgentBinding{}, true }
	for _, step := range []wfdot.State{declared, {Agent: "coder"}} {
		db := placementtest.Wire(t, "parallel")
		epic := placementtest.Epic(t, db)
		placementtest.SignIn(t)
		item := placementtest.Child(t, db, epic)
		with := placement.Decide(context.Background(), item, step, plain)
		without := placement.Decide(context.Background(), item, step, nil)
		if with != without {
			t.Errorf("step %+v: with resolver %+v, without %+v", step, with, without)
		}
	}
}
