package cli

import (
	"crypto/sha256"
	"encoding/hex"
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
)

// fakeConfigStore is an in-memory versioned config store matching the server's
// per-file, workspace-scoped contract — so push->deploy round-trips through a
// realistic surface in tests.
type fakeConfigStore struct {
	mu   sync.Mutex
	data map[string]map[string][][]byte // wsID -> path -> versions (index 0 = v1)
}

func (s *fakeConfigStore) put(wsID, path string, content []byte) (sha string, version int, created bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ws := s.data[wsID]
	if ws == nil {
		ws = map[string][][]byte{}
		s.data[wsID] = ws
	}
	versions := ws[path]
	if len(versions) > 0 {
		head := versions[len(versions)-1]
		if string(head) == string(content) {
			return sha256hex(head), len(versions), false // idempotent re-push of head
		}
	}
	ws[path] = append(versions, content)
	version = len(ws[path])
	return sha256hex(content), version, true
}

func (s *fakeConfigStore) get(wsID, path string, version int) (content []byte, sha string, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ws := s.data[wsID]
	if ws == nil {
		return nil, "", false
	}
	versions := ws[path]
	if version <= 0 {
		if len(versions) == 0 {
			return nil, "", false
		}
		c := versions[len(versions)-1]
		return c, sha256hex(c), true
	}
	if version > len(versions) {
		return nil, "", false
	}
	c := versions[version-1]
	return c, sha256hex(c), true
}

func (s *fakeConfigStore) manifest(wsID string) []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	ws := s.data[wsID]
	out := []map[string]any{}
	for path, versions := range ws {
		head := versions[len(versions)-1]
		out = append(out, map[string]any{
			"path": path, "version": len(versions),
			"blob_sha256": sha256hex(head), "size": len(head), "created_at": "t",
		})
	}
	return out
}

