package agentstep

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/verb"
)

// ledgerMaterial seeds n numbered rows for story, fills the reviewer ledger
// material from them, and returns the material ref written for the gate and the
// rows in the file it points at.
func ledgerMaterial(t *testing.T, story string, n int) (ledgerRef, []ledger.Entry) {
	t.Helper()
	st := openEngineLedger(t)
	verb.SetLedgerStore(st)
	t.Cleanup(func() { verb.SetLedgerStore(nil) })
	base := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	for i := 1; i <= n; i++ {
		if _, err := st.Append(context.Background(), ledger.AppendInput{
			StoryID: story, Kind: ledger.KindComment, Body: fmt.Sprintf("row-%d", i),
		}, base.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	mat := &reviewerMaterial{}
	(&Engine{}).fillLedger(context.Background(), story, mat)
	if mat.LedgerErr != "" {
		t.Fatalf("fillLedger: %s", mat.LedgerErr)
	}
	ref, err := writeReviewerMaterial(t.TempDir(), mat, transitionPayload{})
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(ref.Ledger.Path)
	if err != nil {
		t.Fatal(err)
	}
	var rows []ledger.Entry
	if err := json.Unmarshal(b, &rows); err != nil {
		t.Fatal(err)
	}
	return ref.Ledger, rows
}

// TestFillLedgerRecordsOmission pins sty_523edea7 AC1/AC2 at the gate: a gate
// reads the NEWEST rows of a long ledger, newest last, and the material it is
// handed records how many older rows are missing; a short ledger records none.
func TestFillLedgerRecordsOmission(t *testing.T) {
	t.Run("long ledger", func(t *testing.T) {
		ref, rows := ledgerMaterial(t, "sty_long", 250)
		if len(rows) != ledger.DefaultListLimit || rows[0].Body != "row-51" || rows[len(rows)-1].Body != "row-250" {
			t.Fatalf("file holds %d rows %q..%q, want the newest %d (row-51..row-250)",
				len(rows), rows[0].Body, rows[len(rows)-1].Body, ledger.DefaultListLimit)
		}
		if ref.Omitted != 50 || ref.Total != 250 || ref.Limit != ledger.DefaultListLimit {
			t.Fatalf("ledger ref = %+v, want omitted 50 of total 250 at limit %d", ref, ledger.DefaultListLimit)
		}
		b, _ := json.Marshal(ref)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		if m["omitted"] != float64(50) || m["total"] != float64(250) || m["limit"] != float64(ledger.DefaultListLimit) {
			t.Fatalf("serialised ref lacks the omission: %s", b)
		}
	})
	t.Run("short ledger", func(t *testing.T) {
		ref, rows := ledgerMaterial(t, "sty_short", 10)
		if len(rows) != 10 {
			t.Fatalf("file holds %d rows, want 10", len(rows))
		}
		b, _ := json.Marshal(ref)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		for _, k := range []string{"omitted", "total", "limit"} {
			if _, ok := m[k]; ok {
				t.Errorf("nothing omitted, but the ledger ref carries %q: %s", k, b)
			}
		}
	})
}
