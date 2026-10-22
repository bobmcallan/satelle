package verb_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/verb"
)

// sty_da9e8d0a: a panel verdict's seats keep their own quotation and attempt in
// the review row, and PriorVerdicts reads them back for the next presentation.

// TestPanelSeatRecordJSONCarriesReviewedAndAttempt: the seat record the row
// serialises holds the quotation, its truncation flag, the attempt and the
// attempt a carried accept came from; a plain seat adds none of them.
func TestPanelSeatRecordJSONCarriesReviewedAndAttempt(t *testing.T) {
	b, err := json.Marshal(verb.PanelSeat{Seat: "s1", Accept: true, Reviewed: "Q1", Attempt: 3, CarriedFrom: 2})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"reviewed":"Q1"`, `"attempt":3`, `"carried_from":2`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("seat JSON %s lacks %s", b, want)
		}
	}
	b, _ = json.Marshal(verb.PanelSeat{Seat: "s2", ReviewedTruncated: true})
	if !strings.Contains(string(b), `"reviewed_truncated":true`) || strings.Contains(string(b), `"reviewed":`) {
		t.Errorf("a truncated seat carries the flag and no quotation: %s", b)
	}
}

// TestPriorVerdictsFromParsesSeatsAndStaleClearsThem: the read returns each
// seat's own record, and a stale latest row loses every seat's quotation with
// its own while an older row keeps what it recorded.
func TestPriorVerdictsFromParsesSeatsAndStaleClearsThem(t *testing.T) {
	t0 := time.Now().Add(-time.Hour)
	row := func(at time.Time, recorded time.Time, seats []verb.PanelSeat) ledger.Entry {
		raw, err := json.Marshal(map[string]any{
			"from": "plan", "to": "in_progress", "skill": "pv-plan-review", "order": 0,
			"notes": "COMBINED", "accept": false, "recorded_at": recorded.UTC().Format(time.RFC3339Nano),
			"seats": seats,
		})
		if err != nil {
			t.Fatal(err)
		}
		return ledger.Entry{Kind: ledger.KindReviewReject, Payload: raw, CreatedAt: at}
	}
	older := []verb.PanelSeat{{Seat: "s1", Accept: true, Reviewed: "OLD-Q1", Attempt: 1}}
	latest := []verb.PanelSeat{
		{Seat: "s1", Accept: true, Reviewed: "Q1", Attempt: 2},
		{Seat: "s2", Unavailable: true, Notes: "timeout", Attempt: 2},
	}
	entries := []ledger.Entry{
		row(t0, t0.Add(time.Second), older),
		row(t0.Add(time.Minute), t0.Add(time.Minute+time.Second), latest),
	}

	got := verb.PriorVerdictsFrom(entries, "plan", "in_progress", verb.StaleExemption{})
	if len(got) != 2 || len(got[0].Seats) != 1 || got[0].Seats[0].Reviewed != "OLD-Q1" {
		t.Fatalf("older row = %+v, want its seat with the quotation", got)
	}
	if len(got[1].Seats) != 2 || got[1].Seats[0].Reviewed != "Q1" || got[1].Seats[0].Attempt != 2 || !got[1].Seats[1].Unavailable {
		t.Fatalf("latest row seats = %+v", got[1].Seats)
	}

	// Evidence recorded after the latest cycle ended marks it stale.
	evidence := ledger.Entry{Kind: ledger.KindTelemetryEvent, Payload: json.RawMessage(`{"kind":"plan-consumed"}`), CreatedAt: t0.Add(time.Hour)}
	got = verb.PriorVerdictsFrom(append(entries, evidence), "plan", "in_progress", verb.StaleExemption{})
	if !got[1].ReviewedStale {
		t.Fatalf("latest row = %+v, want stale", got[1])
	}
	for _, s := range got[1].Seats {
		if s.Reviewed != "" || s.ReviewedTruncated {
			t.Errorf("a stale row must clear seat %s's quotation: %+v", s.Seat, s)
		}
	}
	if got[1].Seats[0].Accept != true || got[1].Seats[1].Notes != "timeout" {
		t.Errorf("only quotations are cleared: %+v", got[1].Seats)
	}
	if got[0].Seats[0].Reviewed != "OLD-Q1" {
		t.Errorf("an older row is not re-marked: %+v", got[0].Seats)
	}
}
