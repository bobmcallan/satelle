package cli

import (
	"encoding/json"

	"errors"
	"github.com/bobmcallan/satelle/internal/hosted"
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
// accumulates items/ledger by id across POSTs (sty_88e83180 AC8).
type fakeWorkstateServer struct {
	mu         sync.Mutex
	posts      map[string][]map[string]any // wsID -> posted batches
	itemsByID  map[string]map[string]any   // project -> id -> record
	ledgerByID map[string]map[string]any
	gets       int
	failOnPost int // 1-based: fail this POST with 500; 0 = never
	postN      int
}

func newFakeWorkstateServer(t *testing.T) (*httptest.Server, *fakeWorkstateServer) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	// Work-state cursor lives in the document-sync state file (sty_88e83180).
	hosted.DocumentSyncStatePathOverride = filepath.Join(t.TempDir(), "document-sync-state.json")
	t.Cleanup(func() { hosted.DocumentSyncStatePathOverride = "" })
	f := &fakeWorkstateServer{
		posts:      map[string][]map[string]any{},
		itemsByID:  map[string]map[string]any{},
		ledgerByID: map[string]map[string]any{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/workspaces", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]string{
			{"id": "ws-personal", "kind": "personal", "name": "personal"},
			{"id": "ws-team", "kind": "team", "name": "Acme"},
		})
	})
	mux.HandleFunc("POST /api/v1/projects/{project}/workstate", func(w http.ResponseWriter, r *http.Request) {
		key := r.PathValue("project")
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.postN++
		n := f.postN
		fail := f.failOnPost
		f.mu.Unlock()
		if fail > 0 && n == fail {
			http.Error(w, "injected failure", http.StatusInternalServerError)
			return
		}
		var batch map[string]any
		_ = json.Unmarshal(body, &batch)
		f.mu.Lock()
		f.posts[key] = append(f.posts[key], batch)
		if f.itemsByID[key] == nil {
			f.itemsByID[key] = map[string]any{}
		}
		if f.ledgerByID[key] == nil {
			f.ledgerByID[key] = map[string]any{}
		}
		items, _ := batch["items"].([]any)
		ledger, _ := batch["ledger"].([]any)
		for _, it := range items {
			m, ok := it.(map[string]any)
			if !ok {
				continue
			}
			id, _ := m["id"].(string)
			if id != "" {
				f.itemsByID[key][id] = m
			}
		}
		for _, it := range ledger {
			m, ok := it.(map[string]any)
			if !ok {
				continue
			}
			id, _ := m["id"].(string)
			if id != "" {
				f.ledgerByID[key][id] = m
			}
		}
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]int{"items": len(items), "ledger": len(ledger)})
	})
	// Mirror GET shape: promote fields from accumulated records.
	mux.HandleFunc("GET /api/v1/projects/{project}/workstate/items", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.gets++
		key := r.PathValue("project")
		byID := f.itemsByID[key]
		f.mu.Unlock()
		kindFilter := r.URL.Query().Get("kind")
		out := make([]map[string]any, 0, len(byID))
		for _, it := range byID {
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
	mux.HandleFunc("GET /api/v1/projects/{project}/workstate/ledger", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.gets++
		key := r.PathValue("project")
		byID := f.ledgerByID[key]
		f.mu.Unlock()
		storyFilter := r.URL.Query().Get("story")
		out := make([]map[string]any, 0, len(byID))
		for _, it := range byID {
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
	if f.postCount("probe") != 0 {
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
	if f.postCount("probe") != 1 {
		t.Fatalf("personal posts = %d, want 1", f.postCount("probe"))
	}
	if f.postCount("ws-team") != 0 {
		t.Error("workstate must never post to the team workspace")
	}
	items := f.lastItems("probe")
	if len(items) == 0 {
		t.Fatal("expected at least one item in the push")
	}

	// No-op re-push: cursor advanced, nothing new — no POST (sty_88e83180 AC2).
	out2, err := runRoot(t, "sync", "workstate", "push", "--server", ts.URL)
	if err != nil {
		t.Fatalf("re-push: %v\n%s", err, out2)
	}
	if f.postCount("probe") != 1 {
		t.Fatalf("no-op re-push posts = %d, want 1 (no second POST)", f.postCount("probe"))
	}
	if !strings.Contains(out2, "up to date") {
		t.Fatalf("no-op re-push should say up to date: %q", out2)
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
	if f.postCount("probe") != 1 {
		t.Fatalf("personal posts = %d, want 1 (shared scope still personal dest)", f.postCount("probe"))
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

func TestSyncWorkstateSnapshotSkipsLocal(t *testing.T) {
	ts, f := newFakeWorkstateServer(t)
	seedCred(t, ts.URL)
	workstateRepo(t, "web_port = 8181\n")

	out, err := runRoot(t, "sync", "workstate", "snapshot", "--server", ts.URL)
	if err != nil {
		t.Fatalf("snapshot: %v\n%s", err, out)
	}
	if !strings.Contains(out, "all areas local") {
		t.Fatalf("expected local-skip message, got: %q", out)
	}
	if f.getCount() != 0 {
		t.Errorf("local snapshot contacted server %d time(s)", f.getCount())
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
	if f.postCount("probe") != 1 {
		t.Fatalf("posts = %d", f.postCount("probe"))
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

func TestSyncWorkstateSnapshotRoundTrip(t *testing.T) {
	ts, f := newFakeWorkstateServer(t)
	seedCred(t, ts.URL)
	workstateRepo(t, "web_port = 8181\n\n[sync]\nstories = \"personal\"\nledger = \"personal\"\n\n[hosted]\nproject = \"probe\"\n")

	out, err := runRoot(t, "story", "create",
		"--title", "Snapshot story",
		"--body", "Body for snapshot.",
		"--acceptance", "1. AC survives snapshot",
		"--category", "feature",
	)
	if err != nil {
		t.Fatalf("story create: %v\n%s", err, out)
	}
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
		if title, _ := s["title"].(string); title == "Snapshot story" {
			styID, _ = s["id"].(string)
			break
		}
	}
	if styID == "" {
		t.Fatalf("story not found: %s", listOut)
	}
	out, err = runRoot(t, "sync", "workstate", "push", "--server", ts.URL)
	if err != nil {
		t.Fatalf("push: %v\n%s", err, out)
	}
	if f.postCount("probe") < 1 {
		t.Fatalf("posts = %d", f.postCount("probe"))
	}
	dbPath := runtimeDBPath(t)
	_ = os.Remove(dbPath)
	_ = os.Remove(dbPath + "-wal")
	_ = os.Remove(dbPath + "-shm")

	out, err = runRoot(t, "sync", "workstate", "snapshot", "--server", ts.URL)
	if err != nil {
		t.Fatalf("snapshot: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Snapshot work-state") {
		t.Fatalf("snapshot output: %q", out)
	}
	got, err := runRoot(t, "story", "get", styID)
	if err != nil {
		t.Fatalf("get after snapshot: %v\n%s", err, got)
	}
	if !strings.Contains(got, "Snapshot story") {
		t.Errorf("title missing: %s", got)
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

// TestSyncWorkstatePushCursorSurvivesMidRunFailure (sty_88e83180 AC3): force two
// item chunks; the second POST fails after the first succeeded; cursor stays
// pre-run so the next push re-sends the unconfirmed chunk.
func TestSyncWorkstatePushCursorSurvivesMidRunFailure(t *testing.T) {
	ts, f := newFakeWorkstateServer(t)
	seedCred(t, ts.URL)
	repo := workstateRepo(t, "web_port = 8181\n\n[sync]\nstories = \"personal\"\n\n[hosted]\nproject = \"probe\"\n")

	prevItem, prevLed := workstateItemChunk, workstateLedgerChunk
	workstateItemChunk, workstateLedgerChunk = 1, 1
	t.Cleanup(func() {
		workstateItemChunk, workstateLedgerChunk = prevItem, prevLed
	})

	// Two stories, no prior cursor → two POSTs (chunk size 1). Fail the second.
	for _, title := range []string{"Chunk A", "Chunk B"} {
		out, err := runRoot(t, "story", "create",
			"--title", title, "--body", "body", "--acceptance", "1. ok", "--category", "chore")
		if err != nil {
			t.Fatalf("create %s: %v\n%s", title, err, out)
		}
	}
	f.mu.Lock()
	f.failOnPost = 2 // first chunk lands, second fails
	f.mu.Unlock()

	out, err := runRoot(t, "sync", "workstate", "push", "--server", ts.URL)
	if err == nil {
		t.Fatalf("expected mid-run failure, got: %s", out)
	}
	// Cursor must not advance: first chunk confirmed, second failed.
	cur, err := hosted.LoadWorkstateCursor(ts.URL, "probe", repo)
	if err != nil {
		t.Fatal(err)
	}
	if !cur.ItemsUpdatedAt.IsZero() {
		t.Fatalf("cursor must stay zero after partial failure, got %v", cur.ItemsUpdatedAt)
	}

	// Recovery: clear failure, full push (still no cursor) re-sends both.
	f.mu.Lock()
	f.failOnPost = 0
	f.mu.Unlock()
	out, err = runRoot(t, "sync", "workstate", "push", "--server", ts.URL)
	if err != nil {
		t.Fatalf("recovery push: %v\n%s", err, out)
	}
	cur, err = hosted.LoadWorkstateCursor(ts.URL, "probe", repo)
	if err != nil {
		t.Fatal(err)
	}
	if cur.ItemsUpdatedAt.IsZero() {
		t.Fatal("cursor should advance after full success")
	}
	// Accumulated map has both stories.
	f.mu.Lock()
	n := len(f.itemsByID["probe"])
	f.mu.Unlock()
	if n < 2 {
		t.Fatalf("accumulated items = %d, want ≥2", n)
	}
}

// TestSyncWorkstateIncrementalRehydrate (sty_88e83180 AC8): full push, mutate,
// incremental push, wipe local DB, pull — every id restores with current content.
func TestSyncWorkstateIncrementalRehydrate(t *testing.T) {
	ts, _ := newFakeWorkstateServer(t)
	seedCred(t, ts.URL)
	workstateRepo(t, "web_port = 8181\n\n[sync]\nstories = \"personal\"\nledger = \"personal\"\n\n[hosted]\nproject = \"probe\"\n")

	out, err := runRoot(t, "story", "create",
		"--title", "Keep me", "--body", "body", "--acceptance", "1. ok", "--category", "feature")
	if err != nil {
		t.Fatalf("create: %v\n%s", err, out)
	}
	listOut, _ := runRoot(t, "story", "list", "--limit", "5")
	var stories []map[string]any
	_ = json.Unmarshal([]byte(listOut), &stories)
	var keepID string
	for _, s := range stories {
		if s["title"] == "Keep me" {
			keepID, _ = s["id"].(string)
		}
	}
	if keepID == "" {
		t.Fatal("keep id missing")
	}
	if out, err = runRoot(t, "sync", "workstate", "push", "--server", ts.URL); err != nil {
		t.Fatalf("full push: %v\n%s", err, out)
	}

	// Mutate + create.
	if out, err = runRoot(t, "story", "set", keepID, "--body", "updated body"); err != nil {
		t.Fatalf("mutate: %v\n%s", err, out)
	}
	if out, err = runRoot(t, "story", "create",
		"--title", "New after", "--body", "new", "--acceptance", "1. ok", "--category", "feature"); err != nil {
		t.Fatalf("create2: %v\n%s", err, out)
	}
	if out, err = runRoot(t, "sync", "workstate", "push", "--server", ts.URL); err != nil {
		t.Fatalf("incremental push: %v\n%s", err, out)
	}

	// Wipe local.
	dbPath := runtimeDBPath(t)
	_ = os.Remove(dbPath)
	_ = os.Remove(dbPath + "-wal")
	_ = os.Remove(dbPath + "-shm")

	if out, err = runRoot(t, "sync", "workstate", "pull", "--server", ts.URL, "--dry-run=false"); err != nil {
		t.Fatalf("pull: %v\n%s", err, out)
	}
	got, err := runRoot(t, "story", "get", keepID)
	if err != nil {
		t.Fatalf("get keep: %v\n%s", err, got)
	}
	if !strings.Contains(got, "updated body") {
		t.Errorf("mutated body missing after rehydrate: %s", got)
	}
	listOut, _ = runRoot(t, "story", "list", "--limit", "10")
	if !strings.Contains(listOut, "New after") {
		t.Errorf("new story missing after rehydrate: %s", listOut)
	}
}

// TestSyncWorkstatePushIncrementalOnlyChanged (sty_88e83180 AC1): after a full
// push, mutating one of two stories makes the next POST carry only that id.
func TestSyncWorkstatePushIncrementalOnlyChanged(t *testing.T) {
	ts, f := newFakeWorkstateServer(t)
	seedCred(t, ts.URL)
	workstateRepo(t, "web_port = 8181\n\n[sync]\nstories = \"personal\"\n\n[hosted]\nproject = \"probe\"\n")

	var ids []string
	for _, title := range []string{"Alpha", "Beta"} {
		out, err := runRoot(t, "story", "create",
			"--title", title, "--body", "b", "--acceptance", "1. ok", "--category", "chore")
		if err != nil {
			t.Fatalf("create: %v\n%s", err, out)
		}
	}
	listOut, _ := runRoot(t, "story", "list", "--limit", "10")
	var stories []map[string]any
	_ = json.Unmarshal([]byte(listOut), &stories)
	for _, s := range stories {
		if id, _ := s["id"].(string); id != "" {
			ids = append(ids, id)
		}
	}
	if len(ids) < 2 {
		t.Fatalf("need 2 stories, got %v", ids)
	}
	if out, err := runRoot(t, "sync", "workstate", "push", "--server", ts.URL); err != nil {
		t.Fatalf("full: %v\n%s", err, out)
	}
	n1 := f.postCount("probe")

	// Touch only the first story.
	if out, err := runRoot(t, "story", "set", ids[0], "--body", "changed"); err != nil {
		t.Fatalf("mutate: %v\n%s", err, out)
	}
	if out, err := runRoot(t, "sync", "workstate", "push", "--server", ts.URL); err != nil {
		t.Fatalf("incr: %v\n%s", err, out)
	}
	if f.postCount("probe") != n1+1 {
		t.Fatalf("posts = %d, want %d", f.postCount("probe"), n1+1)
	}
	items := f.lastItems("probe")
	if len(items) != 1 {
		t.Fatalf("incremental batch size = %d, want 1: %+v", len(items), items)
	}
	m, _ := items[0].(map[string]any)
	if id, _ := m["id"].(string); id != ids[0] {
		t.Fatalf("incremental id = %q, want %q", id, ids[0])
	}
}

// TestWorkstatePushCarriesNoAttachmentBodies (sty_948ad5df AC4): a planted
// secret in a type:change story attachment never appears in a workstate POST.
// Workstate push is items+ledger only — attachment bodies stay local.
func TestWorkstatePushCarriesNoAttachmentBodies(t *testing.T) {
	const secret = "planted-change-attachment-secret-xyz-948ad5df"
	ts, f := newFakeWorkstateServer(t)
	seedCred(t, ts.URL)
	repo := workstateRepo(t, "web_port = 8181\n\n[sync]\nstories = \"personal\"\n\n[hosted]\nproject = \"probe\"\n")

	out, err := runRoot(t, "story", "create",
		"--title", "Attachment secrecy",
		"--body", "Prove workstate push does not send change patches.",
		"--acceptance", "1. secret stays local",
	)
	if err != nil {
		t.Fatalf("story create: %v\n%s", err, out)
	}
	// Create may append a notice after the JSON object — decode with decoder.
	var created map[string]any
	dec := json.NewDecoder(strings.NewReader(out))
	if err := dec.Decode(&created); err != nil {
		t.Fatalf("parse create: %v\n%s", err, out)
	}
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatalf("no story id in %s", out)
	}
	// Attach a change patch with a planted secret under the story attachment dir.
	// story attach uses runtime stories dir; write via CLI.
	if out, err := runRoot(t, "story", "attach", id,
		"--name", "change-in_progress-done",
		"--type", "change",
		"--body", "diff --git a/secret.go\n+"+secret+"\n",
	); err != nil {
		t.Fatalf("attach: %v\n%s", err, out)
	}
	// Also ensure attachment exists on disk under repo if CLI uses different root.
	_ = repo

	if out, err := runRoot(t, "sync", "workstate", "push", "--server", ts.URL); err != nil {
		t.Fatalf("push: %v\n%s", err, out)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	posts := f.posts["probe"]
	if len(posts) == 0 {
		t.Fatal("expected at least one workstate POST")
	}
	blob, _ := json.Marshal(posts)
	if strings.Contains(string(blob), secret) {
		t.Error("planted change-attachment secret must not appear in any workstate POST body")
	}
}

// TestWorkstatePushExcludesBinaryAttachment (sty_40e5a305 AC9): a binary
// attachment under the runtime stories dir never rides the workstate mirror.
func TestWorkstatePushExcludesBinaryAttachment(t *testing.T) {
	const plant = "BIN40E5A305-WORKSTATE-EXCLUSION-MARKER"
	// Minimal PNG-like bytes with planted ASCII so a leak is greppable.
	png := []byte{
		0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a,
		0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4, 0x89,
		0x00, 0x00, 0x00, 0x0a, 0x49, 0x44, 0x41, 0x54,
		0x78, 0x9c, 0x63, 0x00, 0x01, 0x00, 0x00, 0x05, 0x00, 0x01,
		0x0d, 0x0a, 0x2d, 0xb4,
		0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4e, 0x44,
		0xae, 0x42, 0x60, 0x82,
	}
	png = append(png, []byte(plant)...)

	ts, f := newFakeWorkstateServer(t)
	seedCred(t, ts.URL)
	_ = workstateRepo(t, "web_port = 8181\n\n[sync]\nstories = \"personal\"\n\n[hosted]\nproject = \"probe\"\n")

	out, err := runRoot(t, "story", "create",
		"--title", "Binary exclusion",
		"--body", "Binary attachments stay off the workstate wire.",
		"--acceptance", "1. binary not in push",
	)
	if err != nil {
		t.Fatalf("create: %v\n%s", err, out)
	}
	var created map[string]any
	if err := json.NewDecoder(strings.NewReader(out)).Decode(&created); err != nil {
		t.Fatalf("parse: %v\n%s", err, out)
	}
	id, _ := created["id"].(string)
	binPath := filepath.Join(t.TempDir(), "shot.png")
	if err := os.WriteFile(binPath, png, 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := runRoot(t, "story", "attach", id,
		"--name", "shot.png", "--type", "screenshot",
		"--binary-file", binPath, "--content-type", "image/png",
	); err != nil {
		t.Fatalf("binary attach: %v\n%s", err, out)
	}

	if out, err := runRoot(t, "sync", "workstate", "push", "--server", ts.URL); err != nil {
		t.Fatalf("push: %v\n%s", err, out)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	blob, _ := json.Marshal(f.posts["probe"])
	if strings.Contains(string(blob), plant) {
		t.Error("binary plant must not appear in workstate POST bodies")
	}
	if strings.Contains(string(blob), "shot.png") && strings.Contains(string(blob), "data_base64") {
		t.Error("binary attachment payload must not ride workstate")
	}
}
