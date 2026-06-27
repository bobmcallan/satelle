package web

import (
	"encoding/json"
	"testing"

	"github.com/bobmcallan/satelle/internal/ledger"
)

// ev builds a ledger entry with a {from,to} payload.
func ev(kind, from, to string) ledger.Entry {
	p, _ := json.Marshal(lightPayload{From: from, To: to})
	return ledger.Entry{Kind: kind, Payload: p}
}

// reverse returns es newest-first, the order ledger-list yields.
func newestFirst(es []ledger.Entry) []ledger.Entry {
	out := make([]ledger.Entry, len(es))
	for i := range es {
		out[len(es)-1-i] = es[i]
	}
	return out
}

func states(ls []reviewLight) []string {
	out := make([]string, len(ls))
	for i, l := range ls {
		out[i] = l.State
	}
	return out
}

func TestBuildLights(t *testing.T) {
	chrono := []ledger.Entry{
		ev(ledger.KindReviewAccept, "open", "in_progress"),
		ev(ledger.KindStatusTransition, "open", "in_progress"),
		ev(ledger.KindReviewReject, "in_progress", "done"),
		ev(ledger.KindReviewAccept, "in_progress", "done"),
		ev(ledger.KindStatusTransition, "in_progress", "done"),
	}
	lights := buildLights(newestFirst(chrono), "done")
	// stage 1 passes; stage 2 fails then passes (shared index); no current (done).
	got := states(lights)
	want := []string{"pass", "fail", "pass"}
	if len(got) != len(want) {
		t.Fatalf("lights = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("light[%d] = %s, want %s", i, got[i], want[i])
		}
	}
	if lights[0].Index != 1 || lights[1].Index != 2 || lights[2].Index != 2 {
		t.Errorf("indices = %d,%d,%d, want 1,2,2", lights[0].Index, lights[1].Index, lights[2].Index)
	}
}

func TestBuildLightsNonTerminalTrailsCurrent(t *testing.T) {
	chrono := []ledger.Entry{
		ev(ledger.KindReviewAccept, "open", "in_progress"),
		ev(ledger.KindStatusTransition, "open", "in_progress"),
	}
	lights := buildLights(newestFirst(chrono), "in_progress")
	if len(lights) != 2 || lights[1].State != "current" || lights[1].Index != 2 {
		t.Fatalf("want [pass, current(2)], got %v", lights)
	}
}

func TestBuildLightsUngatedIsFired(t *testing.T) {
	// A status_transition with no matching review_accept is an ungated checkpoint.
	chrono := []ledger.Entry{ev(ledger.KindStatusTransition, "open", "in_progress")}
	lights := buildLights(newestFirst(chrono), "done")
	if len(lights) != 1 || lights[0].State != "fired" {
		t.Fatalf("want [fired], got %v", lights)
	}
}
