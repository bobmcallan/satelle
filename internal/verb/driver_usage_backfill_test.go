package verb

// Internal tests for story-driver-backfill (sty_8c0e7e8c): a closed story's driver
// spend attributed from a pi session record by its engage..close window, against the
// captured multi-story fixture agentcli/testdata/driver/pi_session_multistory.jsonl.

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/costview"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/workitem"
)

const (
	bfSession      = "01a0f01e-fb15-7095-8798-9b7ab9d3d092"
	bfRepo         = "/home/example/pi-repo"
	bfNoReader     = "pi: no driver-usage reader for this harness"
	bfUnpriced     = "pi: usage.cost.total is zero beside real token counts (model unpriced by pi)"
	bfBackfillNote = "derived from timestamps, not a live delta"
)

// bfAt is a wall-clock time on the fixture's day.
func bfAt(hhmm string) time.Time {
	ts, err := time.Parse(time.RFC3339, "2026-09-30T"+hhmm+":00Z")
	if err != nil {
		panic(err)
	}
	return ts
}

// wireBackfill wires the stores with a workflow that has an engaging, an executor and a
// terminal state, so a story's engage..close window resolves from its transitions.
func wireBackfill(t *testing.T) *store.DB {
	t.Helper()
	return wireDUWithWorkflow(t,
		"[\"*\"]\nobligations = [\"raised\", \"planned\", \"closed\"]\n",
		"[raised]\nstatus = \"backlog\"\nstart = true\n\n"+
			"[planned]\nstatus = \"in_progress\"\nagent = \"executor\"\nrequires = [\"raised\"]\n\n"+
			"[closed]\nstatus = \"done\"\nterminal = true\nrequires = [\"planned\"]\n")
}

// useMultistoryRecord installs the multi-story pi capture where pi would have written
// it and points the window reader at it.
func useMultistoryRecord(t *testing.T) {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "agentcli", "testdata", "driver", "pi_session_multistory.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	agentDir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", agentDir)
	dir := filepath.Join(agentDir, "sessions", "--home-example-pi-repo--")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "2026-09-30T09-59-00-000Z_"+bfSession+".jsonl"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	useBackfillRepo(t)
}

// useBackfillRepo points the window reader at bfRepo, whether or not a record exists.
func useBackfillRepo(t *testing.T) {
	t.Helper()
	prev := driverWindowReader
	driverWindowReader = func(harness, sessionID, _ string, from, to time.Time) agentcli.DriverWindowUsage {
		return agentcli.SessionWindowUsage(harness, sessionID, bfRepo, from, to)
	}
	t.Cleanup(func() { driverWindowReader = prev })
}

func bfPayloadAppend(t *testing.T, db *store.DB, storyID, kind string, payload any, at time.Time) {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Ledger.Append(context.Background(), ledger.AppendInput{StoryID: storyID, Kind: kind, Actor: "executor", Payload: raw}, at); err != nil {
		t.Fatal(err)
	}
}

// bfStory creates a story driven on pi that engaged at engage and, when close is
// non-zero, closed at close, with the unavailable driver rows a harness that had no
// driver-usage reader wrote on those two transitions.
func bfStory(t *testing.T, db *store.DB, title string, engage, close time.Time) string {
	t.Helper()
	return bfStoryOn(t, db, title, "pi", bfNoReader, engage, close)
}

