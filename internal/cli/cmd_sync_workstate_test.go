package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// fakeWorkstateServer records POST /workstate bodies per workspace and
// serves the personal + team workspace list.
type fakeWorkstateServer struct {
	mu      sync.Mutex
	posts   map[string][]map[string]any // wsID -> posted batches
	lastRaw map[string][]byte
}

func newFakeWorkstateServer(t *testing.T) (*httptest.Server, *fakeWorkstateServer) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	f := &fakeWorkstateServer{
		posts:   map[string][]map[string]any{},
		lastRaw: map[string][]byte{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/workspaces", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]string{
			{"id": "ws-personal", "kind": "personal", "name": "personal"},
			{"id": "ws-team", "kind": "team", "name": "Acme"},
		})
	})
	mux.HandleFunc("POST /api/v1/workspaces/{id}/workstate", func(w http.ResponseWriter, r *http.Request) {
		wsID := r.PathValue("id")
		body, _ := io.ReadAll(r.Body)
		var batch map[string]any
		_ = json.Unmarshal(body, &batch)
		f.mu.Lock()
		f.posts[wsID] = append(f.posts[wsID], batch)
		f.lastRaw[wsID] = body
		f.mu.Unlock()
		items, _ := batch["items"].([]any)
		ledger, _ := batch["ledger"].([]any)
		_ = json.NewEncoder(w).Encode(map[string]int{"items": len(items), "ledger": len(ledger)})
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, f
}

func (f *fakeWorkstateServer) postCount(wsID string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.posts[wsID])
}

func (f *fakeWorkstateServer) lastItems(wsID string) []any {
	f.mu.Lock()
	defer f.mu.Unlock()
	posts := f.posts[wsID]
	if len(posts) == 0 {
		return nil
	}
	items, _ := posts[len(posts)-1]["items"].([]any)
	return items
}

// workstateRepo scaffolds a store-backed repo with the given [sync] work-state
// scopes and optional hosted workspace binding.
func workstateRepo(t *testing.T, satelleToml string) string {
	t.Helper()
	repo := tempRepo(t)
	cfgPath := filepath.Join(repo, ".satelle", "satelle.toml")
	if err := os.WriteFile(cfgPath, []byte(satelleToml), 0o644); err != nil {
		t.Fatal(err)
	}
	return repo
}

// TestSyncWorkstatePushSkipsLocal: all-local work-state areas produce the
// skip message and no network call (AC1, AC5).
func TestSyncWorkstatePushSkipsLocal(t *testing.T) {
	ts, f := newFakeWorkstateServer(t)
	seedCred(t, ts.URL)
	workstateRepo(t, "web_port = 8181\n") // no [sync] → all local

	out, err := runRoot(t, "sync", "workstate", "push", "--server", ts.URL)
	if err != nil {
		t.Fatalf("push: %v\n%s", err, out)
	}
	if !strings.Contains(out, "every work-state area is local") {
		t.Fatalf("expected local-skip message, got: %q", out)
	}
	if f.postCount("ws-personal") != 0 {
		t.Error("local-scope push contacted the server")
	}
}

// TestSyncWorkstatePushPersonalAndIdempotent: opted-in stories push to personal
// workspace; a re-push succeeds (server upserts) (AC1, AC2, AC5).
func TestSyncWorkstatePushPersonalAndIdempotent(t *testing.T) {
	ts, f := newFakeWorkstateServer(t)
	seedCred(t, ts.URL)
	workstateRepo(t, "web_port = 8181\n\n[sync]\nstories = \"personal\"\n")

	// Create a story so there is something to push.
	out, err := runRoot(t, "story", "create",
		"--title", "Workstate probe",
		"--body", "A story for the workstate push test.",
		"--acceptance", "1. pushed",
	)
	if err != nil {
		t.Fatalf("story create: %v\n%s", err, out)
	}

	out, err = runRoot(t, "sync", "workstate", "push", "--server", ts.URL)
	if err != nil {
		t.Fatalf("push: %v\n%s", err, out)
	}
	if !strings.Contains(out, "personal workspace") {
		t.Fatalf("push output should name personal workspace: %q", out)
	}
	if f.postCount("ws-personal") != 1 {
		t.Fatalf("personal posts = %d, want 1", f.postCount("ws-personal"))
	}
	if f.postCount("ws-team") != 0 {
		t.Error("workstate must never post to the team workspace")
	}
	items := f.lastItems("ws-personal")
	if len(items) == 0 {
		t.Fatal("expected at least one item in the push")
	}

	// Idempotent re-push.
	out2, err := runRoot(t, "sync", "workstate", "push", "--server", ts.URL)
	if err != nil {
		t.Fatalf("re-push: %v\n%s", err, out2)
	}
	if f.postCount("ws-personal") != 2 {
		t.Fatalf("re-push posts = %d, want 2", f.postCount("ws-personal"))
	}
}

// TestSyncWorkstatePushIgnoresTeamBinding: even with active team workspace,
// work-state goes to personal only (AC1, AC3, AC5).
func TestSyncWorkstatePushIgnoresTeamBinding(t *testing.T) {
	ts, f := newFakeWorkstateServer(t)
	seedCred(t, ts.URL)
	workstateRepo(t, "web_port = 8181\n\n[sync]\nstories = \"shared\"\n\n[hosted]\nworkspace = \"Acme\"\n")

	out, err := runRoot(t, "story", "create",
		"--title", "Team binding probe",
		"--body", "Shared scope still goes personal.",
		"--acceptance", "1. personal dest",
	)
	if err != nil {
		t.Fatalf("story create: %v\n%s", err, out)
	}
	out, err = runRoot(t, "sync", "workstate", "push", "--server", ts.URL)
	if err != nil {
		t.Fatalf("push: %v\n%s", err, out)
	}
	if f.postCount("ws-personal") != 1 {
		t.Fatalf("personal posts = %d, want 1 (shared scope still personal dest)", f.postCount("ws-personal"))
	}
	if f.postCount("ws-team") != 0 {
		t.Error("shared-scope workstate must NOT route to team workspace")
	}
	if !strings.Contains(out, "personal workspace") {
		t.Fatalf("output should confirm personal dest: %q", out)
	}
}
