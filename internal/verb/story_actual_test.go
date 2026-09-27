package verb_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// wireActualWF wires the stores AND a minimal DOT workflow with an engaging
// state, a park state and a terminal state, so ComputeStoryActual's
// shape-derived clock (storyStatusIsEngaging / targetIsTerminalStateOnly)
// resolves instead of falling back to "no governing workflow" — same fixture
// shape as wireDUWithEngagingWorkflow (driver_usage_test.go), duplicated here
// because that helper is unexported in package verb and this file is the
// external verb_test package (sty_8eae81ac).
func wireActualWF(t *testing.T) *store.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "satelle.db"))
	if err != nil {
		t.Fatal(err)
	}
	wfDir := filepath.Join(dir, "workflows")
	if err := os.MkdirAll(wfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	doneBody := "[meta]\nname = \"done\"\ntype = \"workflow\"\nscope = \"project\"\ndescription = \"fixture\"\n\n" +
		"[\"*\"]\nobligations = [\"raised\", \"planned\", \"parked\", \"closed\"]\n"
	stepBody := "[meta]\nname = \"step\"\ntype = \"workflow\"\nscope = \"project\"\ndescription = \"fixture\"\n\n" +
		"[raised]\nstatus = \"backlog\"\nstart = true\n\n" +
		"[planned]\nstatus = \"in_progress\"\nagent = \"executor\"\nrequires = [\"raised\"]\n\n" +
		"[parked]\nstatus = \"blocked\"\nagent = \"reviewer\"\nrequires = [\"planned\"]\n\n" +
		"[closed]\nstatus = \"done\"\nterminal = true\nrequires = [\"planned\"]\n"
	if err := os.WriteFile(filepath.Join(wfDir, "done.toml"), []byte(doneBody), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wfDir, "step.toml"), []byte(stepBody), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DocIndex.Sync(context.Background(), map[string]string{"workflows": wfDir}, time.Now()); err != nil {
		t.Fatal(err)
	}
	verb.SetWorkItemStore(db.Stories)
	verb.SetLedgerStore(db.Ledger)
	verb.SetTxRunner(db.InTx)
	verb.SetDocIndexStore(db.DocIndex)
	verb.SetLeaseStore(db.Leases)
	t.Cleanup(func() {
		db.Close()
		verb.SetWorkItemStore(nil)
		verb.SetLedgerStore(nil)
		verb.SetTxRunner(nil)
		verb.SetDocIndexStore(nil)
		verb.SetLeaseStore(nil)
	})
	return db
}

func appendInvocation(t *testing.T, db *store.DB, storyID string, payload map[string]any, at time.Time) {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Ledger.Append(context.Background(), ledger.AppendInput{
		StoryID: storyID, Kind: ledger.KindAgentInvocation, Actor: "coder", Payload: raw,
	}, at); err != nil {
		t.Fatal(err)
	}
}

func appendTransition(t *testing.T, db *store.DB, storyID, from, to string, at time.Time) {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"from": from, "to": to})
	if _, err := db.Ledger.Append(context.Background(), ledger.AppendInput{
		StoryID: storyID, Kind: ledger.KindStatusTransition, Actor: "executor", Payload: raw,
	}, at); err != nil {
		t.Fatal(err)
	}
}

