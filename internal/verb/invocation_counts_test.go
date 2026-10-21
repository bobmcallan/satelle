package verb_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// sty_46e950ab AC1/AC2 at the row writer (verb.invocationPayload): every reviewer
// agent_invocation row carries its attempt id and its turns and tool calls — or
// the reason each is unavailable, never a bare zero — and an attempt that
// produced no verdict is written, with its outcome, from a GateFailure.

type invocationRow struct {
	Attempt                    string `json:"attempt"`
	Skill                      string `json:"skill"`
	Seat                       string `json:"seat"`
	Outcome                    string `json:"outcome"`
	Turns                      *int   `json:"turns"`
	TurnsUnavailableReason     string `json:"turns_unavailable_reason"`
	ToolCalls                  *int   `json:"tool_calls"`
	ToolCallsUnavailableReason string `json:"tool_calls_unavailable_reason"`
	TokensOut                  int    `json:"tokens_out"`
	UsageAvailable             bool   `json:"usage_available"`
	BundleID                   string `json:"bundle_id"`
}

func intp(n int) *int { return &n }

// errGater returns a decision alongside a gate error, as a reviewer that ran and
// produced no verdict does.
type errGater struct {
	dec verb.GateDecision
	err error
}

func (g errGater) Gate(context.Context, workitem.Item, string) (verb.GateDecision, error) {
	return g.dec, g.err
}

// gateOnce creates a story at in_progress and presents it for done under gater,
// returning its id and the presentation's error. The store is the test's own
// (wire opens a fresh one per call, so each test opens it once).
func gateOnce(t *testing.T, gater verb.TransitionGater) (*store.DB, string, error) {
	t.Helper()
	db := wire(t)
	verb.SetTransitionGater(gater)
	var it workitem.Item
	if err := json.Unmarshal(call(t, "story-create", map[string]any{"title": "x", "status": "in_progress"}), &it); err != nil {
		t.Fatal(err)
	}
	_, err := dispatchRaw(t, "story-set", map[string]any{"id": it.ID, "status": "done"})
	return db, it.ID, err
}

