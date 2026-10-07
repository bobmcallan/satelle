package cli

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Work-state pull merges by id (sty_78e20d15): a local row the hosted copy has
// never seen is kept beside the hosted rows, and only a same-id row whose local
// side changed (or a ledger row that differs) refuses without --force.

const mergeToml = "[sync]\nstories = \"personal\"\nledger = \"personal\"\n\n[hosted]\nproject = \"probe\"\n"

func seedHostedStory(f *fakeWorkstateServer, id, title, status string, updated time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.itemsByID["probe"] == nil {
		f.itemsByID["probe"] = map[string]any{}
	}
	f.itemsByID["probe"][id] = map[string]any{
		"id": id, "kind": "story", "status": status, "title": title,
		"created_at": updated.Add(-time.Hour).Format(time.RFC3339Nano),
		"updated_at": updated.Format(time.RFC3339Nano),
	}
}

func seedHostedLedger(f *fakeWorkstateServer, id, storyID, kind string, payload any, created time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ledgerByID["probe"] == nil {
		f.ledgerByID["probe"] = map[string]any{}
	}
	f.ledgerByID["probe"][id] = map[string]any{
		"id": id, "story_id": storyID, "kind": kind, "payload": payload,
		"created_at": created.Format(time.RFC3339Nano),
	}
}

// AC1: ids hosted has never seen merge without --force; an older or identical
// same-id local row is replaced by hosted without complaint.
func TestSyncWorkstatePullMergesLocalOnly(t *testing.T) {
	ts, f := newFakeWorkstateServer(t)
	seedCred(t, ts.URL)
	workstateRepo(t, mergeToml)

	localID := createStoryID(t, "Local only story")
	hostedAt := time.Now().Add(time.Hour).UTC()
	seedHostedStory(f, "sty_hosted01", "Hosted story", "backlog", hostedAt)
	seedHostedLedger(f, "led_hosted01", "sty_hosted01", "note", map[string]any{"k": "v"}, hostedAt)
	// A same-id row the local side holds an OLDER copy of: hosted lands, no conflict.
	seedHostedStory(f, localID, "Hosted retitle", "backlog", hostedAt)

	out, err := runRoot(t, "sync", "workstate", "pull", "--server", ts.URL)
	if err != nil {
		t.Fatalf("pull without --force: %v\n%s", err, out)
	}
	if strings.Contains(out, "conflict") {
		t.Errorf("unexpected conflict in output:\n%s", out)
	}
	list, err := runRoot(t, "story", "list", "--limit", "50")
	if err != nil {
		t.Fatalf("list: %v\n%s", err, list)
	}
	for _, want := range []string{"sty_hosted01", "Hosted story", localID} {
		if !strings.Contains(list, want) {
			t.Errorf("story list missing %q:\n%s", want, list)
		}
	}
}

// AC2: a same-id story the local side changed since hosted refuses, names the
// id and area, and --force lets hosted win.
func TestSyncWorkstatePullConflictFails(t *testing.T) {
	ts, _ := newFakeWorkstateServer(t)
	seedCred(t, ts.URL)
	workstateRepo(t, mergeToml)

	id := createStoryID(t, "Original title")
	if out, err := runRoot(t, "sync", "workstate", "push", "--server", ts.URL); err != nil {
		t.Fatalf("push: %v\n%s", err, out)
	}
	// Local moves on after hosted: newer and different.
	time.Sleep(10 * time.Millisecond)
	if out, err := runRoot(t, "story", "set", id, "--title", "Local edit"); err != nil {
		t.Fatalf("set: %v\n%s", err, out)
	}

	out, err := runRoot(t, "sync", "workstate", "pull", "--server", ts.URL)
	if err == nil {
		t.Fatalf("expected conflict, got success: %s", out)
	}
	if !errors.Is(err, ErrWorkstatePullConflict) {
		t.Fatalf("want ErrWorkstatePullConflict, got %v", err)
	}
	for _, want := range []string{"stories " + id, "--force", "hosted win"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q: %v", want, err)
		}
	}
	got, _ := runRoot(t, "story", "get", id)
	if !strings.Contains(got, "Local edit") {
		t.Errorf("local edit lost after refused pull: %s", got)
	}

	out, err = runRoot(t, "sync", "workstate", "pull", "--server", ts.URL, "--force")
	if err != nil {
		t.Fatalf("forced pull: %v\n%s", err, out)
	}
	got, _ = runRoot(t, "story", "get", id)
	if !strings.Contains(got, "Original title") {
		t.Errorf("--force did not let hosted win: %s", got)
	}
}

