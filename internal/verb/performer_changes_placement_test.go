package verb_test

import (
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/agentartifact"
	"github.com/bobmcallan/satelle/internal/verb"
)

// sty_c931cd90: the readiness round records the orchestrator's per-child delivery
// choice as a tag under the step's placement_tag prefix. The fixture declares the
// prefix on the container's ready step and allows it in apply_changes.

// placementStep is the changes fixture with the placement prefix allowed and
// declared as the ready step's placement_tag.
func placementStep() string {
	s := strings.ReplaceAll(changesStep(), `"surface:"]`, `"surface:", "placement:"]`)
	return strings.Replace(s, changesApply2(), changesApply2()+`placement_tag = "placement:"`+"\n", 1)
}

// changesApply2 is the apply_changes line of placementStep's allow-list.
func changesApply2() string {
	return strings.ReplaceAll(changesApply, `"surface:"]`, `"surface:", "placement:"]`)
}

// A readied epic carries the recorded per-child choice: a reported placement pin
// is applied to the child as a tag, within the allow-list.
func TestReadyRoundRecordsTheDeliveryChoiceOnTheChild(t *testing.T) {
	w := newChangesWorldFor(t, placementStep())
	w.r.d.perform = performerReports(w.epic.ID, []agentartifact.Change{
		{Op: "add-tags", Story: w.a.ID, Tags: []string{"placement:team:/teams/x"}},
		{Op: "add-tags", Story: w.b.ID, Tags: []string{"placement:in-loop"}},
	}, "")

	res := readyCall(t, w.epic.ID)[0]
	if res.Outcome != verb.ReadyMarked {
		t.Fatalf("the epic must be readied: %+v", res)
	}
	if got := tagsOf(t, w.a.ID); !carries(got, "placement:team:/teams/x") {
		t.Errorf("child a tags = %v, want its team pin", got)
	}
	if got := tagsOf(t, w.b.ID); !carries(got, "placement:in-loop") {
		t.Errorf("child b tags = %v, want its in-loop pin", got)
	}
}

// A value that is not a pin the engine can honour — remote included — is refused
// with nothing applied, and a prefix the step does not allow is still refused.
func TestReadyRoundRefusesAnInvalidPlacementPin(t *testing.T) {
	for name, tc := range map[string]struct{ tag, want string }{
		"remote":                 {"placement:remote", "not one of"},
		"unknown value":          {"placement:bogus", "not one of"},
		"empty value":            {"placement:", "not one of"},
		"a prefix not allowed":   {"venue:team:/teams/x", "no declared prefix"},
		"team with no path":      {"placement:team:", "not one of"},
		"differently cased team": {"Placement:team:/teams/x", "no declared prefix"},
	} {
		t.Run(name, func(t *testing.T) {
			w := newChangesWorldFor(t, placementStep())
			w.r.d.perform = performerReports(w.epic.ID, []agentartifact.Change{
				{Op: "add-tags", Story: w.a.ID, Tags: []string{"order:1"}},
				{Op: "add-tags", Story: w.b.ID, Tags: []string{tc.tag}},
			}, "")
			res := readyCall(t, w.epic.ID)[0]
			if res.Outcome != verb.ReadyNotAccepted || !strings.Contains(res.Reason, tc.want) || !strings.Contains(res.Reason, tc.tag) {
				t.Fatalf("want a refusal naming %q and %q, got %+v", tc.tag, tc.want, res)
			}
			if carries(tagsOf(t, w.a.ID), "order:1") || carries(tagsOf(t, w.b.ID), tc.tag) {
				t.Errorf("nothing may be applied: a=%v b=%v", tagsOf(t, w.a.ID), tagsOf(t, w.b.ID))
			}
		})
	}
}
