package verb_test

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/waiver"
)

// sty_bef97244 AC4: a verdict's waived checks are recorded on its review_accept row.
func TestVerdictRowRecordsTheWaivedChecks(t *testing.T) {
	lg, id := bundleTransition(t, verb.GateDecision{
		Gated: true, Accept: true, Skill: "satelle-story-release-review",
		Reviewers: []verb.ReviewerVerdict{
			{Skill: "satelle-story-release-review", Accept: true, Notes: "ok", Waived: []string{"check_plan_consumed"}, Command: "claude -p"},
		},
	})
	accepts, err := lg.ListByStory(context.Background(), id, ledger.KindReviewAccept)
	if err != nil || len(accepts) != 1 {
		t.Fatalf("accepts = %+v, %v", accepts, err)
	}
	var row struct {
		Waived []string `json:"waived"`
	}
	if err := json.Unmarshal(accepts[0].Payload, &row); err != nil {
		t.Fatal(err)
	}
	if len(row.Waived) != 1 || row.Waived[0] != "check_plan_consumed" {
		t.Errorf("review_accept row waived = %v, want [check_plan_consumed]", row.Waived)
	}
}

// sty_bef97244: `ledger append` is the only verb that appends a caller-chosen
// kind, so it must refuse gate_waiver — otherwise any agent could write the row a
// gate payload carries — while `ledger list --kind gate_waiver` still reads them.
func TestLedgerAppendRefusesGateWaiverAndListStillReadsThem(t *testing.T) {
	db := wire(t)
	ctx := context.Background()

	_, err := dispatchRaw(t, "ledger-append", map[string]any{
		"story_id": "sty_a", "kind": ledger.KindGateWaiver, "actor": "developer",
		"payload": map[string]string{"gate": "g", "check": "c", "reason": "r"},
	})
	if err == nil {
		t.Fatal("ledger-append of gate_waiver must be refused")
	}
	if rows, _ := db.Ledger.ListByStory(ctx, "sty_a", ledger.KindGateWaiver); len(rows) != 0 {
		t.Fatalf("the refused append wrote %d rows", len(rows))
	}
	if got, _ := waiver.List(ctx, db.Ledger, "sty_a"); len(got) != 0 {
		t.Fatalf("the waivers a gate payload would carry = %+v, want none", got)
	}
	// Any other kind is still appendable.
	call(t, "ledger-append", map[string]any{"story_id": "sty_a", "kind": ledger.KindComment, "actor": "executor"})

	saved := os.Environ()
	os.Clearenv()
	t.Cleanup(func() {
		os.Clearenv()
		for _, e := range saved {
			k, v, _ := strings.Cut(e, "=")
			_ = os.Setenv(k, v)
		}
	})
	tty := filepath.Join(t.TempDir(), "tty")
	if err := os.WriteFile(tty, []byte("sty_a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = waiver.Record(ctx, db.Ledger, waiver.Input{StoryID: "sty_a", Gate: "g", Check: "c", Reason: "r"},
		waiver.TTYConfirmer{Path: tty, Out: io.Discard}, time.Now())
	if err != nil {
		t.Fatalf("Record: %v", err)
	}

	var rows []ledger.Entry
	if err := json.Unmarshal(call(t, "ledger-list", map[string]any{"story_id": "sty_a", "kind": ledger.KindGateWaiver}), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Actor != "developer" {
		t.Errorf("ledger-list --kind gate_waiver = %+v, want the one developer row", rows)
	}
}
