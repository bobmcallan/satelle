package verb

// Internal tests for the live driver-usage row's attribution (sty_6deecf76): a row
// recorded while another story drove the same session in an intersecting window says
// so, by the same function and outcome name backfill uses.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/costview"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/workitem"
)

const grokNote = "grok: subagent use undeterminable; subagent spend inclusion unverified"

// ovSubject creates the story whose live row is recorded: engaged at engage, with only
// its engage transition in the ledger.
func ovSubject(t *testing.T, db *store.DB, engage time.Time) workitem.Item {
	t.Helper()
	it, err := db.Stories.Create(context.Background(), workitem.CreateInput{Kind: workitem.KindStory, Title: "subject", Status: "backlog"}, engage.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	bfPayloadAppend(t, db, it.ID, ledger.KindStatusTransition, map[string]any{"from": "backlog", "to": "in_progress"}, engage)
	it.Status = "in_progress"
	return it
}

// ovRecord records the subject's live row for a claude session shared with the others
// planted by bfStoryOn, and returns it.
func ovRecord(t *testing.T, db *store.DB, subject workitem.Item, snap agentcli.DriverSnapshot, now time.Time) DriverUsagePayload {
	t.Helper()
	t.Setenv(config.SessionEnv, bfSession)
	t.Setenv("CLAUDECODE", "1")
	stubSnapshotter(t, snap)
	recordDriverUsage(context.Background(), subject, "plan", "in_progress", now)
	rows := driverUsageRows(t, db, subject.ID)
	if len(rows) != 1 || !rows[0].Available {
		t.Fatalf("rows = %+v, want one available row", rows)
	}
	return rows[0]
}

func claudeSnap() agentcli.DriverSnapshot {
	return agentcli.DriverSnapshot{Available: true, Model: "claude-opus-5", FreshInputTokens: 100, OutputTokens: 10}
}

// TestLiveRowNamesTheStoriesSharingItsWindow pins AC3 for the live row: a parent session
// keeping a container engaged while a subagent drives a child, and two sibling subagents
// driving concurrently, each record overlaps and the adapter-named reason; a story with
// no other story in its window stays exact.
func TestLiveRowNamesTheStoriesSharingItsWindow(t *testing.T) {
	t.Run("parent plus one subagent", func(t *testing.T) {
		db := wireBackfill(t)
		// The parent's container: engaged at 09:00, still open, its orchestration rows
		// recorded under the same session id.
		container := bfStoryOn(t, db, "container", "claude", "claude: unreadable", bfAt("09:00"), time.Time{})
		child := ovSubject(t, db, bfAt("10:00"))
		row := ovRecord(t, db, child, claudeSnap(), bfAt("10:30"))
		want := "claude: window overlaps " + container + " in session " + bfSession + "; usage not split"
		if len(row.Overlaps) != 1 || row.Overlaps[0] != container || row.AttributionReason != want {
			t.Fatalf("row overlaps %v reason %q, want [%s] %q", row.Overlaps, row.AttributionReason, container, want)
		}
		if row.AttributionReason != driverOverlapReason("claude", []string{container}, bfSession) {
			t.Fatal("the live reason is not the function backfill uses")
		}
		if row.Cumulative.FreshInput != 100 {
			t.Fatalf("the row must keep its figures as the delta baseline: %+v", row.Cumulative)
		}
	})

	t.Run("two sibling subagents", func(t *testing.T) {
		db := wireBackfill(t)
		a := bfStoryOn(t, db, "sibling A", "claude", "claude: unreadable", bfAt("10:00"), time.Time{})
		b := bfStoryOn(t, db, "sibling B", "claude", "claude: unreadable", bfAt("10:05"), time.Time{})
		subject := ovSubject(t, db, bfAt("10:10"))
		row := ovRecord(t, db, subject, claudeSnap(), bfAt("10:30"))
		if len(row.Overlaps) != 2 || row.Overlaps[0] != min(a, b) || row.Overlaps[1] != max(a, b) {
			t.Fatalf("overlaps = %v, want both siblings %s %s sorted", row.Overlaps, a, b)
		}
		if !strings.Contains(row.AttributionReason, a) || !strings.Contains(row.AttributionReason, b) || !strings.HasSuffix(row.AttributionReason, "usage not split") {
			t.Fatalf("reason = %q", row.AttributionReason)
		}
	})

	t.Run("no overlap is exact", func(t *testing.T) {
		db := wireBackfill(t)
		// A story that closed before the subject engaged shares nothing with it.
		bfStoryOn(t, db, "earlier", "claude", "claude: unreadable", bfAt("08:00"), bfAt("09:00"))
		subject := ovSubject(t, db, bfAt("10:00"))
		row := ovRecord(t, db, subject, claudeSnap(), bfAt("10:30"))
		if len(row.Overlaps) != 0 || row.AttributionReason != "" {
			t.Fatalf("row overlaps %v reason %q, want an exact, unqualified row", row.Overlaps, row.AttributionReason)
		}
	})
}

// TestLiveRowCarriesTheAdapterQualifier: a snapshot's subagent qualifier is recorded on
// the row — alone it leaves the row exact (nothing shows another story shared the
// window), joined to the overlap sentence it follows it.
func TestLiveRowCarriesTheAdapterQualifier(t *testing.T) {
	t.Run("alone", func(t *testing.T) {
		db := wireBackfill(t)
		subject := ovSubject(t, db, bfAt("10:00"))
		snap := claudeSnap()
		snap.Executable, snap.SubagentNote = "grok", grokNote
		row := ovRecord(t, db, subject, snap, bfAt("10:30"))
		if len(row.Overlaps) != 0 || row.AttributionReason != grokNote {
			t.Fatalf("row overlaps %v reason %q, want the qualifier alone", row.Overlaps, row.AttributionReason)
		}
		if (costview.DriverRow{Available: true, Overlaps: row.Overlaps}).Ambiguous() {
			t.Fatal("a qualifier alone must not make a row ambiguous")
		}
	})

	t.Run("with an overlap", func(t *testing.T) {
		db := wireBackfill(t)
		other := bfStoryOn(t, db, "sibling", "claude", "claude: unreadable", bfAt("09:55"), time.Time{})
		subject := ovSubject(t, db, bfAt("10:00"))
		snap := claudeSnap()
		snap.SubagentNote = grokNote
		row := ovRecord(t, db, subject, snap, bfAt("10:30"))
		want := driverOverlapReason("claude", []string{other}, bfSession) + "; " + grokNote
		if len(row.Overlaps) != 1 || row.AttributionReason != want {
			t.Fatalf("row overlaps %v reason %q, want %q", row.Overlaps, row.AttributionReason, want)
		}
	})
}

// TestBackfillAndLiveShareTheOutcomeName: one constant names the outcome, and the live
// row reads as ambiguous to costview exactly when it carries overlaps.
func TestBackfillAndLiveShareTheOutcomeName(t *testing.T) {
	if BackfillAmbiguous != DriverAttributionAmbiguous || DriverAttributionAmbiguous != "ambiguous" {
		t.Fatalf("outcome names differ: backfill %q live %q", BackfillAmbiguous, DriverAttributionAmbiguous)
	}
	db := wireBackfill(t)
	bfStoryOn(t, db, "sibling", "claude", "claude: unreadable", bfAt("09:55"), time.Time{})
	subject := ovSubject(t, db, bfAt("10:00"))
	ovRecord(t, db, subject, claudeSnap(), bfAt("10:30"))
	// The engage row only sets the baseline (costview keeps that named gap); the
	// next reading is a measured delta, and it is the one that reads as ambiguous.
	grown := claudeSnap()
	grown.FreshInputTokens, grown.OutputTokens = 250, 30
	stubSnapshotter(t, grown)
	recordDriverUsage(context.Background(), subject, "in_progress", "integration", bfAt("10:45"))
	entries, err := db.Ledger.ListByStory(context.Background(), subject.ID, ledger.KindDriverUsage)
	if err != nil || len(entries) != 2 {
		t.Fatalf("entries = %d err %v", len(entries), err)
	}
	first, _ := costview.DecodeDriverRow(entries[0])
	if first.Ambiguous() || !first.BaselineFresh {
		t.Fatalf("engage row = %+v, want the named baseline gap, not ambiguous", first)
	}
	row := driverUsageRows(t, db, subject.ID)[1]
	dr, ok := costview.DecodeDriverRow(entries[1])
	if !ok || !dr.Ambiguous() || dr.AttributionReason != row.AttributionReason || row.AttributionReason == "" {
		t.Fatalf("costview row = %+v ok %v, want ambiguous with the recorded reason", dr, ok)
	}
	if got := costview.Own(subject, entries, costview.Clock{}, bfAt("11:00")).Figures; got.UsageRows != 0 || len(got.Driver) != 1 || got.Driver[0].AmbiguousRows != 1 {
		t.Fatalf("figures = %+v, want the ambiguous row outside the exact sums", got)
	}
}