// TestComputeStoryActualDollarsSplitAndTime pins AC2: cost_usd sums only rows
// with a known cost, counting the rest as unavailable rather than folding them
// in as zero; cache_write stays its own field, never folded into fresh_input
// or cache_read; elapsed_ms is the story clock (engaging → terminal); agent_ms
// is dispatch + driver, each also kept separately.
func TestComputeStoryActualDollarsSplitAndTime(t *testing.T) {
	db := wireActualWF(t)
	ctx := context.Background()
	it, err := db.Stories.Create(ctx, workitem.CreateInput{
		Kind: workitem.KindStory, Title: "actual", Status: "backlog",
	}, time.Unix(1_700_000_000, 0))
	if err != nil {
		t.Fatal(err)
	}

	t0 := time.Unix(1_700_000_100, 0)
	appendTransition(t, db, it.ID, "backlog", "in_progress", t0) // engage

	c1, c2 := 0.12, 0.30
	appendInvocation(t, db, it.ID, map[string]any{
		"from": "plan", "to": "in_progress", "agent": "coder",
		"usage_available": true, "tokens_in_fresh": 100, "tokens_out": 20,
		"duration_ms": 1000, "cost_usd": c1,
	}, t0.Add(time.Minute))
	appendInvocation(t, db, it.ID, map[string]any{
		"from": "plan", "to": "in_progress", "agent": "coder",
		"usage_available": true, "tokens_in_fresh": 50, "tokens_cache_write": 5, "tokens_cache_read": 3, "tokens_out": 10,
		"duration_ms": 2000, "cost_usd": c2,
	}, t0.Add(2*time.Minute))
	appendInvocation(t, db, it.ID, map[string]any{
		"from": "plan", "to": "in_progress", "agent": "coder",
		"usage_available": true, "tokens_in_fresh": 7, "tokens_out": 1, "duration_ms": 500,
		// no cost_usd — unavailable, must never be folded in as zero.
	}, t0.Add(3*time.Minute))

	if _, err := db.Ledger.Append(ctx, ledger.AppendInput{
		StoryID: it.ID, Kind: ledger.KindDriverUsage, Actor: "executor", Payload: mustJSON(t, map[string]any{
			"session_id": "sess-1", "executable": "claude", "available": true,
			"fresh_input": 40, "output": 8, "cache_read": 2, "cache_write": 1,
			"cost_usd": 1.00, "wall_seconds": 120.0, "trigger": "transition",
		}),
	}, t0.Add(4*time.Minute)); err != nil {
		t.Fatal(err)
	}

	t1 := t0.Add(10 * time.Minute)
	appendTransition(t, db, it.ID, "in_progress", "done", t1) // terminal

	actual, err := verb.ComputeStoryActual(ctx, it.ID)
	if err != nil {
		t.Fatal(err)
	}
	tot := actual.Total
	if got, want := tot.CostUSD, c1+c2+1.00; got != want {
		t.Errorf("CostUSD = %v, want %v", got, want)
	}
	if tot.CostRows != 3 {
		t.Errorf("CostRows = %d, want 3 (two dispatch + one driver)", tot.CostRows)
	}
	if tot.CostUnavailableRows != 1 {
		t.Errorf("CostUnavailableRows = %d, want 1 (the uncosted dispatch row)", tot.CostUnavailableRows)
	}
	if tot.FreshInput != 100+50+7+40 {
		t.Errorf("FreshInput = %d, want %d", tot.FreshInput, 100+50+7+40)
	}
	if tot.Output != 20+10+1+8 {
		t.Errorf("Output = %d, want %d", tot.Output, 20+10+1+8)
	}
	if tot.CacheWrite != 5+1 {
		t.Errorf("CacheWrite = %d, want %d — must never be folded into FreshInput/CacheRead", tot.CacheWrite, 5+1)
	}
	if tot.CacheRead != 3+2 {
		t.Errorf("CacheRead = %d, want %d", tot.CacheRead, 3+2)
	}
	if tot.ElapsedMs != t1.Sub(t0).Milliseconds() {
		t.Errorf("ElapsedMs = %d, want %d (t1-t0, the engage-to-terminal clock)", tot.ElapsedMs, t1.Sub(t0).Milliseconds())
	}
	wantDispatchMs := int64(1000 + 2000 + 500)
	wantDriverMs := int64(120 * 1000)
	if tot.DispatchMs != wantDispatchMs {
		t.Errorf("DispatchMs = %d, want %d", tot.DispatchMs, wantDispatchMs)
	}
	if tot.DriverMs != wantDriverMs {
		t.Errorf("DriverMs = %d, want %d", tot.DriverMs, wantDriverMs)
	}
	if tot.AgentMs != wantDispatchMs+wantDriverMs {
		t.Errorf("AgentMs = %d, want dispatch+driver = %d", tot.AgentMs, wantDispatchMs+wantDriverMs)
	}
}

// TestComputeStoryActualParkDoesNotStopClock pins A1: a park (blocked)
// transition does not end the story clock — only a terminal transition does.
func TestComputeStoryActualParkDoesNotStopClock(t *testing.T) {
	db := wireActualWF(t)
	ctx := context.Background()
	it, err := db.Stories.Create(ctx, workitem.CreateInput{
		Kind: workitem.KindStory, Title: "parked", Status: "backlog",
	}, time.Unix(1_700_000_000, 0))
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Unix(1_700_000_100, 0)
	appendTransition(t, db, it.ID, "backlog", "in_progress", t0)
	appendTransition(t, db, it.ID, "in_progress", "blocked", t0.Add(time.Minute))   // park — must NOT stop the clock
	appendTransition(t, db, it.ID, "blocked", "in_progress", t0.Add(2*time.Minute)) // resume
	t1 := t0.Add(30 * time.Minute)
	appendTransition(t, db, it.ID, "in_progress", "done", t1)

	actual, err := verb.ComputeStoryActual(ctx, it.ID)
	if err != nil {
		t.Fatal(err)
	}
	if actual.Total.ElapsedMs != t1.Sub(t0).Milliseconds() {
		t.Errorf("ElapsedMs = %d, want %d — the park detour must not shorten or split the clock",
			actual.Total.ElapsedMs, t1.Sub(t0).Milliseconds())
	}
}

