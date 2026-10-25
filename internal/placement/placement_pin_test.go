package placement_test

import (
	"context"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/placement"
	"github.com/bobmcallan/satelle/internal/wfdot"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// A child the orchestrator pinned (a tag under the step's placement_tag prefix) is
// placed by its pin, over the binding's own placement and ahead of local_tags and
// remote_agent. A pin never places a child remote.
func TestDecideHonoursChildPin(t *testing.T) {
	bindings := func(name string) (config.AgentBinding, bool) {
		switch name {
		case "far":
			return config.AgentBinding{Placement: "remote", Remote: "r1"}, true
		case "plain":
			return config.AgentBinding{}, true
		}
		return config.AgentBinding{}, false
	}
	pinned := wfdot.State{Agent: "plain", PlacementTag: "placement:"}
	cases := []struct {
		name    string
		step    wfdot.State
		tags    []string
		want    placement.Decision
		wantErr string
	}{
		{name: "team pin", step: pinned, tags: []string{"placement:team:/teams/x"},
			want: placement.Decision{Agent: "plain", Pinned: true, Team: "/teams/x"}},
		{name: "in-loop pin", step: pinned, tags: []string{"placement:in-loop"},
			want: placement.Decision{Agent: "plain", Pinned: true, InLoop: true}},
		{name: "local pin", step: pinned, tags: []string{"placement:local"},
			want: placement.Decision{Agent: "plain", Pinned: true}},
		{name: "local pin overrides a remote-placed binding", step: wfdot.State{Agent: "far", PlacementTag: "placement:"},
			tags: []string{"placement:local"},
			want: placement.Decision{Agent: "far", Pinned: true}},
		{name: "no pin keeps the binding's placement", step: wfdot.State{Agent: "far", PlacementTag: "placement:"},
			tags: []string{"other:tag"},
			want: placement.Decision{Agent: "far", Remote: true, Channel: true}},
		{name: "a step without placement_tag ignores the tag", step: wfdot.State{Agent: "plain"},
			tags: []string{"placement:team:/teams/x"},
			want: placement.Decision{Agent: "plain"}},
		{name: "the pin wins over a non-matching local_tags case",
			step: wfdot.State{Agent: "plain", PlacementTag: "placement:", RemoteAgent: "cloud", LocalTags: []string{"lane:trunk"}},
			tags: []string{"placement:team:/teams/x"},
			want: placement.Decision{Agent: "plain", Pinned: true, Team: "/teams/x"}},
		{name: "a local pin agrees with a matching local_tags case",
			step: wfdot.State{Agent: "plain", PlacementTag: "placement:", RemoteAgent: "cloud", LocalTags: []string{"lane:trunk"}},
			tags: []string{"lane:trunk", "placement:local"},
			want: placement.Decision{Agent: "plain", Pinned: true}},
		{name: "a pinned child never goes to the cloud agent",
			step: wfdot.State{Agent: "plain", PlacementTag: "placement:", RemoteAgent: "cloud"},
			tags: []string{"placement:in-loop"},
			want: placement.Decision{Agent: "plain", Pinned: true, InLoop: true}},
		{name: "remote is not a pin", step: pinned, tags: []string{"placement:remote"}, wantErr: "not one of"},
		{name: "an unknown value is not a pin", step: pinned, tags: []string{"placement:bogus"}, wantErr: "not one of"},
		{name: "an empty value is not a pin", step: pinned, tags: []string{"placement:"}, wantErr: "not one of"},
		{name: "two pins that disagree", step: pinned, tags: []string{"placement:local", "placement:in-loop"}, wantErr: "conflicting"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			item := workitem.Item{ID: "sty_child", Kind: workitem.KindStory, Tags: tc.tags}
			got := placement.Decide(context.Background(), item, tc.step, bindings)
			if tc.wantErr != "" {
				if got.PinErr == nil || !strings.Contains(got.PinErr.Error(), tc.wantErr) {
					t.Fatalf("PinErr = %v, want one containing %q", got.PinErr, tc.wantErr)
				}
				return
			}
			if got.PinErr != nil || got != tc.want {
				t.Fatalf("Decide = %+v, want %+v", got, tc.want)
			}
		})
	}
}