func invocationRows(t *testing.T, db *store.DB, id string) []invocationRow {
	t.Helper()
	entries, err := db.Ledger.ListByStory(context.Background(), id, ledger.KindAgentInvocation)
	if err != nil {
		t.Fatal(err)
	}
	var out []invocationRow
	for _, e := range entries {
		var r invocationRow
		if err := json.Unmarshal(e.Payload, &r); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

func verdictAttempt(t *testing.T, db *store.DB, id, kind string) string {
	t.Helper()
	entries, err := db.Ledger.ListByStory(context.Background(), id, kind)
	if err != nil || len(entries) != 1 {
		t.Fatalf("%s rows = %d, err %v", kind, len(entries), err)
	}
	var r struct {
		Attempt string `json:"attempt"`
	}
	if err := json.Unmarshal(entries[0].Payload, &r); err != nil {
		t.Fatal(err)
	}
	return r.Attempt
}

func TestInvocationRowCarriesAttemptTurnsAndToolCalls(t *testing.T) {
	dec := verb.GateDecision{
		Gated: true, Accept: true, Skill: "rev", Command: "claude -p", Context: "rev",
		RunCounts: verb.RunCounts{Turns: intp(7), ToolCalls: intp(0)},
	}
	db, id, err := gateOnce(t, errGater{dec: dec})
	if err != nil {
		t.Fatal(err)
	}
	rows := invocationRows(t, db, id)
	if len(rows) != 1 {
		t.Fatalf("rows = %d", len(rows))
	}
	r := rows[0]
	if r.Turns == nil || *r.Turns != 7 || r.TurnsUnavailableReason != "" {
		t.Errorf("turns = %v (%q), want 7", r.Turns, r.TurnsUnavailableReason)
	}
	// A measured zero stays a zero — present, not omitted.
	if r.ToolCalls == nil || *r.ToolCalls != 0 || r.ToolCallsUnavailableReason != "" {
		t.Errorf("tool_calls = %v (%q), want a measured 0", r.ToolCalls, r.ToolCallsUnavailableReason)
	}
	if want := verdictAttempt(t, db, id, ledger.KindReviewAccept); r.Attempt == "" || r.Attempt != want {
		t.Errorf("attempt = %q, want the verdict row's %q", r.Attempt, want)
	}
}

// A row whose run reported no figure names the harness binary and says so; the
// writer never leaves a bare absence.
func TestInvocationRowNamesAReasonForAFigureNobodyReported(t *testing.T) {
	dec := verb.GateDecision{Gated: true, Accept: true, Skill: "rev", Command: "mystery-cli -p {payload}", Context: "rev"}
	db, id, err := gateOnce(t, errGater{dec: dec})
	if err != nil {
		t.Fatal(err)
	}
	r := invocationRows(t, db, id)[0]
	if r.Turns != nil || r.ToolCalls != nil {
		t.Errorf("an unreported figure is absent, never zero: turns %v tool_calls %v", r.Turns, r.ToolCalls)
	}
	if !strings.HasPrefix(r.TurnsUnavailableReason, "mystery-cli:") || !strings.HasPrefix(r.ToolCallsUnavailableReason, "mystery-cli:") {
		t.Errorf("reasons = %q / %q, want them to name the harness", r.TurnsUnavailableReason, r.ToolCallsUnavailableReason)
	}
	// The engine's adapter-named reason wins over the writer's fallback.
	dec.RunCounts = verb.RunCounts{TurnsUnavailableReason: "grok command: no turn count", ToolCallsUnavailableReason: "grok command: no tool events"}
	db, id, err = gateOnce(t, errGater{dec: dec})
	if err != nil {
		t.Fatal(err)
	}
	r = invocationRows(t, db, id)[0]
	if r.TurnsUnavailableReason != "grok command: no turn count" || r.ToolCallsUnavailableReason != "grok command: no tool events" {
		t.Errorf("reasons = %q / %q, want the engine's", r.TurnsUnavailableReason, r.ToolCallsUnavailableReason)
	}
}

func TestPanelSeatRowsCarryTheAttemptAndTheirOwnCounts(t *testing.T) {
	judged := verb.ReviewerVerdict{
		Skill: "rev", Seat: "seat-a", Command: "claude -p", Context: "rev",
		RunCounts: verb.RunCounts{Turns: intp(4), ToolCalls: intp(9)},
	}
	cut := verb.ReviewerVerdict{
		Skill: "rev", Seat: "seat-b", Command: "grok -p", Context: "rev", Outcome: verb.OutcomeMaxTurns,
		RunCounts: verb.RunCounts{TurnsUnavailableReason: "grok command: no turn count", ToolCallsUnavailableReason: "grok command: no tool events"},
	}
	dec := verb.GateDecision{Gated: true, Accept: true, Skill: "rev", Reviewers: []verb.ReviewerVerdict{{
		Skill: "rev", Accept: true,
		Seats:      []verb.PanelSeat{{Seat: "seat-a", Accept: true}, {Seat: "seat-b", Unavailable: true, MaxTurns: true}},
		SeatStamps: []verb.ReviewerVerdict{judged, cut},
	}}}
	db, id, err := gateOnce(t, errGater{dec: dec})
	if err != nil {
		t.Fatal(err)
	}
	rows := invocationRows(t, db, id)
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want one per seat that started", len(rows))
	}
	want := verdictAttempt(t, db, id, ledger.KindReviewAccept)
	for _, r := range rows {
		if r.Attempt != want {
			t.Errorf("seat %s attempt = %q, want the skill verdict's %q", r.Seat, r.Attempt, want)
		}
	}
	if a := rows[0]; a.Seat != "seat-a" || a.Outcome != "" || a.Turns == nil || *a.Turns != 4 || a.ToolCalls == nil || *a.ToolCalls != 9 {
		t.Errorf("seat-a row = %+v", a)
	}
	if b := rows[1]; b.Seat != "seat-b" || b.Outcome != verb.OutcomeMaxTurns || b.Turns != nil || b.TurnsUnavailableReason == "" {
		t.Errorf("seat-b row = %+v", b)
	}
}

func TestBundleRowIsWrittenOnceWithItsCounts(t *testing.T) {
	first := verb.ReviewerVerdict{
		Skill: "rev-a", Accept: true, Command: "claude -p", Context: "rev-a", BundleID: "bun_1",
		BundleSkills: []string{"rev-a", "rev-b"}, RunCounts: verb.RunCounts{Turns: intp(5), ToolCalls: intp(2)},
	}
	second := verb.ReviewerVerdict{Skill: "rev-b", Accept: true, Command: "claude -p", Context: "rev-b", BundleID: "bun_1", BundleSkills: []string{"rev-a", "rev-b"}}
	db, id, err := gateOnce(t, errGater{dec: verb.GateDecision{Gated: true, Accept: true, Skill: "rev-b", Reviewers: []verb.ReviewerVerdict{first, second}}})
	if err != nil {
		t.Fatal(err)
	}
	rows := invocationRows(t, db, id)
	if len(rows) != 1 {
		t.Fatalf("a bundle is one measured call, one row; got %d", len(rows))
	}
	if r := rows[0]; r.BundleID != "bun_1" || r.Turns == nil || *r.Turns != 5 || r.ToolCalls == nil || *r.ToolCalls != 2 || r.Attempt == "" {
		t.Errorf("bundle row = %+v", r)
	}
}

// AC2: a single-seat attempt that ends at the turn cap returns a GateFailure; the
// round writer records its invocation — outcome, attempt, the usage it reported —
// and writes NO verdict row, so it is not a reject.
func TestGateFailureWritesAMaxTurnsRowAndNoVerdict(t *testing.T) {
	stamp := verb.ReviewerVerdict{
		Skill: "rev", Command: "claude -p", Context: "rev", Outcome: verb.OutcomeMaxTurns,
		TokensOut: 1893, UsageAvailable: true,
		RunCounts: verb.RunCounts{Turns: intp(9), ToolCalls: intp(8)},
	}
	cause := errors.New("the harness ended the run at its turn cap")
	db, id, err := gateOnce(t, errGater{dec: verb.GateDecision{Gated: true, Skill: "rev"}, err: &verb.GateFailure{Err: cause, Stamps: []verb.ReviewerVerdict{stamp}}})
	if err == nil || !strings.Contains(err.Error(), "turn cap") {
		t.Fatalf("err = %v, want the gate's own error", err)
	}
	rows := invocationRows(t, db, id)
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want the failed attempt's one", len(rows))
	}
	r := rows[0]
	if r.Outcome != verb.OutcomeMaxTurns || !strings.HasPrefix(r.Attempt, "att_") {
		t.Errorf("outcome %q attempt %q", r.Outcome, r.Attempt)
	}
	if !r.UsageAvailable || r.TokensOut != 1893 || r.Turns == nil || *r.Turns != 9 || r.ToolCalls == nil || *r.ToolCalls != 8 {
		t.Errorf("the usage and counts it reported are kept: %+v", r)
	}
	for _, kind := range []string{ledger.KindReviewAccept, ledger.KindReviewReject} {
		es, lerr := db.Ledger.ListByStory(context.Background(), id, kind)
		if lerr != nil || len(es) != 0 {
			t.Errorf("a failed attempt writes no %s row: %d, %v", kind, len(es), lerr)
		}
	}
}
