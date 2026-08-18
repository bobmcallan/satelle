package verb

import (
	"testing"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/wfdot"
	"github.com/bobmcallan/satelle/internal/workitem"
)

func parkSpec(t *testing.T) wfdot.Spec {
	t.Helper()
	spec, err := wfdot.ParseRoute(
		`["*"]
obligations = ["raised", "coded", "closed"]
park = { state = "blocked", gate = "satelle-story-blocked-review" }
cancel = { state = "cancelled", gate = "satelle-story-cancel-review" }
`,
		`[raised]
status = "backlog"
start = true

[coded]
status = "in_progress"
agent = "executor"
requires = ["raised"]

[closed]
status = "done"
terminal = true
requires = ["coded"]
`,
		"fix", nil)
	if err != nil {
		t.Fatal(err)
	}
	return spec
}

func trans(from, to string) ledger.Entry {
	return ledger.Entry{Kind: ledger.KindStatusTransition, Body: from + " → " + to}
}

func TestParkOriginStampedColumnWins(t *testing.T) {
	spec := parkSpec(t)
	item := workitem.Item{Status: "blocked", ParkOrigin: "in_progress"}
	got := ParkOrigin(item, spec, []ledger.Entry{trans("backlog", "blocked")})
	if got != "in_progress" {
		t.Errorf("ParkOrigin = %q, want stamped in_progress", got)
	}
}

func TestParkOriginDerivesFromLedgerIntoPark(t *testing.T) {
	spec := parkSpec(t)
	item := workitem.Item{ID: "sty_7bc2de55", Status: "blocked", ParkOrigin: ""}
	got := ParkOrigin(item, spec, []ledger.Entry{
		trans("backlog", "in_progress"),
		trans("in_progress", "blocked"),
	})
	if got != "in_progress" {
		t.Errorf("ParkOrigin = %q, want derived in_progress", got)
	}
}

func TestParkOriginEmptyLedgerDoesNotInvent(t *testing.T) {
	spec := parkSpec(t)
	item := workitem.Item{Status: "blocked", ParkOrigin: ""}
	if got := ParkOrigin(item, spec, nil); got != "" {
		t.Errorf("ParkOrigin = %q, want empty", got)
	}
}

func TestParkOriginIgnoresNonPerformingFrom(t *testing.T) {
	spec := parkSpec(t)
	item := workitem.Item{Status: "blocked", ParkOrigin: ""}
	// backlog is start, not performing — do not resume onto it.
	if got := ParkOrigin(item, spec, []ledger.Entry{trans("backlog", "blocked")}); got != "" {
		t.Errorf("ParkOrigin = %q, want empty for non-performing from", got)
	}
}

func TestTransitionFromBodyAndPayload(t *testing.T) {
	if got := TransitionFrom(ledger.Entry{Body: "in_progress → blocked"}); got != "in_progress" {
		t.Errorf("body From = %q", got)
	}
	if got := TransitionFrom(ledger.Entry{Payload: []byte(`{"from":"plan","to":"blocked"}`)}); got != "plan" {
		t.Errorf("payload From = %q", got)
	}
}
