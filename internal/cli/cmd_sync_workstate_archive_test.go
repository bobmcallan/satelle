package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// sty_28665836 AC5: a story's archived flag is carried by its chain. Archived on
// machine A and pushed, it arrives archived on machine B after the pull's fold
// rebuild; a later unarchive arrives as not archived.
func TestArchivedFlagTravelsWithTheChainBetweenMachines(t *testing.T) {
	withVerbWiring(t)
	ts, _ := newFakeWorkstateServer(t)
	seedCred(t, ts.URL)
	t.Setenv("SATELLE_HOME", t.TempDir())
	t.Setenv("SATELLE_SERVER_ENDPOINT", "none")

	a := newMachine(t, false)
	b := newMachine(t, false)

	wfDir := filepath.Join(t.TempDir(), "workflows")
	if err := os.MkdirAll(wfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range twoMachineRoute {
		if err := os.WriteFile(filepath.Join(wfDir, name+".toml"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.db.DocIndex.Sync(context.Background(), map[string]string{"workflows": wfDir}, time.Now()); err != nil {
		t.Fatalf("sync workflows: %v", err)
	}
	verb.SetWorkItemStore(a.db.Stories)
	verb.SetLedgerStore(a.db.Ledger)
	verb.SetTxRunner(a.db.InTx)
	verb.SetDocIndexStore(a.db.DocIndex)
	verb.SetLeaseStore(a.db.Leases)
	verb.SetStoryDir(filepath.Join(a.repo, "stories"))

	dispatch := func(name string, req map[string]any) json.RawMessage {
		t.Helper()
		raw, _ := json.Marshal(req)
		resp, err := verb.Dispatch(context.Background(), name, raw)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return resp
	}
	var it workitem.Item
	if err := json.Unmarshal(dispatch("story-create", map[string]any{
		"title": "archived across machines", "body": "goal", "acceptance_criteria": "1. ok",
		"category": "feature", "tags": []string{"workflow:cr-wf"},
	}), &it); err != nil {
		t.Fatal(err)
	}

	archivedOn := func(m *machine) bool {
		t.Helper()
		got, err := m.db.Stories.Get(context.Background(), it.ID)
		if err != nil {
			t.Fatalf("story on machine: %v", err)
		}
		return got.Archived
	}

	dispatch("story-archive", map[string]any{"ids": []string{it.ID}, "reason": "not a priority"})
	a.push(t, ts.URL)
	b.pull(t, ts.URL)
	if !archivedOn(b) {
		t.Fatal("the story is not archived on machine B after the pull")
	}
	if d, err := verb.DetectDrift(context.Background(), b.db.Stories, b.db.Ledger); err != nil || len(d) != 0 {
		t.Errorf("drift on B after the pull: %+v %v", d, err)
	}

	time.Sleep(10 * time.Millisecond) // time-subject: the unarchive must be stamped after the archive both machines hold
	dispatch("story-unarchive", map[string]any{"ids": []string{it.ID}})
	a.push(t, ts.URL)
	b.pull(t, ts.URL)
	if archivedOn(b) {
		t.Error("the story is still archived on machine B after the unarchive arrived")
	}
}

// sty_28665836 AC5: the pull's fold rebuild takes archived from the pulled chain,
// not from the hosted row beside it: a hosted row that says not archived over a
// chain that archived the story arrives archived, and one that says archived over a
// chain that unarchived it arrives not archived.
func TestPullRebuildsArchivedFromThePulledChain(t *testing.T) {
	ts, f := newFakeWorkstateServer(t)
	seedCred(t, ts.URL)
	workstateRepo(t, holdOffToml)
	at := time.Now().Add(time.Hour).UTC()
	archiveEvent := func(old, new string) map[string]any {
		return map[string]any{"fields": []map[string]string{{"field": "archived", "old": old, "new": new}}}
	}
	seedHostedFull(f, "sty_arch01", "Chain archived", "B", "1. x", "backlog", []string{"x"}, at)
	seedHostedLedger(f, "led_a_c", "sty_arch01", ledger.KindStoryCreated, map[string]any{"source": "legacy"}, at)
	seedHostedLedger(f, "led_a_u", "sty_arch01", ledger.KindStoryUpdated, archiveEvent("false", "true"), at.Add(time.Second))
	seedHostedFull(f, "sty_arch02", "Chain unarchived", "B", "1. x", "backlog", []string{"x"}, at)
	f.mu.Lock()
	f.itemsByID["probe"]["sty_arch02"].(map[string]any)["archived"] = true
	f.mu.Unlock()
	seedHostedLedger(f, "led_b_c", "sty_arch02", ledger.KindStoryCreated, map[string]any{"source": "legacy"}, at)
	seedHostedLedger(f, "led_b_u1", "sty_arch02", ledger.KindStoryUpdated, archiveEvent("false", "true"), at.Add(time.Second))
	seedHostedLedger(f, "led_b_u2", "sty_arch02", ledger.KindStoryUpdated, archiveEvent("true", "false"), at.Add(2*time.Second))

	if out, err := runRoot(t, "sync", "workstate", "pull", "--server", ts.URL); err != nil {
		t.Fatalf("pull: %v\n%s", err, out)
	}
	db := openScopeDB(t)
	defer func() { _ = db.Close() }()
	for id, want := range map[string]bool{"sty_arch01": true, "sty_arch02": false} {
		row, err := db.Stories.Get(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if row.Archived != want {
			t.Errorf("%s archived = %v after the pull, want %v as its chain folds", id, row.Archived, want)
		}
	}
}
