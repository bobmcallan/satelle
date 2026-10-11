package verb_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/verb"
)

// retroRouteWorkflow gives two categories that share the stage name `done`: the
// epic-parent's closing step declares an advisor, the feature's declares none.
var retroRouteWorkflow = routeHalves(
	`[epic-parent]
obligations = ["raised", "epic-closed"]
cancel = { state = "cancelled" }

[feature]
obligations = ["raised", "closed"]
cancel = { state = "cancelled" }
`,
	`[raised]
status = "backlog"
start = true

[epic-closed]
status = "done"
terminal = true
requires = ["raised"]
advise = { agent = "retro-agent", skill = "route-declared-retro" }

[closed]
status = "done"
terminal = true
requires = ["raised"]
`)

func wireRetroRoute(t *testing.T) {
	t.Helper()
	withWiring(t)
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "satelle.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	verb.SetWorkItemStore(db.Stories)
	verb.SetLedgerStore(db.Ledger)
	verb.SetTxRunner(db.InTx)
	verb.SetDocIndexStore(db.DocIndex)
	verb.SetLeaseStore(db.Leases)
	verb.SetStoryDir(filepath.Join(dir, "stories"))
	t.Cleanup(func() { db.Close() })

	wfDir := t.TempDir()
	for name, half := range retroRouteWorkflow {
		if err := os.WriteFile(filepath.Join(wfDir, name+".toml"), []byte(half), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	call(t, "doc-sync", map[string]any{"dirs": map[string]string{"workflows": wfDir}})
}

func createDone(t *testing.T, category string) string {
	t.Helper()
	var it struct {
		ID string `json:"id"`
	}
	json.Unmarshal(call(t, "story-create", map[string]any{
		"title": "t", "body": "b", "acceptance": "1. x", "category": category, "status": "done",
	}), &it)
	if it.ID == "" {
		t.Fatalf("no %s created", category)
	}
	return it.ID
}

// TestRetrospectRunsRouteDeclaredAdvisor (sty_da018b06 AC1): the skill and agent
// `story retrospect` dispatches are the `advise` declared on the item's route at
// its current step, and a route declaring none leaves the spec empty so the
// dispatcher falls back to its built-in retrospective.
func TestRetrospectRunsRouteDeclaredAdvisor(t *testing.T) {
	wireRetroRoute(t)
	declared := &fakeRetro{res: verb.DispatchResult{Dispatched: true, Agent: "retro-agent", Skill: "route-declared-retro"}}
	verb.SetRetrospector(declared)

	call(t, "story-retrospect", map[string]any{"id": createDone(t, "epic-parent"), "model": "haiku"})
	if declared.gotSpec.Agent != "retro-agent" || declared.gotSpec.Skill != "route-declared-retro" {
		t.Fatalf("spec = %+v, want the route-declared retro-agent @route-declared-retro", declared.gotSpec)
	}
	if declared.gotSpec.Model != "haiku" {
		t.Errorf("--model must still reach the dispatcher: %+v", declared.gotSpec)
	}

	fallback := &fakeRetro{res: verb.DispatchResult{Dispatched: true, Agent: "retrospective"}}
	verb.SetRetrospector(fallback)
	call(t, "story-retrospect", map[string]any{"id": createDone(t, "feature")})
	if fallback.gotModelCalls != 1 || fallback.gotSpec.Agent != "" || fallback.gotSpec.Skill != "" {
		t.Fatalf("a route declaring no advisor must leave the spec empty (fallback), got %+v", fallback.gotSpec)
	}
}

// TestRetrospectRecordsDeclaredSkillAndChangesNoStatus (AC1 + AC4): the ledger
// entry names the dispatched skill; the container stays terminal and the only
// entry the retrospect path adds is the agent_invocation.
func TestRetrospectRecordsDeclaredSkillAndChangesNoStatus(t *testing.T) {
	wireRetroRoute(t)
	verb.SetRetrospector(&fakeRetro{res: verb.DispatchResult{
		Dispatched: true, Agent: "retro-agent", Skill: "route-declared-retro", Model: "m",
	}})
	id := createDone(t, "epic-parent")
	before := len(ledgerEntries(t, id))

	call(t, "story-retrospect", map[string]any{"id": id})

	var it struct {
		Status string `json:"status"`
	}
	json.Unmarshal(call(t, "story-get", map[string]any{"id": id}), &it)
	if it.Status != "done" {
		t.Errorf("status after retrospect = %q, want done (retrospect changes no status)", it.Status)
	}
	after := ledgerEntries(t, id)
	added := after[before:]
	if len(added) != 1 || added[0].Kind != ledger.KindAgentInvocation {
		t.Fatalf("retrospect added ledger entries %+v, want only one agent_invocation", added)
	}
	if !strings.Contains(added[0].Body, "@skill:route-declared-retro") {
		t.Errorf("agent_invocation must name the declared skill: %q", added[0].Body)
	}
}
