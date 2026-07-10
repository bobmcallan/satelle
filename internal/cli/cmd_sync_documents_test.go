package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/bobmcallan/satelle/internal/hosted"
)

// fakeDocStore is an in-memory workspace document store with a monotonic
// sequence counter so ?since=N returns only seq>N items plus cursor=maxSeq.
type fakeDocStore struct {
	mu   sync.Mutex
	data map[string]map[string][][]byte // wsID -> path -> versions
	seq  map[string]map[string]int      // wsID -> path -> sequence of head
	next int                            // global sequence counter
}

func newFakeDocStore() *fakeDocStore {
	return &fakeDocStore{
		data: map[string]map[string][][]byte{},
		seq:  map[string]map[string]int{},
	}
}

func (s *fakeDocStore) put(wsID, path string, content []byte) (sha string, version int, created bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ws := s.data[wsID]
	if ws == nil {
		ws = map[string][][]byte{}
		s.data[wsID] = ws
		s.seq[wsID] = map[string]int{}
	}
	versions := ws[path]
	if len(versions) > 0 && string(versions[len(versions)-1]) == string(content) {
		return sha256hex(content), len(versions), false
	}
	ws[path] = append(versions, content)
	s.next++
	s.seq[wsID][path] = s.next
	return sha256hex(content), len(ws[path]), true
}

func (s *fakeDocStore) get(wsID, path string) (content []byte, sha string, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	versions := s.data[wsID][path]
	if len(versions) == 0 {
		return nil, "", false
	}
	c := versions[len(versions)-1]
	return c, sha256hex(c), true
}

func (s *fakeDocStore) changes(wsID, since string) (items []map[string]any, cursor string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sinceN := 0
	if since != "" {
		sinceN, _ = strconv.Atoi(since)
	}
	maxSeq := sinceN
	for path, seq := range s.seq[wsID] {
		if seq > sinceN {
			versions := s.data[wsID][path]
			head := versions[len(versions)-1]
			items = append(items, map[string]any{
				"path": path, "version": len(versions),
				"blob_sha256": sha256hex(head), "size": len(head), "created_at": "t",
			})
			if seq > maxSeq {
				maxSeq = seq
			}
		}
	}
	if items == nil {
		items = []map[string]any{}
	}
	return items, strconv.Itoa(maxSeq)
}

