package verb_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// wireWithWorkflows wires stores and indexes the given workflows (name → body)
// so story-set freeze resolution can List them.
func wireWithWorkflows(t *testing.T, workflows map[string]string) {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "satelle.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	wfDir := filepath.Join(dir, "workflows")
	if err := os.MkdirAll(wfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range workflows {
		if err := os.WriteFile(filepath.Join(wfDir, name+".toml"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.DocIndex.Sync(context.Background(), map[string]string{"workflows": wfDir}, time.Now()); err != nil {
		t.Fatalf("sync workflows: %v", err)
	}
	verb.SetWorkItemStore(db.Stories)
	verb.SetLedgerStore(db.Ledger)
	verb.SetDocIndexStore(db.DocIndex)
	verb.SetLeaseStore(db.Leases)
	t.Cleanup(func() {
		db.Close()
		verb.SetWorkItemStore(nil)
		verb.SetLedgerStore(nil)
		verb.SetDocIndexStore(nil)
		verb.SetLeaseStore(nil)
	})
}

// writeRouteFiles lands a routeHalves map in a workflows dir. The route source
// is TOML (sty_81bb0dde), and the extension is load-bearing: a `.md` half is
// REFUSED by name as an unconverted repo, so a fixture written as markdown fails
// every case with a conversion error rather than the behaviour under test.
func writeRouteFiles(t *testing.T, wfDir string, halves map[string]string) {
	t.Helper()
	for name, body := range halves {
		if err := os.WriteFile(filepath.Join(wfDir, name+".toml"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// A lifecycle is a DERIVED ROUTE — done.toml + step.toml (sty_d953c5d8).
// routeHalves names the two docs a fixture must write; a category-specific lane
// is a `[<category>]` table rather than a second workflow file. Frontmatter is
// the `[meta]` table the TOML form uses (sty_81bb0dde).
func routeHalves(done, step string) map[string]string {
	mk := func(name, what, body string) string {
		return "[meta]\nname = \"" + name + "\"\ntype = \"workflow\"\nscope = \"project\"\ndescription = \"" +
			what + "\"\n\n" + body
	}
	return map[string]string{
		"done": mk("done", "fixture declaration of done", done),
		"step": mk("step", "fixture step catalogue", step),
	}
}

var freezeWF = routeHalves(
	`["*"]
obligations = ["raised", "coded", "closed"]

[triage-cat]
obligations = ["triaged", "t-coded", "t-closed"]
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

[triaged]
status = "triage"
start = true

[t-coded]
status = "in_progress"
agent = "executor"
requires = ["triaged"]

[t-closed]
status = "done"
terminal = true
requires = ["t-coded"]
`)

func TestDefinitionFreezeEngagedRefusesTitle(t *testing.T) {
	wireWithWorkflows(t, freezeWF)
	// Create stamps workflow: by category with applies_to * → freeze-wf if we
	// also wire a resolver, or stamp via tags after create.
	// Create without resolver leaves no stamp; GoverningWorkflow falls to
	// OrderedWorkflows on category — freeze-wf is the only * match.
	var created workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{"title": "Freeze me", "category": "feature"}), &created)

	// Engage: leave entry state.
	var engaged workitem.Item
	json.Unmarshal(call(t, "story-set", map[string]any{"id": created.ID, "status": "in_progress"}), &engaged)
	if engaged.Status != "in_progress" {
		t.Fatalf("status = %q", engaged.Status)
	}

	err := dispatchErr(t, "story-set", map[string]any{"id": created.ID, "title": "changed"})
	msg := err.Error()
	if !strings.Contains(msg, "title") || !strings.Contains(msg, "engaged") {
		t.Fatalf("expected freeze error naming title+engaged, got: %v", err)
	}

	// Unaffected fields still work.
	json.Unmarshal(call(t, "story-set", map[string]any{"id": created.ID, "status": "done"}), &engaged)
	if engaged.Status != "done" {
		t.Errorf("status set failed: %q", engaged.Status)
	}
	json.Unmarshal(call(t, "story-set", map[string]any{
		"id": created.ID, "tags": []string{"ok"},
	}), &engaged)
	if len(engaged.Tags) == 0 {
		t.Error("tags set failed")
	}
	json.Unmarshal(call(t, "story-set", map[string]any{"id": created.ID, "priority": "high"}), &engaged)
	if engaged.Priority != "high" {
		t.Errorf("priority = %q", engaged.Priority)
	}
}

func TestDefinitionFreezeEntryStateAllowsEdit(t *testing.T) {
	wireWithWorkflows(t, freezeWF)
	var created workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{"title": "Open", "category": "feature"}), &created)

	var updated workitem.Item
	json.Unmarshal(call(t, "story-set", map[string]any{
		"id":                  created.ID,
		"title":               "New title",
		"body":                "New body",
		"acceptance_criteria": "1. yes",
		"category":            "chore",
	}), &updated)
	if updated.Title != "New title" || updated.Body != "New body" ||
		updated.AcceptanceCriteria != "1. yes" || updated.Category != "chore" {
		t.Fatalf("entry-state edit failed: %+v", updated)
	}
}

func TestDefinitionFreezeNonBacklogEntry(t *testing.T) {
	// The triage-cat SECTION of the same route starts at `triage`, not `backlog`.
	wireWithWorkflows(t, freezeWF)
	// Story stamped onto triage-wf so entry state is "triage", not "backlog".
	var created workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{
		"title":    "Triage path",
		"category": "triage-cat",
		"tags":     []string{"workflow:triage-wf"},
		"status":   "triage",
	}), &created)
	if created.Status != "triage" {
		// create may default status to backlog; force set while still at entry.
		json.Unmarshal(call(t, "story-set", map[string]any{"id": created.ID, "status": "triage"}), &created)
	}
	// Confirm edits allowed at triage (entry).
	json.Unmarshal(call(t, "story-set", map[string]any{"id": created.ID, "title": "still open"}), &created)
	if created.Title != "still open" {
		t.Fatalf("triage entry edit failed: %q", created.Title)
	}
	// Leave entry.
	json.Unmarshal(call(t, "story-set", map[string]any{"id": created.ID, "status": "in_progress"}), &created)
	err := dispatchErr(t, "story-set", map[string]any{"id": created.ID, "body": "nope"})
	if !strings.Contains(err.Error(), "body") || !strings.Contains(err.Error(), "engaged") {
		t.Fatalf("expected body freeze on non-backlog entry workflow, got: %v", err)
	}
	if !strings.Contains(err.Error(), "triage") {
		t.Fatalf("error should name entry state triage, got: %v", err)
	}
}

func TestDefinitionFreezeFailClosedNoWorkflow(t *testing.T) {
	// Wire stores with NO on-disk workflow docs. Virtual sparse defaults
	// (sty_29e5a9a5) still resolve the embedded baseline, so entry (backlog) is
	// resolvable and a title edit at backlog succeeds — fail-closed no longer
	// triggers when the binary ships a default lifecycle.
	wire(t)
	var created workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{"title": "Orphan"}), &created)
	json.Unmarshal(call(t, "story-set", map[string]any{"id": created.ID, "title": "x"}), &created)
	if created.Title != "x" {
		t.Fatalf("virtual baseline should allow backlog edit, title=%q", created.Title)
	}
}

func TestDefinitionFreezeIdenticalResubmitOK(t *testing.T) {
	wireWithWorkflows(t, freezeWF)
	var created workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{"title": "Same", "category": "feature"}), &created)
	json.Unmarshal(call(t, "story-set", map[string]any{"id": created.ID, "status": "in_progress"}), &created)
	// Resubmitting the same title is not a change — must not freeze.
	json.Unmarshal(call(t, "story-set", map[string]any{"id": created.ID, "title": "Same"}), &created)
	if created.Title != "Same" {
		t.Fatalf("identical resubmit failed: %q", created.Title)
	}
}