func sha256hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// newFakeConfigServer stands up a hosted server whose workspaces are a personal
// "personal" (id ws-personal) and a team "Acme" (id ws-team), with the full
// per-file config PUT/GET surface. The personal sentinel resolves to ws-personal
// (kind=personal); the team name to ws-team.
func newFakeConfigServer(t *testing.T) *httptest.Server {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	store := &fakeConfigStore{data: map[string]map[string][][]byte{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/workspaces", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]string{
			{"id": "ws-personal", "kind": "personal", "name": "personal"},
			{"id": "ws-team", "kind": "team", "name": "Acme"},
		})
	})
	mux.HandleFunc("/api/v1/workspaces/", func(w http.ResponseWriter, r *http.Request) {
		rest := strings.TrimPrefix(r.URL.Path, "/api/v1/workspaces/")
		segs := strings.SplitN(rest, "/", 3) // [wsID, "config", path?]
		if len(segs) < 2 || segs[1] != "config" {
			http.NotFound(w, r)
			return
		}
		wsID := segs[0]
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
			_ = json.NewEncoder(w).Encode(store.manifest(wsID))
		case r.Method == http.MethodGet:
			ver := 0
			if v := r.URL.Query().Get("version"); v != "" {
				ver, _ = strconv.Atoi(v)
			}
			content, sha, ok := store.get(wsID, path, ver)
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

// syncConfigRepo scaffolds a temp repo with the given satelle.toml body (plus the
// agents layer a store-backed command would need) and returns its dir. Does NOT
// touch SATELLE_CONFIG — call pointAt to switch the active repo.
func syncConfigRepo(t *testing.T, satelleToml string) string {
	t.Helper()
	repo := t.TempDir()
	dir := filepath.Join(repo, ".satelle")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "satelle.toml"), []byte(satelleToml), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "agents.toml"), []byte("[executor]\nharness = \"in-loop\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return repo
}

func pointAt(t *testing.T, repo string) {
	t.Helper()
	t.Setenv("SATELLE_CONFIG", filepath.Join(repo, ".satelle", "satelle.toml"))
}

func writeRepoFile(t *testing.T, repo, rel, body string) {
	t.Helper()
	dest := filepath.Join(repo, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestSyncConfigPushDeploysByteExact: push authored config from one repo, then
// deploy it into a FRESH repo — "set up project X like project Y" — and the
// bytes match exactly (AC2, AC5 push/deploy/pull).
func TestSyncConfigPushDeploysByteExact(t *testing.T) {
	ts := newFakeConfigServer(t)
	seedCred(t, ts.URL)

	// Source repo: shared-scope skills + constitution, active workspace = team.
	src := syncConfigRepo(t, "[sync]\nskills = \"shared\"\nconstitution = \"shared\"\n[hosted]\nworkspace = \"Acme\"\n")
	skillBody := "---\ntype: skill\n---\nshared team skill\n"
	constBody := "# project constitution\n"
	writeRepoFile(t, src, ".satelle/skills/team-skill.md", skillBody)
	writeRepoFile(t, src, ".satelle/constitution.md", constBody)
	pointAt(t, src)

	cmd, buf := testCmd()
	if err := runSyncConfigPush(cmd, ts.URL, "", false); err != nil {
		t.Fatalf("push: %v\n%s", err, buf.String())
	}
	if !strings.Contains(buf.String(), "Pushed") || !strings.Contains(buf.String(), "2 new") {
		t.Fatalf("push output: %q", buf.String())
	}

	// Fresh destination repo: no config files yet, no [sync] needed for deploy.
	dst := syncConfigRepo(t, "")
	pointAt(t, dst)

	cmd2, buf2 := testCmd()
	if err := runSyncConfigDeploy(cmd2, ts.URL, "Acme", 0); err != nil {
		t.Fatalf("deploy: %v\n%s", err, buf2.String())
	}
	if !strings.Contains(buf2.String(), "Deployed 2 file") {
		t.Fatalf("deploy output: %q", buf2.String())
	}
	gotSkill, err := os.ReadFile(filepath.Join(dst, ".satelle", "skills", "team-skill.md"))
	if err != nil {
		t.Fatalf("read deployed skill: %v", err)
	}
	if string(gotSkill) != skillBody {
		t.Errorf("deployed skill = %q, want %q (byte-exact)", gotSkill, skillBody)
	}
	gotConst, _ := os.ReadFile(filepath.Join(dst, ".satelle", "constitution.md"))
	if string(gotConst) != constBody {
		t.Errorf("deployed constitution = %q, want %q", gotConst, constBody)
	}
}

// TestSyncConfigPushSkipsLocalArea: a local-scope (default) area is never pushed
// — the server's team workspace stays empty for it (AC1, AC5 scope=local skip).
func TestSyncConfigPushSkipsLocalArea(t *testing.T) {
	ts := newFakeConfigServer(t)
	seedCred(t, ts.URL)

	repo := syncConfigRepo(t, "")
	writeRepoFile(t, repo, ".satelle/skills/local-only.md", "should not push")
	pointAt(t, repo)

	cmd, buf := testCmd()
	if err := runSyncConfigPush(cmd, ts.URL, "Acme", false); err != nil {
		t.Fatalf("push: %v", err)
	}
	if !strings.Contains(buf.String(), "No config to push") {
		t.Fatalf("expected no-config message for all-local, got: %q", buf.String())
	}
}

// TestSyncConfigPushSharedFlagRoutesToTeam: inside a personal-scope area, a file
// frontmarked shared routes to the TEAM workspace, while a non-shared file routes
// to the PERSONAL workspace (AC1, AC5 honors the per-file shared flag).
func TestSyncConfigPushSharedFlagRoutesToTeam(t *testing.T) {
	ts := newFakeConfigServer(t)
	seedCred(t, ts.URL)

	repo := syncConfigRepo(t, "[sync]\nskills = \"personal\"\n[hosted]\nworkspace = \"Acme\"\n")
	writeRepoFile(t, repo, ".satelle/skills/shared-one.md", "---\ntype: skill\nshared: true\n---\nshared body\n")
	writeRepoFile(t, repo, ".satelle/skills/private-one.md", "---\ntype: skill\n---\nprivate body\n")
	pointAt(t, repo)

	cmd, buf := testCmd()
	if err := runSyncConfigPush(cmd, ts.URL, "", false); err != nil {
		t.Fatalf("push: %v\n%s", err, buf.String())
	}
	// Inspect the fake server's per-workspace state via deploy probes: the
	// shared-flagged file lives in the team workspace, the private one personal.

	// Deploy from the TEAM workspace -> only the shared-flagged file is present.
	dst := syncConfigRepo(t, "")
	pointAt(t, dst)
	cmd2, buf2 := testCmd()
	if err := runSyncConfigDeploy(cmd2, ts.URL, "Acme", 0); err != nil {
		t.Fatalf("team deploy: %v\n%s", err, buf2.String())
	}
	if _, err := os.Stat(filepath.Join(dst, ".satelle", "skills", "shared-one.md")); err != nil {
		t.Errorf("shared-flagged file missing from team workspace: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, ".satelle", "skills", "private-one.md")); err == nil {
		t.Error("private file leaked into the team workspace (should be personal-only)")
	}

	// Deploy from the PERSONAL workspace -> only the private file is present.
	dst2 := syncConfigRepo(t, "")
	pointAt(t, dst2)
	cmd3, buf3 := testCmd()
	if err := runSyncConfigDeploy(cmd3, ts.URL, "personal", 0); err != nil {
		t.Fatalf("personal deploy: %v\n%s", err, buf3.String())
	}
	got, _ := os.ReadFile(filepath.Join(dst2, ".satelle", "skills", "private-one.md"))
	if !strings.Contains(string(got), "private body") {
		t.Errorf("personal workspace missing private file: %q", got)
	}
}

// TestSyncConfigPushIdempotent: a second identical push reports unchanged
// (Created=false), appending no new version (AC1 identical content is idempotent).
func TestSyncConfigPushIdempotent(t *testing.T) {
	ts := newFakeConfigServer(t)
	seedCred(t, ts.URL)

	repo := syncConfigRepo(t, "[sync]\nprinciples = \"shared\"\n[hosted]\nworkspace = \"Acme\"\n")
	writeRepoFile(t, repo, ".satelle/principles/rule.md", "one rule\n")
	pointAt(t, repo)

	cmd, buf := testCmd()
	if err := runSyncConfigPush(cmd, ts.URL, "", false); err != nil {
		t.Fatalf("first push: %v", err)
	}
	if !strings.Contains(buf.String(), "1 new") {
		t.Fatalf("first push should report 1 new: %q", buf.String())
	}
	cmd2, buf2 := testCmd()
	if err := runSyncConfigPush(cmd2, ts.URL, "", false); err != nil {
		t.Fatalf("second push: %v", err)
	}
	if !strings.Contains(buf2.String(), "1 unchanged") {
		t.Fatalf("idempotent re-push should report unchanged: %q", buf2.String())
	}
}

// TestSyncConfigDeployVersionPin: push two versions of a file, then deploy
// --version 1 lands the ORIGINAL bytes (AC2, AC5 version-pin).
func TestSyncConfigDeployVersionPin(t *testing.T) {
	ts := newFakeConfigServer(t)
	seedCred(t, ts.URL)

	repo := syncConfigRepo(t, "[sync]\nprinciples = \"shared\"\n[hosted]\nworkspace = \"Acme\"\n")
	writeRepoFile(t, repo, ".satelle/principles/rule.md", "version one\n")
	pointAt(t, repo)
	cmd, _ := testCmd()
	if err := runSyncConfigPush(cmd, ts.URL, "", false); err != nil {
		t.Fatalf("push v1: %v", err)
	}
	// Change the file and push again -> version 2.
	writeRepoFile(t, repo, ".satelle/principles/rule.md", "version two\n")
	cmd2, _ := testCmd()
	if err := runSyncConfigPush(cmd2, ts.URL, "", false); err != nil {
		t.Fatalf("push v2: %v", err)
	}

	// Deploy version 1 into a fresh repo -> the ORIGINAL bytes.
	dst := syncConfigRepo(t, "")
	pointAt(t, dst)
	cmd3, buf3 := testCmd()
	if err := runSyncConfigDeploy(cmd3, ts.URL, "Acme", 1); err != nil {
		t.Fatalf("deploy v1: %v\n%s", err, buf3.String())
	}
	got, _ := os.ReadFile(filepath.Join(dst, ".satelle", "principles", "rule.md"))
	if string(got) != "version one\n" {
		t.Errorf("pinned deploy = %q, want %q", got, "version one\n")
	}
}

// TestSyncConfigPushDryRun: --dry-run lists the plan with no server contact.
func TestSyncConfigPushDryRun(t *testing.T) {
	ts := newFakeConfigServer(t)
	seedCred(t, ts.URL)

	repo := syncConfigRepo(t, "[sync]\nskills = \"personal\"\n[hosted]\nworkspace = \"Acme\"\n")
	writeRepoFile(t, repo, ".satelle/skills/shared-one.md", "---\nshared: true\n---\nx\n")
	writeRepoFile(t, repo, ".satelle/skills/private-one.md", "---\n---\nx\n")
	pointAt(t, repo)

	cmd, buf := testCmd()
	if err := runSyncConfigPush(cmd, ts.URL, "", true); err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Would push") {
		t.Errorf("dry-run output: %q", out)
	}
	// The shared-flagged file routes to the team workspace in the plan.
	if !strings.Contains(out, "shared-one.md") || !strings.Contains(out, "Acme") {
		t.Errorf("dry-run should name the shared file and team workspace: %q", out)
	}
}