// newFakeDocServer stands up workspaces + document PUT/GET/list surface.
func newFakeDocServer(t *testing.T) *httptest.Server {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	// Isolate document-sync cursor store to this test's XDG dir.
	hosted.DocumentSyncStatePathOverride = ""
	store := newFakeDocStore()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/workspaces", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]string{
			{"id": "ws-personal", "kind": "personal", "name": "personal"},
			{"id": "ws-team", "kind": "team", "name": "Acme"},
		})
	})
	mux.HandleFunc("/api/v1/workspaces/", func(w http.ResponseWriter, r *http.Request) {
		rest := strings.TrimPrefix(r.URL.Path, "/api/v1/workspaces/")
		segs := strings.SplitN(rest, "/", 3) // [wsID, "documents", path?]
		if len(segs) < 2 || segs[1] != "documents" {
			http.NotFound(w, r)
			return
		}
		wsID := segs[0]
		// Project partitions the store (server sty_0e56fe79).
		if proj := r.URL.Query().Get("project"); proj != "" {
			wsID = wsID + "|" + proj
		}
		path := ""
		if len(segs) == 3 {
			path = segs[2]
		}
		switch {
		case r.Method == http.MethodPut:
			body, _ := io.ReadAll(r.Body)
			sha, ver, created := store.put(wsID, path, body)
			status := http.StatusOK
			if created {
				status = http.StatusCreated
			}
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"path": path, "version": ver, "blob_sha256": sha, "size": len(body), "created": created,
			})
		case r.Method == http.MethodGet && path == "":
			items, cursor := store.changes(wsID, r.URL.Query().Get("since"))
			_ = json.NewEncoder(w).Encode(map[string]any{"items": items, "cursor": cursor})
		case r.Method == http.MethodGet:
			content, sha, ok := store.get(wsID, path)
			if !ok {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("ETag", `"`+sha+`"`)
			_, _ = w.Write(content)
		default:
			http.NotFound(w, r)
		}
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

// TestSyncDocumentsPushPullByteExact: push documents, pull into a fresh repo —
// bytes match exactly (AC1, AC5).
func TestSyncDocumentsPushPullByteExact(t *testing.T) {
	ts := newFakeDocServer(t)
	seedCred(t, ts.URL)

	src := syncConfigRepo(t, "[sync]\ndocuments = \"personal\"\n"+boundProjectToml)
	body := "---\ntype: document\n---\nhello docs\n"
	writeRepoFile(t, src, ".satelle/documents/note.md", body)
	pointAt(t, src)

	cmd, buf := testCmd()
	if err := runSyncDocumentsPush(cmd, ts.URL, "", false); err != nil {
		t.Fatalf("push: %v\n%s", err, buf.String())
	}
	if !strings.Contains(buf.String(), "1 new") {
		t.Fatalf("push output: %q", buf.String())
	}

	dst := syncConfigRepo(t, "[sync]\ndocuments = \"personal\"\n"+boundProjectToml)
	pointAt(t, dst)
	cmd2, buf2 := testCmd()
	if err := runSyncDocumentsPull(cmd2, ts.URL, ""); err != nil {
		t.Fatalf("pull: %v\n%s", err, buf2.String())
	}
	got, err := os.ReadFile(filepath.Join(dst, ".satelle", "documents", "note.md"))
	if err != nil {
		t.Fatalf("read pulled doc: %v", err)
	}
	if string(got) != body {
		t.Errorf("pulled = %q, want %q (byte-exact)", got, body)
	}
}

// TestSyncDocumentsPushSkipsLocalArea: default local scope skips the area (AC1, AC5).
func TestSyncDocumentsPushSkipsLocalArea(t *testing.T) {
	ts := newFakeDocServer(t)
	seedCred(t, ts.URL)

	repo := syncConfigRepo(t, "")
	writeRepoFile(t, repo, ".satelle/documents/local.md", "stay local")
	pointAt(t, repo)

	cmd, buf := testCmd()
	if err := runSyncDocumentsPush(cmd, ts.URL, "", false); err != nil {
		t.Fatalf("push: %v", err)
	}
	if !strings.Contains(buf.String(), "scope is local") {
		t.Fatalf("expected local-skip message, got: %q", buf.String())
	}
}

// TestSyncDocumentsPushSharedFlagGoesPersonal: epic:sync-publish — shared:true
// no longer dual-routes to team; both docs land in personal with a note.
func TestSyncDocumentsPushSharedFlagGoesPersonal(t *testing.T) {
	ts := newFakeDocServer(t)
	seedCred(t, ts.URL)

	repo := syncConfigRepo(t, "[sync]\ndocuments = \"personal\"\n[hosted]\nproject = \"probe\"\nworkspace = \"Acme\"\n")
	writeRepoFile(t, repo, ".satelle/documents/shared.md", "---\ntype: document\nshared: true\n---\nteam doc\n")
	writeRepoFile(t, repo, ".satelle/documents/private.md", "---\ntype: document\n---\nprivate doc\n")
	pointAt(t, repo)

	cmd, buf := testCmd()
	if err := runSyncDocumentsPush(cmd, ts.URL, "", false); err != nil {
		t.Fatalf("push: %v\n%s", err, buf.String())
	}
	if !strings.Contains(buf.String(), "satelle publish") {
		t.Fatalf("expected publish note, got: %q", buf.String())
	}

	// Personal pull gets both (sync destination is personal only).
	dst := syncConfigRepo(t, "[sync]\ndocuments = \"personal\"\n"+boundProjectToml)
	pointAt(t, dst)
	cmd2, buf2 := testCmd()
	if err := runSyncDocumentsPull(cmd2, ts.URL, ""); err != nil {
		t.Fatalf("pull: %v\n%s", err, buf2.String())
	}
	for _, name := range []string{"shared.md", "private.md"} {
		if _, err := os.Stat(filepath.Join(dst, ".satelle", "documents", name)); err != nil {
			t.Errorf("%s missing after personal pull: %v", name, err)
		}
	}
}

// TestSyncDocumentsPushAreaSharedGoesPersonal: [sync] documents = "shared"
// is not a team destination — lands personal with the deprecation note (AC1, AC3).
func TestSyncDocumentsPushAreaSharedGoesPersonal(t *testing.T) {
	ts := newFakeDocServer(t)
	seedCred(t, ts.URL)

	repo := syncConfigRepo(t, "[sync]\ndocuments = \"shared\"\n[hosted]\nproject = \"probe\"\nworkspace = \"Acme\"\n")
	writeRepoFile(t, repo, ".satelle/documents/area.md", "---\ntype: document\n---\nshared-area doc\n")
	pointAt(t, repo)

	cmd, buf := testCmd()
	if err := runSyncDocumentsPush(cmd, ts.URL, "", false); err != nil {
		t.Fatalf("push: %v\n%s", err, buf.String())
	}
	if !strings.Contains(buf.String(), "satelle publish") {
		t.Fatalf("expected publish note for area shared, got: %q", buf.String())
	}

	dst := syncConfigRepo(t, "[sync]\ndocuments = \"personal\"\n"+boundProjectToml)
	pointAt(t, dst)
	cmd2, buf2 := testCmd()
	if err := runSyncDocumentsPull(cmd2, ts.URL, ""); err != nil {
		t.Fatalf("pull: %v\n%s", err, buf2.String())
	}
	if _, err := os.Stat(filepath.Join(dst, ".satelle", "documents", "area.md")); err != nil {
		t.Errorf("area-shared doc missing from personal pull: %v", err)
	}
}

// TestSyncDocumentsPullIncremental: second pull with a cursor only fetches new
// files; a third no-op reports up to date (AC2, AC5).
func TestSyncDocumentsPullIncremental(t *testing.T) {
	ts := newFakeDocServer(t)
	seedCred(t, ts.URL)

	src := syncConfigRepo(t, "[sync]\ndocuments = \"personal\"\n"+boundProjectToml)
	writeRepoFile(t, src, ".satelle/documents/one.md", "first\n")
	pointAt(t, src)
	cmd, _ := testCmd()
	if err := runSyncDocumentsPush(cmd, ts.URL, "", false); err != nil {
		t.Fatalf("push one: %v", err)
	}

	dst := syncConfigRepo(t, "[sync]\ndocuments = \"personal\"\n"+boundProjectToml)
	pointAt(t, dst)
	cmd2, buf2 := testCmd()
	if err := runSyncDocumentsPull(cmd2, ts.URL, ""); err != nil {
		t.Fatalf("first pull: %v\n%s", err, buf2.String())
	}
	if !strings.Contains(buf2.String(), "Pulled 1") {
		t.Fatalf("first pull output: %q", buf2.String())
	}

	// Push a second file from source.
	writeRepoFile(t, src, ".satelle/documents/two.md", "second\n")
	pointAt(t, src)
	cmd3, _ := testCmd()
	if err := runSyncDocumentsPush(cmd3, ts.URL, "", false); err != nil {
		t.Fatalf("push two: %v", err)
	}

	pointAt(t, dst)
	cmd4, buf4 := testCmd()
	if err := runSyncDocumentsPull(cmd4, ts.URL, ""); err != nil {
		t.Fatalf("second pull: %v\n%s", err, buf4.String())
	}
	if !strings.Contains(buf4.String(), "Pulled 1") {
		t.Fatalf("incremental pull should land only the new file: %q", buf4.String())
	}
	if _, err := os.Stat(filepath.Join(dst, ".satelle", "documents", "two.md")); err != nil {
		t.Errorf("two.md missing after incremental pull: %v", err)
	}

	// Third pull: up to date.
	cmd5, buf5 := testCmd()
	if err := runSyncDocumentsPull(cmd5, ts.URL, ""); err != nil {
		t.Fatalf("third pull: %v", err)
	}
	if !strings.Contains(buf5.String(), "up to date") {
		t.Fatalf("expected up-to-date message, got: %q", buf5.String())
	}
}

// TestSyncDocumentsPushAuthFailure: no stored credential yields ErrLoginRequired,
// never a raw body (AC4, AC5).
func TestSyncDocumentsPushAuthFailure(t *testing.T) {
	ts := newFakeDocServer(t)
	// Deliberately do NOT seedCred — no stored tokens.

	repo := syncConfigRepo(t, "[sync]\ndocuments = \"personal\"\n"+boundProjectToml)
	writeRepoFile(t, repo, ".satelle/documents/a.md", "body")
	pointAt(t, repo)

	cmd, _ := testCmd()
	err := runSyncDocumentsPush(cmd, ts.URL, "", false)
	if err == nil {
		t.Fatal("expected auth failure")
	}
	if !strings.Contains(err.Error(), "not signed in") && !strings.Contains(err.Error(), "login") {
		t.Errorf("auth error should mention login, got: %v", err)
	}
	// Must not dump a raw HTTP body.
	if strings.Contains(err.Error(), "{") || strings.Contains(err.Error(), "HTTP 401") {
		t.Errorf("auth error leaked raw body/code: %v", err)
	}
}

// TestSyncDocumentsPushRequiresBoundProject (AC5): personal documents push without
// a bound project fails client-side; the fake server is never contacted.
func TestSyncDocumentsPushRequiresBoundProject(t *testing.T) {
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

	repo := syncConfigRepo(t, "[sync]\ndocuments = \"personal\"\n") // no project
	writeRepoFile(t, repo, ".satelle/documents/a.md", "body")
	pointAt(t, repo)

	cmd, _ := testCmd()
	err := runSyncDocumentsPush(cmd, ts.URL, "", false)
	if err == nil || !strings.Contains(err.Error(), "no hosted project bound") {
		t.Fatalf("expected unbound-project error, got %v", err)
	}
	if hits != 0 {
		t.Fatalf("unbound project contacted server %d time(s)", hits)
	}
}

// TestSyncDocumentsPullRequiresBoundProject: personal documents pull without a
// bound project fails client-side; the fake server is never contacted.
func TestSyncDocumentsPullRequiresBoundProject(t *testing.T) {
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

	repo := syncConfigRepo(t, "[sync]\ndocuments = \"personal\"\n") // no project
	pointAt(t, repo)

	cmd, _ := testCmd()
	err := runSyncDocumentsPull(cmd, ts.URL, "")
	if err == nil || !strings.Contains(err.Error(), "no hosted project bound") {
		t.Fatalf("expected unbound-project error, got %v", err)
	}
	if hits != 0 {
		t.Fatalf("unbound project contacted server %d time(s)", hits)
	}
}

// TestSyncPersonalIsolatedAcrossProjects (AC2/AC4): two repos bound to different
// projects push to the same fake server; each project's partition is exclusive.
func TestSyncPersonalIsolatedAcrossProjects(t *testing.T) {
	ts := newFakeDocServer(t)
	seedCred(t, ts.URL)

	repoA := syncConfigRepo(t, "[sync]\ndocuments = \"personal\"\n[hosted]\nproject = \"proj-a\"\n")
	writeRepoFile(t, repoA, ".satelle/documents/note.md", "from-a\n")
	pointAt(t, repoA)
	cmd, buf := testCmd()
	if err := runSyncDocumentsPush(cmd, ts.URL, "", false); err != nil {
		t.Fatalf("push A: %v\n%s", err, buf.String())
	}

	repoB := syncConfigRepo(t, "[sync]\ndocuments = \"personal\"\n[hosted]\nproject = \"proj-b\"\n")
	writeRepoFile(t, repoB, ".satelle/documents/note.md", "from-b\n")
	pointAt(t, repoB)
	cmd2, buf2 := testCmd()
	if err := runSyncDocumentsPush(cmd2, ts.URL, "", false); err != nil {
		t.Fatalf("push B: %v\n%s", err, buf2.String())
	}

	// Pull as proj-a into a fresh tree — only A's content.
	dstA := syncConfigRepo(t, "[sync]\ndocuments = \"personal\"\n[hosted]\nproject = \"proj-a\"\n")
	pointAt(t, dstA)
	cmd3, buf3 := testCmd()
	if err := runSyncDocumentsPull(cmd3, ts.URL, ""); err != nil {
		t.Fatalf("pull A: %v\n%s", err, buf3.String())
	}
	gotA, err := os.ReadFile(filepath.Join(dstA, ".satelle", "documents", "note.md"))
	if err != nil {
		t.Fatalf("read A: %v", err)
	}
	if string(gotA) != "from-a\n" {
		t.Errorf("proj-a pulled %q, want from-a", gotA)
	}

	// Pull as proj-b — only B's content (AC3/AC4).
	dstB := syncConfigRepo(t, "[sync]\ndocuments = \"personal\"\n[hosted]\nproject = \"proj-b\"\n")
	pointAt(t, dstB)
	cmd4, buf4 := testCmd()
	if err := runSyncDocumentsPull(cmd4, ts.URL, ""); err != nil {
		t.Fatalf("pull B: %v\n%s", err, buf4.String())
	}
	gotB, err := os.ReadFile(filepath.Join(dstB, ".satelle", "documents", "note.md"))
	if err != nil {
		t.Fatalf("read B: %v", err)
	}
	if string(gotB) != "from-b\n" {
		t.Errorf("proj-b pulled %q, want from-b", gotB)
	}
}

// TestSyncDocumentsPullReadsOnlyBoundProject (AC3): seed only under proj-a; a
// repo bound to proj-b pulls nothing.
func TestSyncDocumentsPullReadsOnlyBoundProject(t *testing.T) {
	ts := newFakeDocServer(t)
	seedCred(t, ts.URL)

	src := syncConfigRepo(t, "[sync]\ndocuments = \"personal\"\n[hosted]\nproject = \"proj-a\"\n")
	writeRepoFile(t, src, ".satelle/documents/only-a.md", "secret-a\n")
	pointAt(t, src)
	cmd, _ := testCmd()
	if err := runSyncDocumentsPush(cmd, ts.URL, "", false); err != nil {
		t.Fatalf("seed push: %v", err)
	}

	dst := syncConfigRepo(t, "[sync]\ndocuments = \"personal\"\n[hosted]\nproject = \"proj-b\"\n")
	pointAt(t, dst)
	cmd2, buf2 := testCmd()
	if err := runSyncDocumentsPull(cmd2, ts.URL, ""); err != nil {
		t.Fatalf("pull B: %v\n%s", err, buf2.String())
	}
	if _, err := os.Stat(filepath.Join(dst, ".satelle", "documents", "only-a.md")); err == nil {
		t.Error("proj-b must not receive proj-a's documents")
	}
	if !strings.Contains(buf2.String(), "up to date") && !strings.Contains(buf2.String(), "Pulled 0") {
		// empty pull reports up to date
		if !strings.Contains(buf2.String(), "up to date") {
			t.Fatalf("expected empty/up-to-date pull for proj-b, got: %q", buf2.String())
		}
	}
}