// AC2: a same-id ledger row whose payload differs refuses.
func TestSyncWorkstatePullLedgerConflictFails(t *testing.T) {
	ts, f := newFakeWorkstateServer(t)
	seedCred(t, ts.URL)
	workstateRepo(t, mergeToml)

	createStoryID(t, "Has a ledger row")
	if out, err := runRoot(t, "sync", "workstate", "push", "--server", ts.URL); err != nil {
		t.Fatalf("push: %v\n%s", err, out)
	}
	var tampered string
	f.mu.Lock()
	for id, raw := range f.ledgerByID["probe"] {
		m := raw.(map[string]any)
		m["payload"] = map[string]any{"tampered": true}
		tampered = id
		break
	}
	f.mu.Unlock()
	if tampered == "" {
		t.Fatal("push carried no ledger row to tamper with")
	}

	out, err := runRoot(t, "sync", "workstate", "pull", "--server", ts.URL)
	if err == nil {
		t.Fatalf("expected ledger conflict, got success: %s", out)
	}
	if !errors.Is(err, ErrWorkstatePullConflict) {
		t.Fatalf("want ErrWorkstatePullConflict, got %v", err)
	}
	for _, want := range []string{"ledger", tampered, "--force"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q: %v", want, err)
		}
	}
}

// AC2: --force still overrides.
func TestSyncWorkstatePullConflictForceOverrides(t *testing.T) {
	ts, _ := newFakeWorkstateServer(t)
	seedCred(t, ts.URL)
	workstateRepo(t, mergeToml)

	createStoryID(t, "Hosted title")
	if out, err := runRoot(t, "sync", "workstate", "push", "--server", ts.URL); err != nil {
		t.Fatalf("push: %v\n%s", err, out)
	}
	out, err := runRoot(t, "sync", "workstate", "pull", "--server", ts.URL, "--force")
	if err != nil {
		t.Fatalf("force pull: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Pulled work-state") {
		t.Fatalf("force pull output: %q", out)
	}
}

// AC3: rehydrate onto a freshly initialised repo plus a locally created story
// hosted has never seen completes without --force.
func TestSyncRehydrateFreshInitNoForce(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	hosted0 := time.Now().Add(time.Hour).UTC()
	repo := workstateRepo(t, mergeToml)
	t.Chdir(repo)
	if out, err := runRoot(t, "init"); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	// init may rewrite the config; restore the opt-in the fixture declared.
	if err := os.WriteFile(filepath.Join(repo, ".satelle", "satelle.toml"), []byte(mergeToml), 0o644); err != nil {
		t.Fatal(err)
	}
	localID := createStoryID(t, "Local after init")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/workspaces", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]string{{"id": "ws-personal", "kind": "personal", "name": "personal"}})
	})
	mux.HandleFunc("GET /api/v1/projects/{project}/config", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]any{})
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	seedCred(t, ts.URL)
	f := &fakeWorkstateServer{
		itemsByID:  map[string]map[string]any{},
		ledgerByID: map[string]map[string]any{},
	}
	attachFakeWorkstateGRPC(t, f)
	seedHostedStory(f, "sty_hosted01", "Hosted story", "backlog", hosted0)
	seedHostedLedger(f, "led_hosted01", "sty_hosted01", "note", map[string]any{}, hosted0)

	out, err := runRoot(t, "sync", "rehydrate", "--server", ts.URL)
	if err != nil {
		t.Fatalf("rehydrate without --force: %v\n%s", err, out)
	}
	if !strings.Contains(out, "rehydrate: done") {
		t.Errorf("rehydrate did not finish:\n%s", out)
	}
	list, _ := runRoot(t, "story", "list", "--limit", "50")
	for _, want := range []string{"sty_hosted01", localID} {
		if !strings.Contains(list, want) {
			t.Errorf("story list missing %q:\n%s", want, list)
		}
	}
}
