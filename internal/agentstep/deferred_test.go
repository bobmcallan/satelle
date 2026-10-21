package agentstep

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// sty_83620b39: a deferral recorded against a gate skill reaches the payload
// that gate actually receives — alone or inside a bundled session — and no other.

func relayRow(t *testing.T, rec ReworkRelayRecord) ledger.Entry {
	t.Helper()
	b, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	return ledger.Entry{Kind: ledger.KindAgentInvocation, Payload: b}
}

func TestDeferralsFromReadsOnlyRelayRows(t *testing.T) {
	relay := relayRow(t, ReworkRelayRecord{
		Relay: ReworkRelayMarker, Outcome: "converged_with_deferrals", Converged: true,
		Deferred: []DeferredItem{{Owner: "integration-review", Objection: "db tests never ran"}, {Owner: DeferOwnerGrant, Objection: "needs docker"}},
	})
	// A dispatch-attempt row also carries `outcome`, and may even carry a
	// deferred-shaped key — but never `relay`.
	decoy := ledger.Entry{Kind: ledger.KindAgentInvocation, Payload: json.RawMessage(
		`{"outcome":"timeout","deferred":[{"owner":"integration-review","objection":"decoy"}]}`)}
	otherKind := relayRow(t, ReworkRelayRecord{Relay: ReworkRelayMarker, Deferred: []DeferredItem{{Owner: "x", Objection: "wrong kind"}}})
	otherKind.Kind = "agent_message"
	repeat := relayRow(t, ReworkRelayRecord{Relay: ReworkRelayMarker, Deferred: []DeferredItem{{Owner: DeferOwnerGrant, Objection: "needs docker"}}})

	got := DeferralsFrom([]ledger.Entry{relay, decoy, otherKind, repeat, {Kind: ledger.KindAgentInvocation, Payload: json.RawMessage(`not json`)}})
	want := []DeferredItem{{Owner: "integration-review", Objection: "db tests never ran"}, {Owner: DeferOwnerGrant, Objection: "needs docker"}}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("DeferralsFrom = %+v, want %+v", got, want)
	}
}

// capturingRunner wraps bundleRunner so the material files a request names are
// read while the scratch directory still exists.
type capturingRunner struct {
	*bundleRunner
	opened []map[string][]byte
}

func (c *capturingRunner) Run(ctx context.Context, req agentcli.Request) ([]byte, error) {
	c.opened = append(c.opened, openPayloadFiles(req.Payload))
	return c.bundleRunner.Run(ctx, req)
}

func deferralResolver(items ...DeferredItem) func(context.Context, string) []DeferredItem {
	return func(_ context.Context, id string) []DeferredItem {
		if id != "sty_def" {
			return nil
		}
		return items
	}
}

func deferredFile(t *testing.T, payload string, opened map[string][]byte) []DeferredItem {
	t.Helper()
	var items []DeferredItem
	if err := json.Unmarshal(openedAt(t, opened, payload, "deferred"), &items); err != nil {
		t.Fatal(err)
	}
	return items
}

func TestReviewerMaterialCarriesDeferralsForItsSkill(t *testing.T) {
	g, r := newEngine(t, `{"decision":"accept"}`, fakeDocs{workflow: testWorkflow, skillBody: "rubric", skillFound: true})
	g.SetDeferralsResolver(deferralResolver(
		DeferredItem{Owner: "satelle-story-done-review", Objection: "db tests never ran"},
		DeferredItem{Owner: DeferOwnerGrant, Objection: "needs docker"},
		DeferredItem{Owner: "some-other-gate", Objection: "not for this gate"},
	))
	if _, err := g.Gate(context.Background(), workitem.Item{ID: "sty_def", Status: "in_progress"}, "done"); err != nil {
		t.Fatal(err)
	}
	items := deferredFile(t, r.got.Payload, r.opened)
	if len(items) != 1 || items[0].Owner != "satelle-story-done-review" || items[0].Objection != "db tests never ran" {
		t.Errorf("deferred file = %+v, want exactly the done-review item", items)
	}
}

func TestReviewerPayloadHasNoDeferredForAGateThatOwnsNone(t *testing.T) {
	g, r := newEngine(t, `{"decision":"accept"}`, fakeDocs{workflow: testWorkflow, skillBody: "rubric", skillFound: true})
	g.SetDeferralsResolver(deferralResolver(
		DeferredItem{Owner: "integration-review", Objection: "db tests never ran"},
		DeferredItem{Owner: DeferOwnerGrant, Objection: "needs docker"},
	))
	if _, err := g.Gate(context.Background(), workitem.Item{ID: "sty_def", Status: "in_progress"}, "done"); err != nil {
		t.Fatal(err)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal([]byte(r.got.Payload), &top); err != nil {
		t.Fatal(err)
	}
	if _, ok := top["deferred"]; ok {
		t.Errorf("a gate that owns no deferral must not receive the field: %s", r.got.Payload)
	}
	if strings.Contains(r.got.Payload, "db tests never ran") {
		t.Error("another gate's deferral leaked into this payload")
	}
}

func TestBundledGatesCarryEachOwnersDeferrals(t *testing.T) {
	docs := fakeDocs{workflow: bundleEdgeWF("rev-a,rev-b,rev-c", "bundle = true"), extraSkills: bundleSkillDocs("rev-a", "rev-b", "rev-c")}
	run := func(items ...DeferredItem) (*capturingRunner, string) {
		cr := &capturingRunner{bundleRunner: &bundleRunner{}}
		g := New(cr, docs, "/repo", "")
		g.SetDeferralsResolver(func(_ context.Context, id string) []DeferredItem {
			if id != "sty_b" {
				return nil
			}
			return items
		})
		if _, err := bundleGate(t, g); err != nil {
			t.Fatal(err)
		}
		if cr.calls() != 1 {
			t.Fatalf("a bundle is one session, got %d calls", cr.calls())
		}
		return cr, cr.reqs[0].Payload
	}

	cr, payload := run(
		DeferredItem{Owner: "rev-a", Objection: "a's evidence"},
		DeferredItem{Owner: "rev-c", Objection: "c's evidence"},
		DeferredItem{Owner: DeferOwnerGrant, Objection: "needs docker"},
	)
	items := deferredFile(t, payload, cr.opened[0])
	if len(items) != 2 || items[0] != (DeferredItem{Owner: "rev-a", Objection: "a's evidence"}) || items[1] != (DeferredItem{Owner: "rev-c", Objection: "c's evidence"}) {
		t.Errorf("bundled deferred = %+v, want rev-a and rev-c each under its own owner", items)
	}
	if !strings.Contains(payload, `"review_skills":["rev-a","rev-b","rev-c"]`) {
		t.Errorf("the bundle must still name its rubrics: %s", payload)
	}

	_, none := run(DeferredItem{Owner: "rev-z", Objection: "owned by a gate outside the bundle"})
	var top map[string]json.RawMessage
	if err := json.Unmarshal([]byte(none), &top); err != nil {
		t.Fatal(err)
	}
	if _, ok := top["deferred"]; ok {
		t.Errorf("a bundle with no owning skill must carry no deferred field: %s", none)
	}
}
