package cli

import (
	"encoding/json"
	"errors"
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
// serves the personal + team workspace list. GET items/ledger serve the last
// ingested batch (hermetic rehydrate).
type fakeWorkstateServer struct {
	mu      sync.Mutex
	posts   map[string][]map[string]any // wsID -> posted batches
	lastRaw map[string][]byte
	gets    int
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
		if proj := r.URL.Query().Get("project"); proj != "" {
			wsID = wsID + "|" + proj
		}
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
	// Mirror GET shape: promote fields from stored record for list responses.
	mux.HandleFunc("GET /api/v1/workspaces/{id}/workstate/items", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.gets++
		key := r.PathValue("id")
		if proj := r.URL.Query().Get("project"); proj != "" {
			key = key + "|" + proj
		}
		raw := f.lastRaw[key]
		f.mu.Unlock()
		var batch map[string]any
		_ = json.Unmarshal(raw, &batch)
		items, _ := batch["items"].([]any)
		kindFilter := r.URL.Query().Get("kind")
		out := make([]map[string]any, 0, len(items))
		for _, it := range items {
			m, ok := it.(map[string]any)
			if !ok {
				continue
			}
			kind, _ := m["kind"].(string)
			if kindFilter != "" && kind != kindFilter {
				continue
			}
			id, _ := m["id"].(string)
			status, _ := m["status"].(string)
			title, _ := m["title"].(string)
			rec, _ := json.Marshal(m)
			out = append(out, map[string]any{
				"id": id, "kind": kind, "type": kind, "status": status, "title": title,
				"origin": "cli-sync", "record": json.RawMessage(rec),
			})
		}
		_ = json.NewEncoder(w).Encode(out)
	})
	mux.HandleFunc("GET /api/v1/workspaces/{id}/workstate/ledger", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.gets++
		key := r.PathValue("id")
		if proj := r.URL.Query().Get("project"); proj != "" {
			key = key + "|" + proj
		}
		raw := f.lastRaw[key]
		f.mu.Unlock()
		var batch map[string]any
		_ = json.Unmarshal(raw, &batch)
		ledger, _ := batch["ledger"].([]any)
		storyFilter := r.URL.Query().Get("story")
		out := make([]map[string]any, 0, len(ledger))
		for _, it := range ledger {
			m, ok := it.(map[string]any)
			if !ok {
				continue
			}
			sid, _ := m["story_id"].(string)
			if storyFilter != "" && sid != storyFilter {
				continue
			}
			id, _ := m["id"].(string)
			kind, _ := m["kind"].(string)
			rec, _ := json.Marshal(m)
			out = append(out, map[string]any{
				"id": id, "story_id": sid, "kind": kind, "type": "ledger",
				"origin": "cli-sync", "record": json.RawMessage(rec),
			})
		}
		_ = json.NewEncoder(w).Encode(out)
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

func (f *fakeWorkstateServer) getCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.gets
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
	if f.postCount("ws-personal|probe") != 0 {
		t.Error("local-scope push contacted the server")
	}
}

// TestSyncWorkstatePushPersonalAndIdempotent: opted-in stories push to personal
// workspace; a re-push succeeds (server upserts) (AC1, AC2, AC5).
func TestSyncWorkstatePushPersonalAndIdempotent(t *testing.T) {
	ts, f := newFakeWorkstateServer(t)
	seedCred(t, ts.URL)
	workstateRepo(t, "web_port = 8181\n\n[sync]\nstories = \"personal\"\n\n[hosted]\nproject = \"probe\"\n")

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
	if !strings.Contains(out, "personal collection") && !strings.Contains(out, "personal workspace") {
		t.Fatalf("push output should name personal workspace: %q", out)
	}
	if f.postCount("ws-personal|probe") != 1 {
		t.Fatalf("personal posts = %d, want 1", f.postCount("ws-personal|probe"))
	}
	if f.postCount("ws-team") != 0 {
		t.Error("workstate must never post to the team workspace")
	}
	items := f.lastItems("ws-personal|probe")
	if len(items) == 0 {
		t.Fatal("expected at least one item in the push")
	}

	// Idempotent re-push.
	out2, err := runRoot(t, "sync", "workstate", "push", "--server", ts.URL)
	if err != nil {
		t.Fatalf("re-push: %v\n%s", err, out2)
	}
	if f.postCount("ws-personal|probe") != 2 {
		t.Fatalf("re-push posts = %d, want 2", f.postCount("ws-personal|probe"))
	}
}

// TestSyncWorkstatePushIgnoresTeamBinding: even with active team workspace,
// work-state goes to personal only (AC1, AC3, AC5).
func TestSyncWorkstatePushIgnoresTeamBinding(t *testing.T) {
	ts, f := newFakeWorkstateServer(t)
	seedCred(t, ts.URL)
	workstateRepo(t, "web_port = 8181\n\n[sync]\nstories = \"shared\"\n\n[hosted]\nproject = \"probe\"\nworkspace = \"Acme\"\n")

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
	if f.postCount("ws-personal|probe") != 1 {
		t.Fatalf("personal posts = %d, want 1 (shared scope still personal dest)", f.postCount("ws-personal|probe"))
	}
	if f.postCount("ws-team") != 0 {
		t.Error("shared-scope workstate must NOT route to team workspace")
	}
	if !strings.Contains(out, "personal collection") && !strings.Contains(out, "personal workspace") {
		t.Fatalf("output should confirm personal dest: %q", out)
	}
}

// TestSyncWorkstatePushRequiresBoundProject (AC5): opted-in workstate without a
// bound project fails client-side with zero network calls.
func TestSyncWorkstatePushRequiresBoundProject(t *testing.T) {
	var hits int
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		hits++
		t.Errorf("unexpected network call to %s %s", r.Method, r.URL.String())
		w.WriteHeader(http.StatusInternalServerError)
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	seedCred(t, ts.URL)
	workstateRepo(t, "web_port = 8181\n\n[sync]\nstories = \"personal\"\n") // no project

	out, err := runRoot(t, "sync", "workstate", "push", "--server", ts.URL)
	if err == nil || !strings.Contains(err.Error(), "no hosted project bound") {
		t.Fatalf("expected unbound-project error, got %v\n%s", err, out)
	}
	if hits != 0 {
		t.Fatalf("unbound project contacted server %d time(s)", hits)
	}
}

// TestSyncWorkstatePullSkipsLocal: all-local areas skip with zero GET calls.
func TestSyncWorkstatePullSkipsLocal(t *testing.T) {
	ts, f := newFakeWorkstateServer(t)
	seedCred(t, ts.URL)
	workstateRepo(t, "web_port = 8181\n")

	out, err := runRoot(t, "sync", "workstate", "pull", "--server", ts.URL)
	if err != nil {
		t.Fatalf("pull: %v\n%s", err, out)
	}
	if !strings.Contains(out, "every work-state area is local") {
		t.Fatalf("expected local-skip message, got: %q", out)
	}
	if f.getCount() != 0 {
		t.Errorf("local pull contacted server %d time(s)", f.getCount())
	}
}

// TestSyncWorkstatePullRequiresBoundProject: no project → fail before network.
func TestSyncWorkstatePullRequiresBoundProject(t *testing.T) {
	var hits int
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusInternalServerError)
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	seedCred(t, ts.URL)
	workstateRepo(t, "web_port = 8181\n\n[sync]\nstories = \"personal\"\n")

	out, err := runRoot(t, "sync", "workstate", "pull", "--server", ts.URL)
	if err == nil || !strings.Contains(err.Error(), "no hosted project bound") {
		t.Fatalf("expected unbound-project error, got %v\n%s", err, out)
	}
	if hits != 0 {
		t.Fatalf("unbound project contacted server %d time(s)", hits)
	}
}

