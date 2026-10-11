package verb_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/bobmcallan/satelle/internal/costview"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// sty_c5171eb9: a lifecycle-hook reviewer run (create_review, amend_review) is
// recorded on the ledger like an edge gate's — an agent_invocation row carrying
// executable, model, usage (or its adapter-named unavailable) and duration, then
// the review verdict row with the reviewer's notes. A rejected create persists no
// story, so its rows are repo-scoped (empty story id).

const (
	createReviewSkill = "satelle-story-create-review"
	amendReviewSkill  = "satelle-story-amend-review"
)

// lifecycleDecision is what the engine returns for a declared hook that ran an
// agent: command, model and measured usage. cost is the provider's own figure.
func lifecycleDecision(skill string, accept bool, notes string) verb.GateDecision {
	cost := 0.25
	return verb.GateDecision{
		Gated: true, Accept: accept, Skill: skill, Notes: notes,
		Command: "claude -p", Context: skill, Model: "opus", ModelResolved: "claude-opus-5-5",
		TokensIn: 1200, TokensOut: 300, TokensTotal: 1500, DurationMs: 4200,
		UsageAvailable: true, TokensInFresh: 200, TokensCacheRead: 1000,
		UsageNote: verb.UsageNote{Adapter: "claude"},
		CostUSD:   &cost,
	}
}