func bfStoryOn(t *testing.T, db *store.DB, title, executable, reason string, engage, close time.Time) string {
	t.Helper()
	it, err := db.Stories.Create(context.Background(), workitem.CreateInput{Kind: workitem.KindStory, Title: title, Status: "backlog"}, engage.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	transition := func(from, to string, at time.Time) {
		bfPayloadAppend(t, db, it.ID, ledger.KindStatusTransition, map[string]any{"from": from, "to": to}, at)
		bfPayloadAppend(t, db, it.ID, ledger.KindDriverUsage, DriverUsagePayload{
			SessionID: bfSession, Executable: executable, Trigger: DriverTriggerTransition, From: from, To: to,
			UnavailableReason: reason, CostUnavailableReason: reason,
			WindowKey: it.ID + "|" + from + "|" + to,
		}, at)
	}
	transition("backlog", "in_progress", engage)
	if !close.IsZero() {
		transition("in_progress", "done", close)
	}
	return it.ID
}

func bfBackfill(t *testing.T, id string) DriverBackfillReport {
	t.Helper()
	raw, err := Dispatch(context.Background(), "story-driver-backfill", mustMarshal(t, map[string]any{"id": id}))
	if err != nil {
		t.Fatalf("story-driver-backfill: %v", err)
	}
	var rep DriverBackfillReport
	if err := json.Unmarshal(raw, &rep); err != nil {
		t.Fatal(err)
	}
	return rep
}

func mustMarshal(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func backfilledRows(t *testing.T, db *store.DB, storyID string) []DriverUsagePayload {
	t.Helper()
	var out []DriverUsagePayload
	for _, r := range driverUsageRows(t, db, storyID) {
		if r.Backfilled {
			out = append(out, r)
		}
	}
	return out
}

// TestBackfillAttributesTheStoryWindow pins AC1/AC2: a closed story whose live rows
// are the no-reader unavailable gets one row carrying exactly its own window's usage,
// marked backfilled and derived from timestamps — with no cumulative, no wall time and
// no baseline claim, so it can never pass for a live delta.
func TestBackfillAttributesTheStoryWindow(t *testing.T) {
	db := wireBackfill(t)
	useMultistoryRecord(t)
	id := bfStory(t, db, "story A", bfAt("10:00"), bfAt("10:10"))

	rep := bfBackfill(t, id)
	if len(rep.Sessions) != 1 || rep.Sessions[0].Status != BackfillWritten {
		t.Fatalf("report = %+v, want one backfilled session", rep)
	}
	rows := backfilledRows(t, db, id)
	if len(rows) != 1 {
		t.Fatalf("backfilled rows = %+v, want exactly one", rows)
	}
	r := rows[0]
	if !r.Available || r.Trigger != DriverTriggerBackfill || !r.Backfilled || r.Executable != agentcli.HarnessPi || r.SessionID != bfSession {
		t.Fatalf("row = %+v, want an available backfilled pi row", r)
	}
	if r.FreshInput != 3500 || r.Output != 350 || r.CacheRead != 17500 || r.CacheWrite != 100 || r.ModelCalls == nil || *r.ModelCalls != 3 {
		t.Fatalf("row tokens = %+v calls=%v, want 3500/350/17500/100 over 3 calls", r, r.ModelCalls)
	}
	if r.WindowFrom != "2026-09-30T10:00:00Z" || r.WindowTo != "2026-09-30T10:10:00Z" {
		t.Fatalf("window = %s..%s", r.WindowFrom, r.WindowTo)
	}
	if r.BaselineFresh || r.WallSeconds != 0 || r.Cumulative != (driverCumulative{}) || r.Turns != 0 || r.BaseTurns != 0 || r.Late || r.Pending {
		t.Fatalf("row = %+v claims a live measurement (baseline/wall/cumulative/turns)", r)
	}
	// AC3: pi reports zero cost beside real tokens — unavailable, named, never $0.
	if r.CostUSD != nil || r.CostUnavailableReason != bfUnpriced {
		t.Fatalf("cost = %v (%q), want nil with the named unpriced-model reason", r.CostUSD, r.CostUnavailableReason)
	}
	if s := rep.Sessions[0]; s.FreshInput != 3500 || s.CostUSD != nil || s.CostUnavailableReason != bfUnpriced {
		t.Fatalf("report session = %+v", s)
	}
}

// TestBackfillPricedModelKeepsPisCost: where pi priced the model, the window's cost is
// pi's own sum — not derived, and not dropped.
func TestBackfillPricedModelKeepsPisCost(t *testing.T) {
	db := wireBackfill(t)
	useMultistoryRecord(t)
	id := bfStory(t, db, "story B", bfAt("10:20"), bfAt("10:30"))
	bfBackfill(t, id)
	rows := backfilledRows(t, db, id)
	if len(rows) != 1 || rows[0].CostUSD == nil || math.Abs(*rows[0].CostUSD-0.0323) > 1e-9 || rows[0].CostUnavailableReason != "" {
		t.Fatalf("rows = %+v, want pi's priced 0.0323", rows)
	}
}

// TestBackfillIsIdempotent: re-running over the same window appends nothing.
func TestBackfillIsIdempotent(t *testing.T) {
	db := wireBackfill(t)
	useMultistoryRecord(t)
	id := bfStory(t, db, "story C", bfAt("10:40"), bfAt("10:50"))
	if rep := bfBackfill(t, id); rep.Sessions[0].Status != BackfillWritten {
		t.Fatalf("first run = %+v", rep)
	}
	before := len(driverUsageRows(t, db, id))
	rep := bfBackfill(t, id)
	if rep.Sessions[0].Status != BackfillUnchanged || len(driverUsageRows(t, db, id)) != before {
		t.Fatalf("second run = %+v rows %d→%d, want unchanged and no new row", rep, before, len(driverUsageRows(t, db, id)))
	}
}

// TestBackfillOverlappingWindowsAreAmbiguousNotSplit pins AC2: two stories driven in
// the same session over intersecting windows are each reported ambiguous — an
// unavailable row naming the other story, with no token field set — never apportioned.
func TestBackfillOverlappingWindowsAreAmbiguousNotSplit(t *testing.T) {
	db := wireBackfill(t)
	useMultistoryRecord(t)
	d := bfStory(t, db, "story D", bfAt("11:00"), bfAt("11:20"))
	e := bfStory(t, db, "story E", bfAt("11:10"), bfAt("11:30"))
	for _, c := range []struct{ id, other string }{{d, e}, {e, d}} {
		rep := bfBackfill(t, c.id)
		s := rep.Sessions[0]
		if s.Status != BackfillAmbiguous || len(s.Overlaps) != 1 || s.Overlaps[0] != c.other ||
			!strings.Contains(s.Reason, "overlaps "+c.other) || !strings.HasPrefix(s.Reason, "pi:") {
			t.Fatalf("report = %+v, want ambiguous naming %s", s, c.other)
		}
		rows := backfilledRows(t, db, c.id)
		if len(rows) != 1 {
			t.Fatalf("rows = %+v", rows)
		}
		r := rows[0]
		if r.Available || r.FreshInput != 0 || r.Output != 0 || r.CacheRead != 0 || r.CacheWrite != 0 || r.ModelCalls != nil || r.CostUSD != nil ||
			!strings.Contains(r.UnavailableReason, "usage not split") {
			t.Fatalf("ambiguous row = %+v, want unavailable with no token fields", r)
		}
	}
	// Re-running an ambiguous window repeats nothing.
	before := len(driverUsageRows(t, db, d))
	if rep := bfBackfill(t, d); rep.Sessions[0].Status != BackfillUnchanged || len(driverUsageRows(t, db, d)) != before {
		t.Fatalf("re-run = %+v", rep)
	}
}

// TestBackfillSessionRecordGoneIsNamedUnavailable pins AC4: with no session file the
// row says so by name — never a zero — and a re-run does not repeat it.
func TestBackfillSessionRecordGoneIsNamedUnavailable(t *testing.T) {
	db := wireBackfill(t)
	t.Setenv("PI_CODING_AGENT_DIR", t.TempDir())
	useBackfillRepo(t)
	id := bfStory(t, db, "story A", bfAt("10:00"), bfAt("10:10"))
	rep := bfBackfill(t, id)
	s := rep.Sessions[0]
	if s.Status != BackfillUnavailable || !strings.HasPrefix(s.Reason, "pi: session record for "+bfSession+" not found under ") {
		t.Fatalf("report = %+v, want a named pi unavailable", s)
	}
	rows := backfilledRows(t, db, id)
	if len(rows) != 1 || rows[0].Available || rows[0].FreshInput != 0 || rows[0].UnavailableReason != s.Reason {
		t.Fatalf("rows = %+v, want one unavailable backfill row carrying the reason", rows)
	}
	if rep := bfBackfill(t, id); rep.Sessions[0].Status != BackfillUnchanged || len(backfilledRows(t, db, id)) != 1 {
		t.Fatalf("re-run = %+v", rep)
	}
}

// TestBackfillEmptyWindowIsNamedUnavailable: a window the session record has nothing in
// is unavailable by name, not a zero-token row.
func TestBackfillEmptyWindowIsNamedUnavailable(t *testing.T) {
	db := wireBackfill(t)
	useMultistoryRecord(t)
	id := bfStory(t, db, "story F", bfAt("12:00"), bfAt("12:10"))
	s := bfBackfill(t, id).Sessions[0]
	if s.Status != BackfillUnavailable || !strings.HasPrefix(s.Reason, "pi: session record has no assistant usage between") {
		t.Fatalf("report = %+v", s)
	}
	if rows := backfilledRows(t, db, id); len(rows) != 1 || rows[0].Available {
		t.Fatalf("rows = %+v", rows)
	}
}

// TestBackfillOpenStoryWritesNothing: a story that has not closed has no window yet, and
// says so by name without appending a row.
func TestBackfillOpenStoryWritesNothing(t *testing.T) {
	db := wireBackfill(t)
	useMultistoryRecord(t)
	id := bfStory(t, db, "story open", bfAt("10:00"), time.Time{})
	before := len(driverUsageRows(t, db, id))
	s := bfBackfill(t, id).Sessions[0]
	if s.Status != BackfillUnavailable || s.Reason != "story not closed; window open" {
		t.Fatalf("report = %+v", s)
	}
	if len(driverUsageRows(t, db, id)) != before {
		t.Fatal("an open story's window is not final: no row may be written")
	}
}

// TestBackfillOtherHarnessIsRefusedByName: the verb carries no adapter knowledge — a
// harness with no windowed reader is an adapter-named unavailable.
func TestBackfillOtherHarnessIsRefusedByName(t *testing.T) {
	db := wireBackfill(t)
	useMultistoryRecord(t)
	id := bfStoryOn(t, db, "story X", "mystery", "mystery: "+"no driver-usage reader for this harness", bfAt("10:00"), bfAt("10:10"))
	s := bfBackfill(t, id).Sessions[0]
	if s.Status != BackfillUnavailable || !strings.HasPrefix(s.Reason, "mystery:") || !strings.Contains(s.Reason, "backfill not supported") {
		t.Fatalf("report = %+v", s)
	}
}

// TestBackfillOnlyForMissingReaderRows: a story the harness measured live, or whose rows
// failed for another reason, is not a backfill candidate.
func TestBackfillOnlyForMissingReaderRows(t *testing.T) {
	db := wireBackfill(t)
	useMultistoryRecord(t)
	live := bfStoryOn(t, db, "live", "pi", bfNoReader, bfAt("10:00"), bfAt("10:10"))
	bfPayloadAppend(t, db, live, ledger.KindDriverUsage, DriverUsagePayload{
		SessionID: bfSession, Executable: "pi", Available: true, FreshInput: 9, Trigger: DriverTriggerClose, WindowKey: live + "|close",
	}, bfAt("10:10"))
	other := bfStoryOn(t, db, "other", "pi", "pi: session record unreadable: boom", bfAt("10:20"), bfAt("10:30"))
	for _, id := range []string{live, other} {
		rep := bfBackfill(t, id)
		if len(rep.Sessions) != 0 || rep.Note == "" || len(backfilledRows(t, db, id)) != 0 {
			t.Fatalf("%s: report = %+v, want nothing to backfill", id, rep)
		}
	}
}

// TestBackfillRowIsInvisibleToTheLiveBaseline: a backfill row is history — it must not
// become the session's latest row, claim a turn range, or shift a running session's
// baseline.
func TestBackfillRowIsInvisibleToTheLiveBaseline(t *testing.T) {
	db := wireBackfill(t)
	useMultistoryRecord(t)
	id := bfStory(t, db, "story A", bfAt("10:00"), bfAt("10:10"))
	live := DriverUsagePayload{
		SessionID: bfSession, Executable: "pi", Available: true, Trigger: DriverTriggerClose, WindowKey: "live|close",
		Turns: 7, BaseTurns: 4, Cumulative: driverCumulative{FreshInput: 777},
	}
	bfPayloadAppend(t, db, id, ledger.KindDriverUsage, live, bfAt("10:11"))
	bfBackfill(t, id) // appended later than the live row, at time.Now()

	latest, found, latestAvail, availFound := sessionDriverUsageState(context.Background(), bfSession)
	if !found || latest.payload.WindowKey != "live|close" || !availFound || latestAvail.payload.WindowKey != "live|close" {
		t.Fatalf("latest = %+v / %+v: the backfill row displaced the live high-water mark", latest.payload, latestAvail.payload)
	}
	claimed := sessionClaimedTurns(context.Background(), bfSession)
	if len(claimed) != 3 || !claimed[4] || !claimed[5] || !claimed[6] {
		t.Fatalf("claimed turns = %v, want only the live row's [4,7)", claimed)
	}
}

// TestReconciliationCountsBackfilledStoryInSessionTotal pins the closed decision: a
// story whose pre-backfill rows were unavailable no longer reads unavailable once a
// backfill row recovered it, its tokens count toward what is attributed, and the
// session total stays the harness's own latest cumulative.
func TestReconciliationCountsBackfilledStoryInSessionTotal(t *testing.T) {
	db := wireBackfill(t)
	useMultistoryRecord(t)
	a := bfStory(t, db, "story A", bfAt("10:00"), bfAt("10:10"))
	// A later story on the same session, read live: its row is the session's latest
	// cumulative, well before the backfill row is written at time.Now().
	later := bfStoryOn(t, db, "later", "pi", bfNoReader, bfAt("10:20"), time.Time{})
	bfPayloadAppend(t, db, later, ledger.KindDriverUsage, DriverUsagePayload{
		SessionID: bfSession, Executable: "pi", Available: true, Trigger: DriverTriggerClose, WindowKey: later + "|live",
		Cumulative: driverCumulative{FreshInput: 20000, Output: 2000, CacheRead: 30000, CacheWrite: 100},
	}, bfAt("10:30"))

	before, err := ComputeSessionReconciliation(context.Background(), bfSession)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range before.Stories {
		if s.StoryID == a && s.Available {
			t.Fatalf("story A already available before backfill: %+v", s)
		}
	}
	bfBackfill(t, a)

	recon, err := ComputeSessionReconciliation(context.Background(), bfSession)
	if err != nil {
		t.Fatal(err)
	}
	var got *SessionReconciliationStory
	for i := range recon.Stories {
		if recon.Stories[i].StoryID == a {
			got = &recon.Stories[i]
		}
	}
	if got == nil || !got.Available || got.FreshInput != 3500 || got.Output != 350 || got.CacheRead != 17500 || got.CacheWrite != 100 || !got.Backfilled {
		t.Fatalf("story A = %+v, want available backfilled 3500/350/17500/100", got)
	}
	if !recon.TotalAvailable || recon.Total.FreshInput != 20000 || recon.Total.CacheRead != 30000 {
		t.Fatalf("total = %+v available=%v: the backfill row must not become the session's latest read", recon.Total, recon.TotalAvailable)
	}
}

// TestReconciliationAmbiguousBackfillKeepsStoryUnavailable: a backfill that could not
// attribute anything does not erase the gap it was meant to close.
func TestReconciliationAmbiguousBackfillKeepsStoryUnavailable(t *testing.T) {
	db := wireBackfill(t)
	useMultistoryRecord(t)
	d := bfStory(t, db, "story D", bfAt("11:00"), bfAt("11:20"))
	bfStory(t, db, "story E", bfAt("11:10"), bfAt("11:30"))
	bfBackfill(t, d)
	recon, err := ComputeSessionReconciliation(context.Background(), bfSession)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range recon.Stories {
		if s.StoryID == d && (s.Available || s.UnavailableReason == "") {
			t.Fatalf("story D = %+v, want it still unavailable with a reason", s)
		}
	}
}

// TestStoryCostLabelsBackfilledFiguresDerived: the cost view counts the recovered
// tokens, drops the superseded unavailable rows from the gap count, and says — on the
// coverage line, the row's trigger and the adapter line — that the figure is derived.
func TestStoryCostLabelsBackfilledFiguresDerived(t *testing.T) {
	db := wireBackfill(t)
	useMultistoryRecord(t)
	id := bfStory(t, db, "story A", bfAt("10:00"), bfAt("10:10"))
	bfBackfill(t, id)

	sc, err := ComputeStoryCost(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	f := sc.Figures
	if f.FreshInput != 3500 || f.Output != 350 || f.CacheRead != 17500 {
		t.Fatalf("figures fresh/out/read = %d/%d/%d, want the recovered 3500/350/17500", f.FreshInput, f.Output, f.CacheRead)
	}
	if f.DriverStatus != costview.DriverDerived {
		t.Fatalf("driver status = %q, want derived (the engage/close rows are superseded, the backfill row is not live)", f.DriverStatus)
	}
	if line := costview.FormatDriverCoverage(f); !strings.Contains(line, "1 backfilled ("+bfBackfillNote+")") || strings.Contains(line, "no driver-usage reader") {
		t.Fatalf("coverage line = %q", line)
	}
	if lines := costview.FormatAdapterCoverage(f); len(lines) != 1 || !strings.Contains(lines[0], bfBackfillNote) {
		t.Fatalf("adapter coverage = %q", lines)
	}
	rows, _ := costview.FormatDriverRows(driverRowsAsCostview(sc.DriverRows))
	var triggers []string
	for _, r := range rows {
		triggers = append(triggers, r.Trigger)
	}
	want := []string{"transition (superseded by backfill)", "transition (superseded by backfill)", "backfill (" + bfBackfillNote + ")"}
	if strings.Join(triggers, "|") != strings.Join(want, "|") {
		t.Fatalf("triggers = %q, want %q", triggers, want)
	}
}

// driverRowsAsCostview re-encodes payload rows through JSON, the same decode the
// ledger entry takes into costview.DriverRow.
func driverRowsAsCostview(rows []DriverUsagePayload) []costview.DriverRow {
	out := make([]costview.DriverRow, 0, len(rows))
	for _, r := range rows {
		raw, _ := json.Marshal(r)
		var d costview.DriverRow
		_ = json.Unmarshal(raw, &d)
		out = append(out, d)
	}
	return costview.MarkSuperseded(out)
}

// TestBackfillVerbRequiresAnExistingStory: the verb refuses a missing id and an unknown story.
func TestBackfillVerbRequiresAnExistingStory(t *testing.T) {
	wireBackfill(t)
	if _, err := Dispatch(context.Background(), "story-driver-backfill", mustMarshal(t, map[string]any{})); err == nil {
		t.Fatal("a request with no id must be refused")
	}
	if _, err := Dispatch(context.Background(), "story-driver-backfill", mustMarshal(t, map[string]any{"id": "sty_nope"})); err == nil {
		t.Fatal("an unknown story must be refused")
	}
}