// TestSyncWorkstatePullDryRun: dry-run lists areas without GETs.
func TestSyncWorkstatePullDryRun(t *testing.T) {
	ts, f := newFakeWorkstateServer(t)
	seedCred(t, ts.URL)
	workstateRepo(t, "web_port = 8181\n\n[sync]\nstories = \"personal\"\n\n[hosted]\nproject = \"probe\"\n")

	out, err := runRoot(t, "sync", "workstate", "pull", "--server", ts.URL, "--dry-run")
	if err != nil {
		t.Fatalf("pull dry-run: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Would pull") || !strings.Contains(out, "stories") {
		t.Fatalf("dry-run output: %q", out)
	}
	if f.getCount() != 0 {
		t.Errorf("dry-run contacted server %d time(s)", f.getCount())
	}
}

// TestSyncWorkstatePullRoundTrip: push → wipe local stories → pull restores id/AC.
func TestSyncWorkstatePullRoundTrip(t *testing.T) {
	ts, f := newFakeWorkstateServer(t)
	seedCred(t, ts.URL)
	workstateRepo(t, "web_port = 8181\n\n[sync]\nstories = \"personal\"\nledger = \"personal\"\n\n[hosted]\nproject = \"probe\"\n")

	out, err := runRoot(t, "story", "create",
		"--title", "Roundtrip story",
		"--body", "Body for rehydrate.",
		"--acceptance", "1. AC survives pull",
		"--category", "feature",
	)
	if err != nil {
		t.Fatalf("story create: %v\n%s", err, out)
	}
	// Parse id from create JSON if present; otherwise list.
	listOut, err := runRoot(t, "story", "list", "--limit", "5")
	if err != nil {
		t.Fatalf("list: %v\n%s", err, listOut)
	}
	var stories []map[string]any
	if jerr := json.Unmarshal([]byte(listOut), &stories); jerr != nil {
		t.Fatalf("parse list: %v\n%s", jerr, listOut)
	}
	var styID string
	for _, s := range stories {
		if title, _ := s["title"].(string); title == "Roundtrip story" {
			styID, _ = s["id"].(string)
			break
		}
	}
	if styID == "" {
		t.Fatalf("story not found in list: %s", listOut)
	}

	out, err = runRoot(t, "sync", "workstate", "push", "--server", ts.URL)
	if err != nil {
		t.Fatalf("push: %v\n%s", err, out)
	}
	if f.postCount("ws-personal|probe") != 1 {
		t.Fatalf("posts = %d", f.postCount("ws-personal|probe"))
	}

	// Wipe local work items by opening store via a second push path isn't available;
	// delete via story is not archive-all. Use SQL-free approach: remove the
	// home-keyed runtime DB (sty_4660bbe1) and re-open empty.
	dbPath := runtimeDBPath(t)
	_ = os.Remove(dbPath)
	_ = os.Remove(dbPath + "-wal")
	_ = os.Remove(dbPath + "-shm")

	// Confirm empty.
	listOut, err = runRoot(t, "story", "list", "--limit", "5")
	if err != nil {
		t.Fatalf("list after wipe: %v\n%s", err, listOut)
	}
	if strings.Contains(listOut, styID) {
		t.Fatalf("story still present after db wipe: %s", listOut)
	}

	// --dry-run=false: registered cobra flag vars persist across tests in-process.
	out, err = runRoot(t, "sync", "workstate", "pull", "--server", ts.URL, "--dry-run=false")
	if err != nil {
		t.Fatalf("pull: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Pulled work-state") {
		t.Fatalf("pull output: %q", out)
	}

	got, err := runRoot(t, "story", "get", styID)
	if err != nil {
		t.Fatalf("get after pull: %v\n%s", err, got)
	}
	if !strings.Contains(got, "Roundtrip story") {
		t.Errorf("title missing: %s", got)
	}
	if !strings.Contains(got, "AC survives pull") {
		t.Errorf("acceptance_criteria missing after pull: %s", got)
	}
}

// TestSyncWorkstatePullConflictFails: both non-empty without --force.
func TestSyncWorkstatePullConflictFails(t *testing.T) {
	ts, _ := newFakeWorkstateServer(t)
	seedCred(t, ts.URL)
	workstateRepo(t, "web_port = 8181\n\n[sync]\nstories = \"personal\"\n\n[hosted]\nproject = \"probe\"\n")

	out, err := runRoot(t, "story", "create",
		"--title", "Local kept",
		"--body", "local row",
		"--acceptance", "1. stays",
	)
	if err != nil {
		t.Fatalf("create: %v\n%s", err, out)
	}
	out, err = runRoot(t, "sync", "workstate", "push", "--server", ts.URL)
	if err != nil {
		t.Fatalf("push: %v\n%s", err, out)
	}

	// Create a second local story so local is non-empty and hosted is non-empty
	// (hosted has first story from push; local has both — conflict on stories).
	out, err = runRoot(t, "story", "create",
		"--title", "Local only extra",
		"--body", "extra",
		"--acceptance", "1. x",
	)
	if err != nil {
		t.Fatalf("create2: %v\n%s", err, out)
	}

	out, err = runRoot(t, "sync", "workstate", "pull", "--server", ts.URL, "--dry-run=false")
	if err == nil {
		t.Fatalf("expected conflict, got success: %s", out)
	}
	if !errors.Is(err, ErrWorkstatePullConflict) && !strings.Contains(err.Error(), "workstate pull conflict") {
		t.Fatalf("want conflict error, got %v\n%s", err, out)
	}

	// Local-only story still present (nothing written / no wipe).
	listOut, err := runRoot(t, "story", "list", "--limit", "10")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(listOut, "Local only extra") {
		t.Errorf("local-only story should remain after conflict: %s", listOut)
	}
}

// TestSyncWorkstatePullConflictForceOverrides: --force materializes.
func TestSyncWorkstatePullConflictForceOverrides(t *testing.T) {
	ts, _ := newFakeWorkstateServer(t)
	seedCred(t, ts.URL)
	workstateRepo(t, "web_port = 8181\n\n[sync]\nstories = \"personal\"\n\n[hosted]\nproject = \"probe\"\n")

	out, err := runRoot(t, "story", "create",
		"--title", "Hosted title",
		"--body", "original",
		"--acceptance", "1. force",
	)
	if err != nil {
		t.Fatalf("create: %v\n%s", err, out)
	}
	out, err = runRoot(t, "sync", "workstate", "push", "--server", ts.URL)
	if err != nil {
		t.Fatalf("push: %v\n%s", err, out)
	}
	// Local still non-empty; force pull should succeed.
	out, err = runRoot(t, "sync", "workstate", "pull", "--server", ts.URL, "--force", "--dry-run=false")
	if err != nil {
		t.Fatalf("force pull: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Pulled work-state") {
		t.Fatalf("force pull output: %q", out)
	}
}

// TestSyncWorkstatePullIgnoresTeamBinding: GETs personal only.
func TestSyncWorkstatePullIgnoresTeamBinding(t *testing.T) {
	ts, f := newFakeWorkstateServer(t)
	seedCred(t, ts.URL)
	workstateRepo(t, "web_port = 8181\n\n[sync]\nstories = \"shared\"\n\n[hosted]\nproject = \"probe\"\nworkspace = \"Acme\"\n")

	// Seed hosted via push first.
	out, err := runRoot(t, "story", "create",
		"--title", "Team ignore pull",
		"--body", "b",
		"--acceptance", "1. a",
	)
	if err != nil {
		t.Fatalf("create: %v\n%s", err, out)
	}
	if _, err := runRoot(t, "sync", "workstate", "push", "--server", ts.URL); err != nil {
		t.Fatalf("push: %v", err)
	}
	// Empty local DB then pull (home-keyed runtime plane).
	dbPath := runtimeDBPath(t)
	_ = os.Remove(dbPath)
	_ = os.Remove(dbPath + "-wal")
	_ = os.Remove(dbPath + "-shm")

	out, err = runRoot(t, "sync", "workstate", "pull", "--server", ts.URL, "--dry-run=false")
	if err != nil {
		t.Fatalf("pull: %v\n%s", err, out)
	}
	if f.getCount() == 0 {
		t.Error("expected GET to personal")
	}
}
