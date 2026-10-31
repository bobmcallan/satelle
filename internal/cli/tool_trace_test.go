package cli

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/agentstep"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/verb"
)

// sty_23ceeca2: the gate hooks append one tool_trace row per tool call they see
// while a story is engaged, and the in-loop evidence check reads those rows since
// the story entered its step.

func traceRows(t *testing.T) []toolTracePayload {
	t.Helper()
	var out []toolTracePayload
	for _, r := range ledgerRows(t, ledger.KindToolTrace) {
		var p toolTracePayload
		if err := json.Unmarshal(r.Payload, &p); err != nil {
			t.Fatal(err)
		}
		out = append(out, p)
	}
	return out
}

// declareTrace writes the entry row of the story's in_progress step, saying its
// skills declare commands and/or tools as evidence.
func declareTrace(t *testing.T, id string, commands, tools bool) {
	t.Helper()
	db, err := store.Open(runtimeDBPath(t))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	payload, _ := json.Marshal(map[string]any{"from": "raised", "to": "in_progress", "trace_commands": commands, "trace_tools": tools})
	if _, err := db.Ledger.Append(context.Background(), ledger.AppendInput{
		StoryID: id, Kind: ledger.KindSkillDelivery, Actor: "in-loop", Body: "delivered", Payload: payload,
	}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
}

func editOn(t *testing.T, repo string) error {
	t.Helper()
	_, err := runRootIn(t, claudeEditEventAs(filepath.Join(repo, "internal", "foo.go"), thisSession), "hook", "gate")
	return err
}

// A step whose skills declare no evidence costs its edits and commands no ledger
// write: neither hook records a tool_trace row.
func TestStepWithoutEvidenceDeclarationsWritesNoToolTraceRow(t *testing.T) {
	repo, _ := ruleRepo(t, engagedHere, "")
	if _, err := commitgateOn(t, "claude", "go test ./..."); err != nil {
		t.Fatal(err)
	}
	if err := editOn(t, repo); err != nil {
		t.Fatalf("the edit must be allowed: %v", err)
	}
	if rows := ledgerRows(t, ledger.KindToolTrace); len(rows) != 0 {
		t.Errorf("no declared evidence, want no tool_trace row, got %d", len(rows))
	}
}

// A declared command is traced for a shell call; an edit is traced only when a
// tool is declared.
func TestGateRecordsAnEditOnlyWhenAToolIsDeclared(t *testing.T) {
	repo, id := ruleRepo(t, engagedHere, "")
	declareTrace(t, id, true, false)
	if err := editOn(t, repo); err != nil {
		t.Fatal(err)
	}
	if rows := ledgerRows(t, ledger.KindToolTrace); len(rows) != 0 {
		t.Fatalf("only a command is declared, an edit is not traced, got %d rows", len(rows))
	}
	declareTrace(t, id, false, true)
	if err := editOn(t, repo); err != nil {
		t.Fatal(err)
	}
	rows := traceRows(t)
	if len(rows) != 1 || rows[0].Tool != "Edit" {
		t.Errorf("a declared tool must trace the edit, got %+v", rows)
	}
}

func TestCommitgateRecordsAToolTraceRowForTheEngagedStory(t *testing.T) {
	_, id := ruleRepo(t, engagedHere, "")
	declareTrace(t, id, true, false)
	if _, err := commitgateOn(t, "claude", "cd x && go test ./..."); err != nil {
		t.Fatal(err)
	}
	rows := ledgerRows(t, ledger.KindToolTrace)
	if len(rows) != 1 || rows[0].StoryID != id {
		t.Fatalf("rows = %d (story %q), want one for the engaged story %s", len(rows), rows[0].StoryID, id)
	}
	if got := traceRows(t)[0]; got.Harness != "claude" || got.Tool != "Bash" || got.Command != "cd x && go test ./..." {
		t.Errorf("payload = %+v, want claude / Bash / the command", got)
	}
}

func TestToolTraceIsWrittenOnlyWhileAStoryIsEngaged(t *testing.T) {
	ruleRepo(t, engagedNowhere, "")
	if _, err := commitgateOn(t, "claude", "ls"); err != nil {
		t.Fatal(err)
	}
	if rows := ledgerRows(t, ledger.KindToolTrace); len(rows) != 0 {
		t.Errorf("no engaged story, want no trace row, got %d", len(rows))
	}
}

func TestToolTraceIsNotWrittenByADispatchedProcess(t *testing.T) {
	ruleRepo(t, engagedHere, "")
	t.Setenv("SATELLE_DISPATCH_AGENT", "coder")
	t.Setenv("SATELLE_DISPATCH_STEP", "in_progress")
	t.Setenv("SATELLE_DISPATCH_ITEM", "sty_x")
	if _, err := commitgateOn(t, "claude", "ls"); err != nil {
		t.Fatal(err)
	}
	if rows := ledgerRows(t, ledger.KindToolTrace); len(rows) != 0 {
		t.Errorf("a dispatched performer's calls are on its event stream, got %d trace rows", len(rows))
	}
}

// The resolver reads only the rows after the story last entered the step, so an
// earlier step's commands cannot satisfy a later step's evidence.
func TestToolTraceResolverReadsSinceTheStepWasEntered(t *testing.T) {
	_, id := ruleRepo(t, engagedHere, "")
	db, err := store.Open(runtimeDBPath(t))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	ctx := context.Background()
	at := time.Now().UTC().Add(-time.Hour)
	add := func(kind string, payload any, off time.Duration) {
		b, _ := json.Marshal(payload)
		if _, err := db.Ledger.Append(ctx, ledger.AppendInput{StoryID: id, Kind: kind, Actor: "t", Body: "x", Payload: b}, at.Add(off)); err != nil {
			t.Fatal(err)
		}
	}
	add(ledger.KindToolTrace, toolTracePayload{Harness: "claude", Tool: "Bash", Command: "go vet ./..."}, 0)
	add(ledger.KindStatusTransition, map[string]string{"from": "plan", "to": "integration"}, time.Minute)
	add(ledger.KindToolTrace, toolTracePayload{Harness: "claude", Tool: "Bash", Command: "go test ./..."}, 2*time.Minute)
	add(ledger.KindToolTrace, toolTracePayload{Harness: "claude", Tool: "Edit"}, 3*time.Minute)

	tr := toolTraceFrom(func(ctx context.Context, itemID, kind string) []ledger.Entry {
		es, _ := db.Ledger.ListByStory(ctx, itemID, kind)
		return es
	})(ctx, id, "integration")
	want := []agentstep.ToolCall{{Tool: "Bash", Command: "go test ./..."}, {Tool: "Edit"}}
	if tr.Harness != "claude" || len(tr.Calls) != 2 || tr.Calls[0] != want[0] || tr.Calls[1] != want[1] {
		t.Errorf("trace = %+v, want claude with %+v (the step-entry transition excludes the earlier vet)", tr, want)
	}
	if none := toolTraceFrom(func(context.Context, string, string) []ledger.Entry { return nil })(ctx, id, "integration"); none.Harness != "" || len(none.Calls) != 0 {
		t.Errorf("no rows must read as no trace channel, got %+v", none)
	}
}

// A document attached before the story entered the step (an old plan) is not the
// step's evidence; one attached after is. A headless run's recorded stream reads
// back with the coverage it had.
func TestToolTraceResolverScopesDocumentsAndReadsAStreamRow(t *testing.T) {
	_, id := ruleRepo(t, engagedHere, "")
	db, err := store.Open(runtimeDBPath(t))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	ctx := context.Background()
	at := time.Now().UTC().Add(-time.Hour)
	add := func(kind string, payload any, off time.Duration) {
		b, _ := json.Marshal(payload)
		if _, err := db.Ledger.Append(ctx, ledger.AppendInput{StoryID: id, Kind: kind, Actor: "t", Body: "x", Payload: b}, at.Add(off)); err != nil {
			t.Fatal(err)
		}
	}
	add(ledger.KindStoryDocAttached, map[string]string{"name": "plan", "type": "plan"}, 0)
	add(ledger.KindStatusTransition, map[string]string{"from": "plan", "to": "integration"}, time.Minute)
	add(ledger.KindStoryDocAttached, map[string]string{"name": "step-summary", "type": "output"}, 2*time.Minute)
	add(ledger.KindToolTrace, toolTracePayload{
		Harness: "claude", Source: verb.ToolTraceStream, CommandsCovered: true, ToolsCovered: true,
		Calls: []verb.TraceCall{{Tool: "Bash", Command: "go test ./..."}},
	}, 3*time.Minute)

	tr := toolTraceFrom(func(ctx context.Context, itemID, kind string) []ledger.Entry {
		es, _ := db.Ledger.ListByStory(ctx, itemID, kind)
		return es
	})(ctx, id, "integration")
	if !tr.DocsScoped || len(tr.Docs) != 1 || tr.Docs[0] != "step-summary" {
		t.Errorf("docs = %+v scoped=%v, want only step-summary (the plan predates the step)", tr.Docs, tr.DocsScoped)
	}
	if !tr.Stream || !tr.StreamCommands || len(tr.Calls) != 1 || tr.Calls[0].Command != "go test ./..." {
		t.Errorf("stream trace = %+v, want the headless run's recorded command with its coverage", tr)
	}
}
