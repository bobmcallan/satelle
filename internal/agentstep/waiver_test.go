package agentstep

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/workitem"
)

// sty_bef97244: a developer waiver reaches the payload of its own story's gate and
// no other, and a verdict records only the waivers the payload actually carried.

const waiverGate = "satelle-story-done-review"

func waiverResolver(items map[string][]WaiverItem) func(context.Context, string) []WaiverItem {
	return func(_ context.Context, id string) []WaiverItem { return items[id] }
}

func waiversFile(t *testing.T, payload string, opened map[string][]byte) []WaiverItem {
	t.Helper()
	var items []WaiverItem
	if err := json.Unmarshal(openedAt(t, opened, payload, "waivers"), &items); err != nil {
		t.Fatal(err)
	}
	return items
}

// AC3 + AC5: the gate's payload lists exactly its own story's waivers whose gate
// is its skill; another gate's waiver, another story's waiver, and a story with
// none carry nothing.
func TestGatePayloadCarriesOnlyItsOwnStoryAndGateWaivers(t *testing.T) {
	own := WaiverItem{ID: "evt_1", Gate: waiverGate, Check: "check_plan_consumed", Reason: "r", Actor: "developer"}
	otherGate := WaiverItem{ID: "evt_2", Gate: "some-other-gate", Check: "check_x", Reason: "r", Actor: "developer"}
	otherStory := WaiverItem{ID: "evt_3", Gate: waiverGate, Check: "check_other_story", Reason: "r", Actor: "developer"}
	resolver := waiverResolver(map[string][]WaiverItem{
		"sty_a": {own, otherGate},
		"sty_b": {otherStory},
	})
	run := func(id string) (*fakeRunner, string) {
		g, r := newEngine(t, `{"decision":"accept"}`, fakeDocs{workflow: testWorkflow, skillBody: "rubric", skillFound: true})
		g.SetWaiversResolver(resolver)
		if _, err := g.Gate(context.Background(), workitem.Item{ID: id, Status: "in_progress"}, "done"); err != nil {
			t.Fatal(err)
		}
		return r, r.got.Payload
	}

	r, payload := run("sty_a")
	items := waiversFile(t, payload, r.opened)
	if len(items) != 1 || items[0] != own {
		t.Errorf("story A's gate payload waivers = %+v, want exactly its own gate's waiver", items)
	}
	for _, leaked := range []string{"check_x", "check_other_story"} {
		if strings.Contains(string(openedAt(t, r.opened, payload, "waivers")), leaked) {
			t.Errorf("%s leaked into story A's payload", leaked)
		}
	}

	// Story B holds a waiver for this same gate: it carries its own, never story A's.
	rb, payloadB := run("sty_b")
	itemsB := waiversFile(t, payloadB, rb.opened)
	if len(itemsB) != 1 || itemsB[0] != otherStory {
		t.Errorf("story B's gate payload waivers = %+v, want only its own", itemsB)
	}

	// A story with no waiver carries no waivers field at all.
	_, payloadNone := run("sty_none")
	var top map[string]json.RawMessage
	if err := json.Unmarshal([]byte(payloadNone), &top); err != nil {
		t.Fatal(err)
	}
	if _, ok := top["waivers"]; ok {
		t.Errorf("a story with no waiver must carry no waivers field: %s", payloadNone)
	}

	// A story whose only waiver names another gate carries none for this one.
	g, r2 := newEngine(t, `{"decision":"accept"}`, fakeDocs{workflow: testWorkflow, skillBody: "rubric", skillFound: true})
	g.SetWaiversResolver(waiverResolver(map[string][]WaiverItem{"sty_c": {otherGate}}))
	if _, err := g.Gate(context.Background(), workitem.Item{ID: "sty_c", Status: "in_progress"}, "done"); err != nil {
		t.Fatal(err)
	}
	var topC map[string]json.RawMessage
	if err := json.Unmarshal([]byte(r2.got.Payload), &topC); err != nil {
		t.Fatal(err)
	}
	if _, ok := topC["waivers"]; ok {
		t.Errorf("another gate's waiver must not reach this gate: %s", r2.got.Payload)
	}
}

// AC4: a verdict naming a honoured waiver has it on its row; a waived entry whose
// check has no payload waiver is dropped.
func TestVerdictKeepsOnlyWaivedChecksThePayloadCarried(t *testing.T) {
	granted := WaiverItem{ID: "evt_1", Gate: waiverGate, Check: "check_plan_consumed", Reason: "r", Actor: "developer"}
	cases := []struct {
		name     string
		reply    string
		waivers  []WaiverItem
		wantKept []string
	}{
		{"honoured", `{"decision":"accept","waived":["check_plan_consumed"]}`, []WaiverItem{granted}, []string{"check_plan_consumed"}},
		{"unwaived check dropped", `{"decision":"accept","waived":["check_plan_consumed","check_other"]}`, []WaiverItem{granted}, []string{"check_plan_consumed"}},
		{"no payload waiver at all", `{"decision":"accept","waived":["check_plan_consumed"]}`, nil, nil},
		{"duplicate collapsed", `{"decision":"accept","waived":["check_plan_consumed","check_plan_consumed"]}`, []WaiverItem{granted}, []string{"check_plan_consumed"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g, _ := newEngine(t, tc.reply, fakeDocs{workflow: testWorkflow, skillBody: "rubric", skillFound: true})
			g.SetWaiversResolver(waiverResolver(map[string][]WaiverItem{"sty_w": tc.waivers}))
			dec, err := g.Gate(context.Background(), workitem.Item{ID: "sty_w", Status: "in_progress"}, "done")
			if err != nil {
				t.Fatal(err)
			}
			if len(dec.Reviewers) == 0 {
				t.Fatalf("no per-reviewer verdict: %+v", dec)
			}
			got := dec.Reviewers[0].Waived
			if strings.Join(got, ",") != strings.Join(tc.wantKept, ",") {
				t.Errorf("verdict waived = %v, want %v", got, tc.wantKept)
			}
			if strings.Join(dec.Waived, ",") != strings.Join(tc.wantKept, ",") {
				t.Errorf("decision waived = %v, want %v", dec.Waived, tc.wantKept)
			}
		})
	}
}

// A bundled session merges each gate's waivers under its own gate label.
func TestBundledGatesCarryEachGatesWaivers(t *testing.T) {
	docs := fakeDocs{workflow: bundleEdgeWF("rev-a,rev-b", "bundle = true"), extraSkills: bundleSkillDocs("rev-a", "rev-b")}
	cr := &capturingRunner{bundleRunner: &bundleRunner{}}
	g := New(cr, docs, "/repo", "")
	g.SetWaiversResolver(waiverResolver(map[string][]WaiverItem{"sty_b": {
		{ID: "e1", Gate: "rev-a", Check: "check_a", Actor: "developer"},
		{ID: "e2", Gate: "rev-z", Check: "check_z", Actor: "developer"},
	}}))
	if _, err := bundleGate(t, g); err != nil {
		t.Fatal(err)
	}
	items := waiversFile(t, cr.reqs[0].Payload, cr.opened[0])
	if len(items) != 1 || items[0].Gate != "rev-a" {
		t.Errorf("bundled waivers = %+v, want only rev-a's", items)
	}
}
