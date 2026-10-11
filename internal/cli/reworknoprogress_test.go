package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/store"
)

// A relay whose coder changes nothing and whose consultant repeats itself stops
// at round two with the distinct no_progress outcome, instead of spending the
// budget (sty_e596be56 AC1).
func TestReworkRelayStopsOnNoProgress(t *testing.T) {
	consultant := newScriptSess("NOT READY: x", "Still the same.\nNOT READY:  X ", "NOT READY: x", "NOT READY: x", "NOT READY: x")
	coder := newScriptSess("still waiting", "still waiting")
	var out bytes.Buffer
	l := newReworkLoop(coder, consultant, 5, &memLedger{}, &out)
	l.Evidence = func(context.Context) (string, error) { return "same", nil }

	res, err := l.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Outcome != outcomeNoProgress || res.Converged || res.Rounds != 2 {
		t.Fatalf("result = %+v; want no_progress after 2 rounds", res)
	}
	if res.LastObjection != "X" {
		t.Errorf("last objection = %q; want the one that repeated", res.LastObjection)
	}
	if len(consultant.turns) != 2 || len(coder.turns) != 1 {
		t.Errorf("turns consult=%d coder=%d; want 2 and 1 — no further rounds spent", len(consultant.turns), len(coder.turns))
	}
	if !strings.Contains(out.String(), "no progress") {
		t.Errorf("output does not name the outcome:\n%s", out.String())
	}
}

// Rounds that each bring new evidence run to the authored budget (AC3).
func TestReworkRelayWithEvidenceRunsToBudget(t *testing.T) {
	consultant := newScriptSess("NOT READY: x", "NOT READY: x", "NOT READY: x")
	coder := newScriptSess("a", "b", "c")
	l := newReworkLoop(coder, consultant, 3, &memLedger{}, &bytes.Buffer{})
	n := 0
	l.Evidence = func(context.Context) (string, error) { n++; return fmt.Sprint(n), nil }

	res, err := l.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Outcome != outcomeBudget || res.Rounds != 3 {
		t.Fatalf("result = %+v; want the budget spent across 3 rounds", res)
	}
}

// A changed objection is progress even when nothing else changed.
func TestReworkRelayChangedObjectionIsProgress(t *testing.T) {
	consultant := newScriptSess("NOT READY: a", "NOT READY: b", "NOT READY: c")
	coder := newScriptSess("x", "y", "z")
	l := newReworkLoop(coder, consultant, 3, &memLedger{}, &bytes.Buffer{})
	l.Evidence = func(context.Context) (string, error) { return "same", nil }

	res, err := l.Run(context.Background())
	if err != nil || res.Outcome != outcomeBudget || res.Rounds != 3 {
		t.Fatalf("result = %+v, err %v; want budget after 3 rounds", res, err)
	}
}

// A detector that fails never cuts a relay short.
func TestReworkRelayFailingEvidenceCountsAsProgress(t *testing.T) {
	consultant := newScriptSess("NOT READY: x", "NOT READY: x")
	coder := newScriptSess("a", "b")
	l := newReworkLoop(coder, consultant, 2, &memLedger{}, &bytes.Buffer{})
	l.Evidence = func(context.Context) (string, error) { return "", fmt.Errorf("boom") }

	res, err := l.Run(context.Background())
	if err != nil || res.Outcome != outcomeBudget || res.Rounds != 2 {
		t.Fatalf("result = %+v, err %v; want budget after 2 rounds", res, err)
	}
}

func TestReworkRelayOutcomesWithoutDetector(t *testing.T) {
	res, err := newReworkLoop(newScriptSess("done"), newScriptSess("NOT READY: x", "READY"), 3, &memLedger{}, &bytes.Buffer{}).Run(context.Background())
	if err != nil || res.Outcome != outcomeConverged {
		t.Fatalf("result = %+v, err %v; want converged", res, err)
	}
}

// The fingerprint ignores the relay's own rows and moves on real evidence
// (AC3: the relay's chatter is never progress).
func TestReworkEvidenceFingerprintIgnoresRelayRows(t *testing.T) {
	tempRepo(t)
	db, err := store.Open(runtimeDBPath(t))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	ctx := context.Background()
	const id = "sty_fp1"
	add := func(kind string, payload map[string]any) {
		t.Helper()
		raw, _ := json.Marshal(payload)
		if _, err := db.Ledger.Append(ctx, ledger.AppendInput{StoryID: id, Kind: kind, Actor: "t", Body: kind, Payload: raw}, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	fp := func() string {
		t.Helper()
		got, err := reworkEvidenceFingerprint(ctx, db.Ledger, id)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}

	add(ledger.KindTelemetryEvent, map[string]any{"kind": "plan-consumed"})
	before := fp()
	add(ledger.KindAgentMessage, map[string]any{"from": "consult", "to": "coder", "cc": "*", "body": "NOT READY"})
	add(ledger.KindToolPermission, map[string]any{})
	add(ledger.KindAgentInvocation, map[string]any{})
	if after := fp(); after != before {
		t.Fatalf("relay transcript and bookkeeping rows changed the fingerprint")
	}
	add(ledger.KindTelemetryEvent, map[string]any{"kind": "ac-evidence"})
	if after := fp(); after == before {
		t.Fatalf("a story log row did not change the fingerprint")
	}
}

// recordReworkResult carries the outcome, the rounds used and the last
// objection (AC2), and the stop reason for no_progress.
func TestRecordReworkResultCarriesOutcome(t *testing.T) {
	tempRepo(t)
	db, err := store.Open(runtimeDBPath(t))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	ctx := context.Background()
	rw := reworkPlan{CoderBinding: "coder", ConsultBinding: "consult", Rounds: 5}

	for _, tc := range []struct {
		res    reworkResult
		reason bool
	}{
		{reworkResult{Outcome: outcomeNoProgress, Rounds: 2, LastObjection: "x"}, true},
		{reworkResult{Outcome: outcomeBudget, Rounds: 5, LastObjection: "y"}, false},
		{reworkResult{Outcome: outcomeConverged, Converged: true, Rounds: 1}, false},
	} {
		id := "sty_rec_" + tc.res.Outcome
		recordReworkResult(ctx, db.Ledger, id, rw, tc.res)
		rows, err := db.Ledger.ListByStory(ctx, id, ledger.KindAgentInvocation)
		if err != nil || len(rows) != 1 {
			t.Fatalf("%s: rows = %d, err %v", tc.res.Outcome, len(rows), err)
		}
		var p map[string]any
		if err := json.Unmarshal(rows[0].Payload, &p); err != nil {
			t.Fatal(err)
		}
		if p["outcome"] != tc.res.Outcome || int(p["rounds"].(float64)) != tc.res.Rounds || p["last_objection"] != nil && p["last_objection"] != tc.res.LastObjection {
			t.Errorf("%s: payload = %v", tc.res.Outcome, p)
		}
		if _, has := p["stopped_reason"]; has != tc.reason {
			t.Errorf("%s: stopped_reason present = %t, want %t", tc.res.Outcome, has, tc.reason)
		}
		if !strings.Contains(rows[0].Body, "outcome="+tc.res.Outcome) {
			t.Errorf("%s: body = %q", tc.res.Outcome, rows[0].Body)
		}
	}
}