func entriesOf(t *testing.T, db *store.DB, storyID, kind string) []ledger.Entry {
	t.Helper()
	var out []ledger.Entry
	if err := db.Ledger.ForEachKind(context.Background(), storyID, kind, func(e ledger.Entry) error {
		out = append(out, e)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

// skillRow finds a skill's gate-value row whatever seat label its model gave it
// (the seat names the RESOLVED model when the transport reported one).
func skillRow(t *testing.T, rep costview.GateValueReport, skill string) costview.GateValueRow {
	t.Helper()
	for _, r := range rep.Rows {
		if r.Skill == skill {
			return r
		}
	}
	t.Fatalf("no %s row in %+v", skill, rep.Rows)
	return costview.GateValueRow{}
}

func decodeJSON(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("payload %s: %v", raw, err)
	}
	return m
}

// AC1 + AC4: a gated create carries the invocation and accept rows on the new
// story, and `story cost` folds the spend in.
func TestCreateReviewAcceptRecordedOnStory(t *testing.T) {
	withWiring(t)
	db := wire(t)
	verb.SetCreateReviewer(createStub{dec: lifecycleDecision(createReviewSkill, true, "well formed")})

	var it workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{"title": "well formed", "acceptance_criteria": "1. works"}), &it)

	inv := entriesOf(t, db, it.ID, ledger.KindAgentInvocation)
	if len(inv) != 1 {
		t.Fatalf("agent_invocation rows on the created story = %d, want 1", len(inv))
	}
	p := decodeJSON(t, inv[0].Payload)
	for k, want := range map[string]any{
		"skill": createReviewSkill, "command": "claude -p", "model": "opus",
		"operation": "create_review", "from": "create", "to": "backlog",
		"tokens_in_fresh": float64(200), "tokens_cache_read": float64(1000), "tokens_out": float64(300),
		"duration_ms": float64(4200), "usage_available": true, "cost_usd": 0.25,
	} {
		if p[k] != want {
			t.Errorf("invocation payload %s = %v, want %v (%s)", k, p[k], want, inv[0].Payload)
		}
	}
	acc := entriesOf(t, db, it.ID, ledger.KindReviewAccept)
	if len(acc) != 1 {
		t.Fatalf("review_accept rows = %d, want 1", len(acc))
	}
	if ap := decodeJSON(t, acc[0].Payload); ap["notes"] != "well formed" || ap["skill"] != createReviewSkill {
		t.Errorf("review_accept payload = %s", acc[0].Payload)
	}

	sc, err := verb.ComputeStoryCost(context.Background(), it.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(sc.Rows) != 1 || sc.TotalTokens != 1500 || sc.TotalCostUSD != 0.25 {
		t.Fatalf("story cost must include the create-review run: %+v", sc)
	}
}

// AC3 + AC4: a rejected create persists no story; its run is recorded
// repo-scoped and the repo-wide gate-value view reads it.
func TestCreateReviewRejectRecordedRepoScoped(t *testing.T) {
	withWiring(t)
	db := wire(t)
	verb.SetCreateReviewer(createStub{dec: lifecycleDecision(createReviewSkill, false, "add numbered criteria")})

	if _, err := dispatchRaw(t, "story-create", map[string]any{"title": "vague"}); err == nil {
		t.Fatal("expected the create to be rejected")
	}
	var items []workitem.Item
	json.Unmarshal(call(t, "story-list", map[string]any{}), &items)
	if len(items) != 0 {
		t.Fatalf("rejected draft persisted: %d items", len(items))
	}

	inv := entriesOf(t, db, "", ledger.KindAgentInvocation)
	rej := entriesOf(t, db, "", ledger.KindReviewReject)
	if len(inv) != 1 || len(rej) != 1 {
		t.Fatalf("repo-scoped rows: invocation %d, reject %d, want 1 and 1", len(inv), len(rej))
	}
	if inv[0].StoryID != "" || rej[0].StoryID != "" {
		t.Errorf("a rejected create has no story to carry the rows: %q / %q", inv[0].StoryID, rej[0].StoryID)
	}
	if p := decodeJSON(t, inv[0].Payload); p["skill"] != createReviewSkill || p["model"] != "opus" ||
		p["command"] != "claude -p" || p["duration_ms"] != float64(4200) || p["operation"] != "create_review" {
		t.Errorf("invocation payload = %s", inv[0].Payload)
	}
	if p := decodeJSON(t, rej[0].Payload); p["notes"] != "add numbered criteria" {
		t.Errorf("review_reject payload = %s", rej[0].Payload)
	}

	rep, err := verb.ComputeGateValue(context.Background(), verb.GateValueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	r := skillRow(t, rep, createReviewSkill)
	if r.Invocations != 1 || r.Rejects != 1 || r.CostUSD != 0.25 {
		t.Errorf("gate-value row = %+v, want 1 invocation, 1 reject, $0.25", r)
	}
}

// AC2 + AC4: an amend records the same rows on the amended story, accept or
// reject, and the gate-value view counts them alongside the create run.
func TestAmendReviewRecordedOnAmendedStory(t *testing.T) {
	for _, tc := range []struct {
		name   string
		accept bool
	}{{"accept", true}, {"reject", false}} {
		t.Run(tc.name, func(t *testing.T) {
			stub := &amendStub{dec: lifecycleDecision(amendReviewSkill, tc.accept, "AC2 was false")}
			db := wireAmend(t, stub)
			st := engagedStory(t, "Wrong ACs", "1. original\n2. false claim")

			_, err := dispatchRaw(t, "story-amend", map[string]any{
				"id": st.ID, "acceptance_criteria": "1. original\n2. corrected", "reason": "AC2 was false",
			})
			if (err == nil) != tc.accept {
				t.Fatalf("amend err = %v, accept = %v", err, tc.accept)
			}

			inv := entriesOf(t, db, st.ID, ledger.KindAgentInvocation)
			if len(inv) != 1 {
				t.Fatalf("agent_invocation rows on the amended story = %d, want 1", len(inv))
			}
			if p := decodeJSON(t, inv[0].Payload); p["skill"] != amendReviewSkill || p["operation"] != "amend_review" ||
				p["from"] != "in_progress" || p["to"] != "in_progress" || p["model"] != "opus" {
				t.Errorf("invocation payload = %s", inv[0].Payload)
			}
			verdictKind := ledger.KindReviewAccept
			if !tc.accept {
				verdictKind = ledger.KindReviewReject
			}
			vs := entriesOf(t, db, st.ID, verdictKind)
			if len(vs) != 1 {
				t.Fatalf("%s rows = %d, want 1", verdictKind, len(vs))
			}
			if p := decodeJSON(t, vs[0].Payload); p["notes"] != "AC2 was false" || p["operation"] != "amend_review" {
				t.Errorf("%s payload = %s", verdictKind, vs[0].Payload)
			}

			sc, cerr := verb.ComputeStoryCost(context.Background(), st.ID)
			if cerr != nil {
				t.Fatal(cerr)
			}
			if sc.TotalCostUSD != 0.25 || sc.TotalTokens != 1500 {
				t.Errorf("story cost must include the amend-review run: %+v", sc)
			}
			rep, gerr := verb.ComputeGateValue(context.Background(), verb.GateValueOptions{StoryID: st.ID})
			if gerr != nil {
				t.Fatal(gerr)
			}
			if r := skillRow(t, rep, amendReviewSkill); r.Invocations != 1 {
				t.Errorf("gate-value amend row = %+v", r)
			}
		})
	}
}

// AC5: the rows carry whatever the adapter measured, or its named unavailable —
// never a silent zero. One fixture per adapter family (claude measured, grok
// without a usage report).
func TestCreateReviewRecordsAdapterUsageOrNamedUnavailable(t *testing.T) {
	grok := verb.GateDecision{
		Gated: true, Accept: true, Skill: createReviewSkill, Notes: "ok",
		Command: "grok --print", Context: createReviewSkill, Model: "grok-build",
		DurationMs: 3100, UsageAvailable: false,
		UsageNote:             verb.UsageNote{Adapter: "grok", UsageUnavailableReason: "grok command: no usage in output"},
		CostUnavailableReason: "grok: no cost reported",
	}
	for _, tc := range []struct {
		name       string
		dec        verb.GateDecision
		wantUsage  bool
		wantReason string
	}{
		{"claude", lifecycleDecision(createReviewSkill, true, "ok"), true, ""},
		{"grok", grok, false, "grok command: no usage in output"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withWiring(t)
			db := wire(t)
			verb.SetCreateReviewer(createStub{dec: tc.dec})
			var it workitem.Item
			json.Unmarshal(call(t, "story-create", map[string]any{"title": "well formed", "acceptance_criteria": "1. works"}), &it)

			inv := entriesOf(t, db, it.ID, ledger.KindAgentInvocation)
			if len(inv) != 1 {
				t.Fatalf("agent_invocation rows = %d, want 1", len(inv))
			}
			p := decodeJSON(t, inv[0].Payload)
			if p["usage_available"] != tc.wantUsage {
				t.Errorf("usage_available = %v, want %v (%s)", p["usage_available"], tc.wantUsage, inv[0].Payload)
			}
			if tc.wantReason != "" && p["usage_unavailable_reason"] != tc.wantReason {
				t.Errorf("usage_unavailable_reason = %v, want %q", p["usage_unavailable_reason"], tc.wantReason)
			}
			sc, err := verb.ComputeStoryCost(context.Background(), it.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !tc.wantUsage && (sc.UnmeasuredRows != 1 || sc.TotalTokens != 0) {
				t.Errorf("an unreported-usage run must read unmeasured, not zero tokens: %+v", sc)
			}
		})
	}
}

// A deterministic accept (the structure check, or a repo declaring no hook)
// invoked no agent and writes no row; a gate-less create is unchanged.
func TestCreateStructureAcceptWritesNoLifecycleRows(t *testing.T) {
	withWiring(t)
	db := wire(t)
	verb.SetCreateReviewer(createStub{dec: verb.GateDecision{Gated: true, Accept: true, Skill: "structure"}})
	var it workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{"title": "well formed", "acceptance_criteria": "1. works"}), &it)
	for _, k := range []string{ledger.KindAgentInvocation, ledger.KindReviewAccept, ledger.KindReviewReject} {
		if n := len(entriesOf(t, db, "", k)); n != 0 {
			t.Errorf("%s rows = %d, want 0 for a deterministic accept", k, n)
		}
	}
}
