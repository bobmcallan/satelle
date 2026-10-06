package placement_test

import (
	"context"
	"testing"

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
			got := placement.Decide(context.Background(), item, tc.step)
			if got.Agent != tc.wantAgent || got.Remote != tc.wantRemote || got.Note != tc.wantNote {
				t.Fatalf("Decide = %+v, want agent %q remote %v note %q", got, tc.wantAgent, tc.wantRemote, tc.wantNote)
			}
		})
	}
}