// TestComputeStoryActualEpicRollup pins AC3: a parent's Total includes every
// child's own (subtree) actual, by parent_id at any depth, each shown
// separately from the parent's own.
func TestComputeStoryActualEpicRollup(t *testing.T) {
	db := wireActualWF(t)
	ctx := context.Background()
	parent, err := db.Stories.Create(ctx, workitem.CreateInput{
		Kind: workitem.KindStory, Title: "epic", Status: "backlog",
	}, time.Unix(1_700_000_000, 0))
	if err != nil {
		t.Fatal(err)
	}
	child1, err := db.Stories.Create(ctx, workitem.CreateInput{
		Kind: workitem.KindStory, Title: "c1", Status: "backlog", ParentID: parent.ID,
	}, time.Unix(1_700_000_010, 0))
	if err != nil {
		t.Fatal(err)
	}
	child2, err := db.Stories.Create(ctx, workitem.CreateInput{
		Kind: workitem.KindStory, Title: "c2", Status: "backlog", ParentID: parent.ID,
	}, time.Unix(1_700_000_020, 0))
	if err != nil {
		t.Fatal(err)
	}

	pc, c1c, c2c := 1.0, 2.0, 3.0
	appendInvocation(t, db, parent.ID, map[string]any{"from": "a", "to": "b", "agent": "x", "usage_available": true, "cost_usd": pc}, time.Now())
	appendInvocation(t, db, child1.ID, map[string]any{"from": "a", "to": "b", "agent": "x", "usage_available": true, "cost_usd": c1c}, time.Now())
	appendInvocation(t, db, child2.ID, map[string]any{"from": "a", "to": "b", "agent": "x", "usage_available": true, "cost_usd": c2c}, time.Now())

	actual, err := verb.ComputeStoryActual(ctx, parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if actual.Own.CostUSD != pc {
		t.Errorf("Own.CostUSD = %v, want %v", actual.Own.CostUSD, pc)
	}
	if len(actual.Children) != 2 {
		t.Fatalf("Children = %d, want 2", len(actual.Children))
	}
	if actual.Total.CostUSD != pc+c1c+c2c {
		t.Errorf("Total.CostUSD = %v, want %v (own + both children)", actual.Total.CostUSD, pc+c1c+c2c)
	}
}

// TestComputeStoryActualEpicRollupExcludesNonStoryChildren pins the AC7 rework
// fix: the family walk (collectDescendants) is pinned to kind=story, the SAME
// kind filter internal/web's mirrorBuildCostVM uses (decodeItems(..., "story")).
// A task child under the same parent must never show up in Children or Total —
// otherwise the CLI and web pages would report different family totals for the
// same epic (sty_b8542a3a AC7 rework).
func TestComputeStoryActualEpicRollupExcludesNonStoryChildren(t *testing.T) {
	db := wireActualWF(t)
	ctx := context.Background()
	parent, err := db.Stories.Create(ctx, workitem.CreateInput{
		Kind: workitem.KindStory, Title: "epic", Status: "backlog",
	}, time.Unix(1_700_000_000, 0))
	if err != nil {
		t.Fatal(err)
	}
	storyChild, err := db.Stories.Create(ctx, workitem.CreateInput{
		Kind: workitem.KindStory, Title: "story child", Status: "backlog", ParentID: parent.ID,
	}, time.Unix(1_700_000_010, 0))
	if err != nil {
		t.Fatal(err)
	}
	taskChild, err := db.Stories.Create(ctx, workitem.CreateInput{
		Kind: workitem.KindTask, Title: "task child", Status: "backlog", ParentID: parent.ID,
	}, time.Unix(1_700_000_020, 0))
	if err != nil {
		t.Fatal(err)
	}

	sc, tc := 2.0, 100.0
	appendInvocation(t, db, storyChild.ID, map[string]any{"from": "a", "to": "b", "agent": "x", "usage_available": true, "cost_usd": sc}, time.Now())
	appendInvocation(t, db, taskChild.ID, map[string]any{"from": "a", "to": "b", "agent": "x", "usage_available": true, "cost_usd": tc}, time.Now())

	actual, err := verb.ComputeStoryActual(ctx, parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(actual.Children) != 1 {
		t.Fatalf("Children = %d, want 1 (the story child only, task excluded)", len(actual.Children))
	}
	if actual.Children[0].ID != storyChild.ID {
		t.Errorf("Children[0].ID = %q, want %q", actual.Children[0].ID, storyChild.ID)
	}
	if actual.Total.CostUSD != sc {
		t.Errorf("Total.CostUSD = %v, want %v (the task child's cost must not roll up)", actual.Total.CostUSD, sc)
	}
}

// TestComputeStoryActualEpicFamilySpan pins revision 2 point 4: an epic's
// Total.ElapsedMs is the family's wall span (earliest engage to latest
// terminal across the parent and every child) — never the sum of each
// item's own clock, which would double-count overlapping windows and, here,
// would even be a SHORTER figure than the true span once a child that runs
// past the parent's own terminal is folded in. Own.ElapsedMs stays the
// parent's own clock throughout.
func TestComputeStoryActualEpicFamilySpan(t *testing.T) {
	db := wireActualWF(t)
	ctx := context.Background()
	parent, err := db.Stories.Create(ctx, workitem.CreateInput{
		Kind: workitem.KindStory, Title: "epic", Status: "backlog",
	}, time.Unix(1_700_000_000, 0))
	if err != nil {
		t.Fatal(err)
	}
	child1, err := db.Stories.Create(ctx, workitem.CreateInput{
		Kind: workitem.KindStory, Title: "c1", Status: "backlog", ParentID: parent.ID,
	}, time.Unix(1_700_000_010, 0))
	if err != nil {
		t.Fatal(err)
	}
	child2, err := db.Stories.Create(ctx, workitem.CreateInput{
		Kind: workitem.KindStory, Title: "c2", Status: "backlog", ParentID: parent.ID,
	}, time.Unix(1_700_000_020, 0))
	if err != nil {
		t.Fatal(err)
	}
	child3, err := db.Stories.Create(ctx, workitem.CreateInput{
		Kind: workitem.KindStory, Title: "c3", Status: "backlog", ParentID: parent.ID,
	}, time.Unix(1_700_000_030, 0))
	if err != nil {
		t.Fatal(err)
	}

	t0 := time.Unix(1_700_000_100, 0)
	// Parent: engaged the whole time, its own clock runs 0-60min.
	appendTransition(t, db, parent.ID, "backlog", "in_progress", t0)
	appendTransition(t, db, parent.ID, "in_progress", "done", t0.Add(60*time.Minute))
	// Child1 and child2 run fully nested inside the parent's own window.
	appendTransition(t, db, child1.ID, "backlog", "in_progress", t0.Add(10*time.Minute))
	appendTransition(t, db, child1.ID, "in_progress", "done", t0.Add(20*time.Minute))
	appendTransition(t, db, child2.ID, "backlog", "in_progress", t0.Add(30*time.Minute))
	appendTransition(t, db, child2.ID, "in_progress", "done", t0.Add(40*time.Minute))
	// Child3 starts before the parent's own terminal but finishes AFTER it —
	// the family span must extend to cover this, even though it is shorter
	// than the naive sum of every item's own clock.
	appendTransition(t, db, child3.ID, "backlog", "in_progress", t0.Add(50*time.Minute))
	appendTransition(t, db, child3.ID, "in_progress", "done", t0.Add(70*time.Minute))

	actual, err := verb.ComputeStoryActual(ctx, parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	wantOwn := (60 * time.Minute).Milliseconds()
	if actual.Own.ElapsedMs != wantOwn {
		t.Errorf("Own.ElapsedMs = %d, want %d (the parent's own clock, unaffected by children)", actual.Own.ElapsedMs, wantOwn)
	}
	wantSpan := (70 * time.Minute).Milliseconds()
	if actual.Total.ElapsedMs != wantSpan {
		t.Errorf("Total.ElapsedMs = %d, want %d (the family's wall span: t0 to child3's terminal)", actual.Total.ElapsedMs, wantSpan)
	}
	wantNaiveSum := (60 + 10 + 10 + 20) * time.Minute
	if actual.Total.ElapsedMs == wantNaiveSum.Milliseconds() {
		t.Errorf("Total.ElapsedMs must not equal the naive sum of each item's own clock (%d)", wantNaiveSum.Milliseconds())
	}
}

// TestComputeStoryActualExcludesToolPermissionRows pins AC4: a legacy
// agent_invocation row carrying the decided_by/decision/tool tool-permission
// shape must never count toward CostUnavailableRows or contribute a cost —
// the mechanism it used to inflate before it got its own ledger kind.
func TestComputeStoryActualExcludesToolPermissionRows(t *testing.T) {
	db := wireActualWF(t)
	ctx := context.Background()
	it, err := db.Stories.Create(ctx, workitem.CreateInput{
		Kind: workitem.KindStory, Title: "perm", Status: "backlog",
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	appendInvocation(t, db, it.ID, map[string]any{
		"tool": "Edit", "kind": "permission", "decision": "allow", "decided_by": "policy",
	}, time.Now())

	actual, err := verb.ComputeStoryActual(ctx, it.ID)
	if err != nil {
		t.Fatal(err)
	}
	if actual.Total.CostUnavailableRows != 0 || actual.Total.CostRows != 0 || actual.Total.DispatchMs != 0 {
		t.Errorf("tool-permission row leaked into the actual: %+v", actual.Total)
	}
}
