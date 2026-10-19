package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/remotedef"
	"github.com/bobmcallan/satelle/internal/workitem"
)

func TestPrintHandbacks(t *testing.T) {
	var out bytes.Buffer
	printHandbacks(&out, nil)
	if out.Len() != 0 {
		t.Errorf("a wake with no failing hand-back prints nothing, got %q", out.String())
	}
	printHandbacks(&out, []remotedef.Handback{{Remote: "edge", Story: "sty_a", Status: "merged", Reason: "not a status of the local default route"}})
	if want := "remote edge: story sty_a left at merged: not a status of the local default route\n"; out.String() != want {
		t.Errorf("printed %q, want %q", out.String(), want)
	}
}

// TestHandbackAdaptersOverTheStore drives the CLI adapters a wake uses: the story
// reader carries the category, the local route resolves the story's own workflow,
// and a hand-back lands as one remote_handback row however often it is recorded.
func TestHandbackAdaptersOverTheStore(t *testing.T) {
	remoteWithTriggers(t)
	ctx := context.Background()
	a, err := app.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()
	it, err := a.Store.Stories.Create(ctx, workitem.CreateInput{
		Kind: workitem.KindStory, Title: "handback", Status: "in_progress", Category: "feature",
	}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}

	r := storyReader{a.Store.Stories}
	ref, err := r.Story(ctx, it.ID)
	if err != nil || ref.Category != "feature" || ref.Status != "in_progress" {
		t.Fatalf("Story = %+v, %v; want status in_progress, category feature", ref, err)
	}
	at, err := r.At(ctx, "in_progress")
	if err != nil || len(at) != 1 || at[0].Category != "feature" {
		t.Fatalf("At = %+v, %v; want the one story with its category", at, err)
	}

	local := localRoute{a.Store.Stories, a.Store.DocIndex}
	for status, want := range map[string]bool{"in_progress": true, "no_such_status": false} {
		got, route, err := local.Declares(ctx, it.ID, status)
		if err != nil || got != want || route == "" {
			t.Errorf("Declares(%s) = %v, %q, %v; want %v and a named route", status, got, route, err, want)
		}
	}

	// Engages is the engagement seat's own classification: working states take a
	// seat; the start state, a terminal state and an unknown one do not.
	for status, want := range map[string]bool{"in_progress": true, "backlog": false, "done": false, "no_such_status": false} {
		got, route, err := local.Engages(ctx, it.ID, status)
		if err != nil || got != want || route == "" {
			t.Errorf("Engages(%s) = %v, %q, %v; want %v and a named route", status, got, route, err, want)
		}
	}

	rec := ledgerRecorder{a.Store.Ledger}
	b := remotedef.Handback{Remote: "edge", FireID: "fire_1", Story: it.ID, Status: "merged", LocalKnown: true, Reason: "outside the contract's exit statuses (done)"}
	for i := 0; i < 2; i++ {
		if err := rec.RecordHandback(ctx, b); err != nil {
			t.Fatal(err)
		}
	}
	rows := ledgerRows(t, ledger.KindRemoteHandback)
	if len(rows) != 1 {
		t.Fatalf("want one remote_handback row for a repeated hand-back, got %d", len(rows))
	}
	var p map[string]any
	if err := json.Unmarshal(rows[0].Payload, &p); err != nil {
		t.Fatal(err)
	}
	if p["status"] != "merged" || p["in_contract"] != false || p["local_known"] != true || !strings.Contains(p["reason"].(string), "exit statuses") {
		t.Errorf("payload = %v", p)
	}
}
