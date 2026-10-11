package verb

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// A re-anchor or full row taken while the story sat in backlog, before its first
// engagement baseline, is not an anchor: the substrate mtime window and the SHA
// anchor start at the baseline. Rows at or after the baseline still anchor
// (sty_12ce4271 AC2).
func TestChangeRecordAnchorIgnoresRowsBeforeEngagementBaseline(t *testing.T) {
	wireCR(t)
	ctx := context.Background()
	ws, err := requireWorkItem()
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Now().Add(-3 * time.Hour)
	it, err := ws.Create(ctx, workitem.CreateInput{
		Kind: workitem.KindStory, Title: "pre-engagement anchor", Category: "substrate",
	}, t0)
	if err != nil {
		t.Fatal(err)
	}

	reanchorAt := t0.Add(time.Minute)
	baseAt := t0.Add(time.Hour)
	rp, _ := json.Marshal(changeRecordPayload{From: "blocked", To: "backlog", Files: []string{}, ReanchorResume: true, HeadSHA: "aaaa", SinceSHA: "aaaa"})
	appendLedgerEntry(ctx, it.ID, ledger.KindChangeRecord, "executor", "reanchor", rp, reanchorAt)

	// Never engaged: the old row is the only anchor there is.
	if got, ok := changeRecordSinceTime(ctx, it.ID); !ok || !got.Equal(reanchorAt) {
		t.Fatalf("unengaged since time = %v,%v want %v", got, ok, reanchorAt)
	}

	bp, _ := json.Marshal(engagementBaselinePayload{HeadSHA: "bbbb", To: "plan"})
	appendLedgerEntry(ctx, it.ID, ledger.KindEngagementBaseline, "executor", "baseline", bp, baseAt)

	if got, ok := changeRecordSinceTime(ctx, it.ID); !ok || !got.Equal(baseAt) {
		t.Errorf("since time = %v,%v want the baseline time %v, not the earlier re-anchor", got, ok, baseAt)
	}
	if since, head, _ := changeRecordAnchor(ctx, it.ID); since != "bbbb" || head != "bbbb" {
		t.Errorf("anchor = %q,%q want the baseline head bbbb", since, head)
	}
	if sha, _, ok := latestResumeReanchor(ctx, it.ID); ok {
		t.Errorf("a pre-engagement re-anchor %q must not be reported", sha)
	}

	// A row at the baseline's own instant (the engaging edge) anchors.
	fp, _ := json.Marshal(changeRecordPayload{From: "backlog", To: "plan", Files: []string{}, HeadSHA: "cccc", SinceSHA: "bbbb"})
	appendLedgerEntry(ctx, it.ID, ledger.KindChangeRecord, "executor", "engaging", fp, baseAt)
	if since, _, _ := changeRecordAnchor(ctx, it.ID); since != "cccc" {
		t.Errorf("anchor = %q want the engaging row head cccc", since)
	}

	// So does a later re-anchor.
	laterAt := baseAt.Add(time.Hour)
	lp, _ := json.Marshal(changeRecordPayload{From: "blocked", To: "plan", Files: []string{}, ReanchorResume: true, HeadSHA: "dddd", SinceSHA: "dddd"})
	appendLedgerEntry(ctx, it.ID, ledger.KindChangeRecord, "executor", "reanchor", lp, laterAt)
	if sha, at, ok := latestResumeReanchor(ctx, it.ID); !ok || sha != "dddd" || !at.Equal(laterAt) {
		t.Errorf("post-engagement re-anchor = %q,%v,%v want dddd at %v", sha, at, ok, laterAt)
	}
	if got, ok := changeRecordSinceTime(ctx, it.ID); !ok || !got.Equal(laterAt) {
		t.Errorf("since time = %v,%v want the post-engagement row %v", got, ok, laterAt)
	}
}
