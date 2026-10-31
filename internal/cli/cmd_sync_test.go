package cli

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/hosted/syncpb"
	"google.golang.org/grpc"
)

// areaScopes parses `sync scopes` tabwriter output into area -> scope,
// ignoring indented "shared: <path>" detail lines.
func areaScopes(out string) map[string]string {
	scopes := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 2 {
			scopes[fields[0]] = fields[1]
		}
	}
	return scopes
}

// TestSyncScopesDefaultsToLocal: with no [sync] table, every area prints local
// — the safe default, nothing syncs without opt-in (AC1).
func TestSyncScopesDefaultsToLocal(t *testing.T) {
	tempRepo(t)
	out, err := runRoot(t, "sync", "scopes")
	if err != nil {
		t.Fatalf("sync scopes: %v\n%s", err, out)
	}
	scopes := areaScopes(out)
	for _, area := range []string{"documents", "workflows", "principles", "skills", "constitution", "agents", "tasks", "stories", "ledger", "executions"} {
		if scopes[area] != "local" {
			t.Errorf("area %q scope = %q, want local (full output:\n%s)", area, scopes[area], out)
		}
	}
}

// TestSyncScopesFollowTheResolvedScope: every area takes the one scope the
// account resolves for the bound project, with no file edit between the runs.
func TestSyncScopesFollowTheResolvedScope(t *testing.T) {
	repo := tempRepo(t)
	signInTo(t, config.DefaultHostedServer)
	writeRepoFile(t, repo, ".satelle/satelle.toml", "[sync]\nproject = \"probe\"\n")
	for _, c := range []struct{ kind, want string }{
		{kindPersonal, "personal"}, {kindTeam, "team"}, {kindForeign, "local"},
	} {
		useScope(t, c.kind)
		out, err := runRoot(t, "sync", "scopes")
		if err != nil {
			t.Fatalf("sync scopes: %v\n%s", err, out)
		}
		if !strings.Contains(out, "scope: "+c.want) {
			t.Errorf("%s project: output does not name scope %s:\n%s", c.kind, c.want, out)
		}
		for area, got := range areaScopes(out) {
			if got != c.want {
				t.Errorf("%s project: area %q scope = %q, want %s", c.kind, area, got, c.want)
			}
		}
	}
}

// TestSyncScopesRefusesARemovedOverlayKey: satelle.local.toml cannot carry a
// per-area scope either; the refusal names the key and the heal.
func TestSyncScopesRefusesARemovedOverlayKey(t *testing.T) {
	repo := tempRepo(t)
	writeRepoFile(t, repo, ".satelle/satelle.local.toml", "[sync]\ndocuments = \"local\"\n")
	out, err := runRoot(t, "sync", "scopes")
	if err == nil || !strings.Contains(err.Error(), "[sync] documents is removed") || !strings.Contains(err.Error(), "satelle init") {
		t.Fatalf("want the removed-key refusal naming satelle init, got %v\n%s", err, out)
	}
}

// TestSyncDefaultAllLocalNothingToSync: bare `satelle sync` with no [sync] table
// prints one "nothing to sync" line and contacts no server (default-action AC2).
func TestSyncDefaultAllLocalNothingToSync(t *testing.T) {
	tempRepo(t)
	out, err := runRoot(t, "sync")
	if err != nil {
		t.Fatalf("sync: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Nothing to sync") {
		t.Fatalf("expected all-local nothing-to-sync message, got: %q", out)
	}
	// No per-verb skip noise leaks through the aggregate path.
	if strings.Contains(out, "scope is local — skipping") || strings.Contains(out, "every work-state area is local") {
		t.Errorf("all-local sync leaked a per-verb skip message:\n%s", out)
	}
}

// TestSyncDefaultDryRunRunsOptedInAreasInOrder: bare `satelle sync --dry-run`
// composes the opted-in verbs — config push, documents push, then work-state
// push — previewing each without a network call, and reports documents pull as
// not previewable (default-action AC1, AC4). --dry-run keeps every handler
// short of contacting the server, so no fake server is needed.
func TestSyncDefaultDryRunRunsOptedInAreasInOrder(t *testing.T) {
	repo := tempRepo(t)
	signInTo(t, "http://sync.example.invalid")
	portableAgents(t, repo)
	cfgPath := filepath.Join(repo, ".satelle", "satelle.toml")
	cfg := "[sync]\nproject = \"probe\"\n"
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	skillsDir := filepath.Join(repo, ".satelle", "skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillsDir, "my-skill.md"), []byte("---\ntype: skill\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	docsDir := filepath.Join(repo, ".satelle", "documents")
	if err := os.MkdirAll(docsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(docsDir, "doc.md"), []byte("# Doc\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := runRoot(t, "sync", "--dry-run", "--server", "http://sync.example.invalid")
	if err != nil {
		t.Fatalf("sync --dry-run: %v\n%s", err, out)
	}
	// Every opted-in kind is previewed, and pull is reported as not previewable.
	needles := []string{"skills/my-skill.md", "documents/doc.md", "documents pull: skipped under --dry-run", "work-state areas"}
	for _, n := range needles {
		if !strings.Contains(out, n) {
			t.Errorf("sync --dry-run output missing %q:\n%s", n, out)
		}
	}
	// Fixed composition order: config push → documents push → pull note → workstate push.
	iConfig := strings.Index(out, "skills/my-skill.md")
	iDoc := strings.Index(out, "documents/doc.md")
	iPull := strings.Index(out, "documents pull: skipped")
	iWork := strings.Index(out, "work-state areas")
	if !(iConfig < iDoc && iDoc < iPull && iPull < iWork) {
		t.Errorf("sync verbs ran out of order (config=%d doc=%d pull=%d work=%d):\n%s", iConfig, iDoc, iPull, iWork, out)
	}
}

// fakeSyncServer is a combined hosted server exposing the config, documents, and
// workstate surfaces at once, so bare `satelle sync` can drive all four verbs
// against a single endpoint. It reuses the per-kind fake stores.
type fakeSyncServer struct {
	docs      *fakeDocStore
	cfg       *fakeConfigStore
	mu        sync.Mutex
	workstate int
}

func newFakeSyncServer(t *testing.T) (*httptest.Server, *fakeSyncServer) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	hosted.DocumentSyncStatePathOverride = "" // isolate the pull cursor to this XDG dir
	f := &fakeSyncServer{docs: newFakeDocStore(), cfg: &fakeConfigStore{data: map[string]map[string][][]byte{}}}
	mux := http.NewServeMux()
	// Project-addressed config/documents (sty_ca64d0cb). Store key is
	// the project id/slug alone. Work-state is gRPC Sync (attachTestGRPC).
	mux.HandleFunc("/api/v1/projects/", func(w http.ResponseWriter, r *http.Request) {
		segs := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/api/v1/projects/"), "/", 3) // [project, kind, path?]
		if len(segs) < 2 {
			http.NotFound(w, r)
			return
		}
		project := segs[0]
		path := ""
		if len(segs) == 3 {
			path = segs[2]
		}
		writePut := func(store interface {
			put(string, string, []byte) (string, int, bool)
		}) {
			body, _ := io.ReadAll(r.Body)
			sha, ver, created := store.put(project, path, body)
			status := http.StatusOK
			if created {
				status = http.StatusCreated
			}
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(map[string]any{"path": path, "version": ver, "blob_sha256": sha, "size": len(body), "created": created})
		}
		switch segs[1] {
		case "config":
			switch {
			case r.Method == http.MethodPut:
				writePut(f.cfg)
			case r.Method == http.MethodGet && path == "":
				_ = json.NewEncoder(w).Encode(f.cfg.manifest(project))
			case r.Method == http.MethodGet:
				content, sha, ok := f.cfg.get(project, path, 0)
				if !ok {
					http.NotFound(w, r)
					return
				}
				w.Header().Set("ETag", `"`+sha+`"`)
				_, _ = w.Write(content)
			default:
				http.NotFound(w, r)
			}
		case "documents":
			switch {
			case r.Method == http.MethodPut:
				writePut(f.docs)
			case r.Method == http.MethodGet && path == "":
				items, cursor := f.docs.changes(project, r.URL.Query().Get("since"))
				_ = json.NewEncoder(w).Encode(map[string]any{"items": items, "cursor": cursor})
			case r.Method == http.MethodGet:
				content, sha, ok := f.docs.get(project, path)
				if !ok {
					http.NotFound(w, r)
					return
				}
				w.Header().Set("ETag", `"`+sha+`"`)
				_, _ = w.Write(content)
			default:
				http.NotFound(w, r)
			}
		default:
			http.NotFound(w, r)
		}
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	// A non-local scope publishes story events too, so the server takes them.
	chain := newFakeChain(&fakeWorkstateServer{posts: map[string][]map[string]any{}, itemsByID: map[string]map[string]any{}, ledgerByID: map[string]map[string]any{}})
	attachTestGRPC(t, &fakeSyncGRPC{f: f}, func(gs *grpc.Server) { syncpb.RegisterChainServer(gs, chain) })
	return ts, f
}

type fakeSyncGRPC struct {
	syncpb.UnimplementedSyncServer
	f *fakeSyncServer
}

func (s *fakeSyncGRPC) Apply(_ context.Context, req *syncpb.ApplyRequest) (*syncpb.ApplyResponse, error) {
	s.f.mu.Lock()
	s.f.workstate++
	s.f.mu.Unlock()
	return &syncpb.ApplyResponse{Items: int32(len(req.GetItems())), Ledger: int32(len(req.GetLedger()))}, nil
}

type rehydrateSnapGRPC struct {
	syncpb.UnimplementedSyncServer
	record []byte
}

func (s *rehydrateSnapGRPC) Snapshot(_ context.Context, _ *syncpb.SnapshotRequest) (*syncpb.SnapshotResponse, error) {
	return &syncpb.SnapshotResponse{Items: []*syncpb.WorkItem{{
		Id: "sty_rehy1", Kind: "story", Type: "stories", Status: "backlog",
		Title: "Rehydrated", Origin: "cli-sync", Record: s.record,
	}}}, nil
}

// TestSyncDefaultRunsFullBundleLive drives bare `satelle sync` (non-dry-run)
// through every opted-in verb against one combined server and asserts each
// actually executed: config push, documents push, documents PULL (a server-only
// file must land locally — the branch a preview test can't pin), and work-state
// push (default-action AC1).
func TestSyncDefaultRunsFullBundleLive(t *testing.T) {
	ts, f := newFakeSyncServer(t)
	seedCred(t, ts.URL)

	repo := syncConfigRepo(t, "[sync]\n"+boundProjectToml)
	writeRepoFile(t, repo, ".satelle/skills/my-skill.md", "---\ntype: skill\n---\nbody\n")
	writeRepoFile(t, repo, ".satelle/documents/local-doc.md", "# local\n")
	pointAt(t, repo)

	// Pre-seed a document ONLY on the server so documents pull must run to land it.
	f.docs.put("probe", "documents/remote-only.md", []byte("# remote\n"))

	// A story so work-state push has a row to send.
	if out, err := runRoot(t, "story", "create", "--title", "Bundle probe", "--body", "x", "--acceptance", "1. ok"); err != nil {
		t.Fatalf("story create: %v\n%s", err, out)
	}

	// --dry-run=false is explicit: runRoot reuses the process-global sync command
	// instance, so a prior test's --dry-run would otherwise persist (a real one-shot
	// CLI process never sees this).
	out, err := runRoot(t, "sync", "--server", ts.URL, "--dry-run=false")
	if err != nil {
		t.Fatalf("sync: %v\n%s", err, out)
	}

	// Documents PULL ran — the server-only file is now on disk (the gap a
	// dry-run/preview test cannot cover).
	if _, err := os.Stat(filepath.Join(repo, ".satelle", "documents", "remote-only.md")); err != nil {
		t.Fatalf("bare sync did not run documents pull — remote-only.md absent locally: %v\noutput:\n%s", err, out)
	}
	// Config push reached the server.
	if _, _, ok := f.cfg.get("probe", "skills/my-skill.md", 0); !ok {
		t.Errorf("config push did not reach the server\noutput:\n%s", out)
	}
	// Documents push reached the server.
	if _, _, ok := f.docs.get("probe", "documents/local-doc.md"); !ok {
		t.Errorf("documents push did not reach the server\noutput:\n%s", out)
	}
	// Work-state push reached the server.
	f.mu.Lock()
	wsPosts := f.workstate
	f.mu.Unlock()
	if wsPosts == 0 {
		t.Errorf("work-state push did not reach the server\noutput:\n%s", out)
	}
}

// TestSyncPoisonedPartitionDoesNotWedgeWorkstate (sty_84f14ace AC7): a documents
// partition that already contains backups/ entries must not abort bare sync —
// the pull skips them, the cursor advances, and the workstate leg still runs.
func TestSyncPoisonedPartitionDoesNotWedgeWorkstate(t *testing.T) {
	ts, f := newFakeSyncServer(t)
	seedCred(t, ts.URL)

	repo := syncConfigRepo(t, "[sync]\n"+boundProjectToml)
	writeRepoFile(t, repo, ".satelle/skills/my-skill.md", "---\ntype: skill\n---\nbody\n")
	writeRepoFile(t, repo, ".satelle/documents/local-doc.md", "# local\n")
	pointAt(t, repo)

	// Poison the documents partition the way a pre-fix init hosted backup would.
	f.docs.put("probe", "backups/pre-mutation/skills/old.md", []byte("poison"))
	// Plus a remote-only legitimate document so the pull has real work.
	f.docs.put("probe", "documents/remote-only.md", []byte("# remote\n"))

	if out, err := runRoot(t, "story", "create", "--title", "Poison probe", "--body", "x", "--acceptance", "1. ok"); err != nil {
		t.Fatalf("story create: %v\n%s", err, out)
	}

	out, err := runRoot(t, "sync", "--server", ts.URL, "--dry-run=false")
	if err != nil {
		t.Fatalf("sync must exit 0 on poisoned partition: %v\n%s", err, out)
	}
	// AC3 (sty_0fd04503): hard-assert skip visibility — a silent-swallow
	// regression must fail. Mixed poison+legit lands in the "…, N skipped
	// (local-only path)" arm so both substrings are present.
	if !strings.Contains(out, "skipped") || !strings.Contains(out, "local-only") {
		t.Fatalf("poisoned pull must report the skip in output:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(repo, ".satelle", "documents", "remote-only.md")); err != nil {
		t.Fatalf("legitimate document not pulled: %v\noutput:\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(repo, ".satelle", "backups", "pre-mutation", "skills", "old.md")); err == nil {
		t.Error("poison backups/ path must not be written locally")
	}
	f.mu.Lock()
	wsPosts := f.workstate
	f.mu.Unlock()
	if wsPosts == 0 {
		t.Errorf("work-state leg did not run (still wedged by documents pull)\noutput:\n%s", out)
	}
}

// TestSyncScopesInvalidScopeRefuses: a per-area key refuses the command whatever
// its value, rather than silently coercing to local.
func TestSyncScopesInvalidScopeRefuses(t *testing.T) {
	repo := tempRepo(t)
	cfgPath := filepath.Join(repo, ".satelle", "satelle.toml")
	committed := "[sync]\ntasks = \"sometimes\"\n"
	if err := os.WriteFile(cfgPath, []byte(committed), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runRoot(t, "sync", "scopes"); err == nil {
		t.Error("sync scopes with an invalid configured scope did not error")
	}
}

// TestSyncScopesListsSharedFiles: under a personal scope a file with
// `shared: true` frontmatter is listed by the command (AC2, AC4).
func TestSyncScopesListsSharedFiles(t *testing.T) {
	repo := tempRepo(t)
	signInTo(t, config.DefaultHostedServer)
	cfgPath := filepath.Join(repo, ".satelle", "satelle.toml")
	if err := os.WriteFile(cfgPath, []byte("[sync]\nproject = \"probe\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	skillsDir := filepath.Join(repo, ".satelle", "skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	shared := "---\ntype: skill\nshared: true\n---\nbody\n"
	unshared := "---\ntype: skill\n---\nbody\n"
	if err := os.WriteFile(filepath.Join(skillsDir, "shared-one.md"), []byte(shared), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillsDir, "private-one.md"), []byte(unshared), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := runRoot(t, "sync", "scopes")
	if err != nil {
		t.Fatalf("sync scopes: %v\n%s", err, out)
	}
	if !strings.Contains(out, "shared-one.md") {
		t.Errorf("sync scopes should list shared-one.md as shared:\n%s", out)
	}
	if strings.Contains(out, "private-one.md") {
		t.Errorf("sync scopes should NOT list private-one.md:\n%s", out)
	}
}

// TestSyncRehydrateLocalOnly: when the account resolves the bound project local
// (not a member of its team), rehydrate runs each step as a local skip / empty
// deploy and exits 0 (order:4 AC4).
func TestSyncRehydrateLocalOnly(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	repo := tempRepo(t)
	useScope(t, kindForeign)
	// Bound project so deploy can resolve; fake server returns empty manifest.
	cfgPath := filepath.Join(repo, ".satelle", "satelle.toml")
	if err := os.WriteFile(cfgPath, []byte("[hosted]\nproject = \"probe\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/workspaces", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]string{
			{"id": "ws-personal", "kind": "personal", "name": "personal"},
		})
	})
	mux.HandleFunc("GET /api/v1/projects/{project}/config", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]any{})
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	seedCred(t, ts.URL)

	out, err := runRoot(t, "sync", "rehydrate", "--server", ts.URL)
	if err != nil {
		t.Fatalf("rehydrate: %v\n%s", err, out)
	}
	for _, want := range []string{"rehydrate: config deploy", "rehydrate: documents pull", "rehydrate: workstate pull", "rehydrate: done"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "scope: local — "+testAccountEmail+" is not a member of project probe's team") {
		t.Errorf("expected the resolver's local reason:\n%s", out)
	}
	if !strings.Contains(out, "settings: not restored") {
		t.Errorf("expected truthful done summary:\n%s", out)
	}
}

// rehydrateEmptyTree binds a bare tree to project probe and rehydrates it from a
// server whose settings snapshot is hostedToml, returning rehydrate's output and
// the satelle.toml it left. The story the server holds must be restored.
func rehydrateEmptyTree(t *testing.T, hostedToml string) (out, gotToml string) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("SATELLE_HOME", t.TempDir()) // home-keyed runtime (sty_c36c211f)
	// Bare tree: no satelle.toml until bind.
	repo := t.TempDir()
	t.Chdir(repo)
	_ = os.Unsetenv("SATELLE_CONFIG")

	// Seed agents so store open after deploy succeeds if deploy writes agents.
	// Fake server serves config (satelle.toml with personal scopes + agents),
	// empty docs changes, and workstate items/ledger from a prior push shape.
	const project = "probe"
	agentsToml := "[executor]\nharness = \"in-loop\"\n"
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/workspaces", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]string{
			{"id": "ws-personal", "kind": "personal", "name": "personal"},
		})
	})
	mux.HandleFunc("GET /api/v1/projects/{project}/config", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"path": "satelle.toml", "version": 1},
			{"path": "agents.toml", "version": 1},
		})
	})
	mux.HandleFunc("GET /api/v1/projects/{project}/config/{path...}", func(w http.ResponseWriter, r *http.Request) {
		p := r.PathValue("path")
		switch p {
		case "satelle.toml":
			w.Header().Set("ETag", "sha-toml")
			_, _ = w.Write([]byte(hostedToml))
		case "agents.toml":
			w.Header().Set("ETag", "sha-agents")
			_, _ = w.Write([]byte(agentsToml))
		default:
			http.NotFound(w, r)
		}
	})
	mux.HandleFunc("GET /api/v1/projects/{project}/documents", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{}, "cursor": ""})
	})
	itemRec := map[string]any{
		"id": "sty_rehy1", "kind": "story", "status": "backlog", "title": "Rehydrated",
		"body": "from hosted", "acceptance_criteria": "1. ok",
	}
	recBytes, _ := json.Marshal(itemRec)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	attachTestGRPC(t, &rehydrateSnapGRPC{record: recBytes})
	seedCred(t, ts.URL)

	// Bind creates minimal toml.
	cmd, buf := testCmd()
	if err := runProjectBind(cmd, project); err != nil {
		t.Fatalf("bind: %v\n%s", err, buf.String())
	}
	// Point config at the bound file for subsequent CLI.
	t.Setenv("SATELLE_CONFIG", filepath.Join(repo, ".satelle", "satelle.toml"))

	out, err := runRoot(t, "sync", "rehydrate", "--server", ts.URL)
	if err != nil {
		t.Fatalf("rehydrate: %v\n%s", err, out)
	}
	for _, want := range []string{"rehydrate: config deploy", "rehydrate: documents pull", "rehydrate: workstate pull", "rehydrate: done"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	// AC6 (sty_2cb4e1f6): rehydrate's work-state pull names its pass; this server
	// does not page, so it is a single page and issues no token.
	if !strings.Contains(out, "(full pass, 1 page(s))") {
		t.Errorf("rehydrate's pull should name its pass kind and page count:\n%s", out)
	}
	// The settings snapshot is restored; the scope is the account's.
	if !strings.Contains(out, "settings: satelle.toml restored") {
		t.Errorf("expected settings restored line:\n%s", out)
	}
	if !strings.Contains(out, "[sync] project preserved") {
		t.Errorf("expected binding preservation line:\n%s", out)
	}
	if !strings.Contains(out, "rehydrate: done — scope: personal") {
		t.Errorf("rehydrate's closing line must name the account's scope:\n%s", out)
	}
	// On-disk satelle.toml preserves the bind project.
	gotBytes, rerr := os.ReadFile(filepath.Join(repo, ".satelle", "satelle.toml"))
	if rerr != nil {
		t.Fatalf("read satelle.toml: %v", rerr)
	}
	if !strings.Contains(string(gotBytes), `project = "probe"`) {
		t.Errorf("local binding not preserved: %s", gotBytes)
	}
	// Story materialised.
	got, gerr := runRoot(t, "story", "get", "sty_rehy1")
	if gerr != nil {
		t.Fatalf("story get after rehydrate: %v\n%s\nrehydrate out:\n%s", gerr, got, out)
	}
	if !strings.Contains(got, "Rehydrated") {
		t.Errorf("story not restored: %s", got)
	}
	return out, string(gotBytes)
}

// TestSyncRehydrateEmptyTreeHappyPath: bind-created toml + deploy restores the
// settings snapshot, the documents path and the workstate items (order:4 AC5
// hermetic).
func TestSyncRehydrateEmptyTreeHappyPath(t *testing.T) {
	_, gotToml := rehydrateEmptyTree(t, "[sync]\nstale_after = \"6h\"\n\n[hosted]\nproject = \"probe\"\n")
	if !strings.Contains(gotToml, `stale_after = "6h"`) {
		t.Errorf("settings snapshot not restored: %s", gotToml)
	}
}

// TestSyncRehydrateIgnoresHostedSyncScope (AC6): a hosted satelle.toml whose
// [sync] block still carries scope keys — here saying local — decides nothing.
// The account resolves personal, so the work state is still restored; the keys
// are dropped from the restored file and named, and the file loads.
func TestSyncRehydrateIgnoresHostedSyncScope(t *testing.T) {
	out, gotToml := rehydrateEmptyTree(t, "[sync]\nall = \"local\"\nstories = \"local\"\nstale_after = \"6h\"\n\n[hosted]\nproject = \"probe\"\n")
	if strings.Contains(gotToml, `all = `) || strings.Contains(gotToml, `stories = `) {
		t.Errorf("the hosted scope keys were restored: %s", gotToml)
	}
	if !strings.Contains(gotToml, `stale_after = "6h"`) {
		t.Errorf("the rest of the hosted [sync] block was not restored: %s", gotToml)
	}
	if !strings.Contains(out, "removed [sync] scope keys all, stories") {
		t.Errorf("rehydrate must name the scope keys it dropped:\n%s", out)
	}
	if _, _, err := config.Load(""); err != nil {
		t.Errorf("the restored satelle.toml does not load: %v", err)
	}
}

// TestSyncScopesReportsWithheldFiles (sty_698e70b6 AC5): scopes lists withheld
// .local files under non-local areas.
func TestSyncScopesReportsWithheldFiles(t *testing.T) {
	repo := tempRepo(t)
	signInTo(t, config.DefaultHostedServer)
	cfgPath := filepath.Join(repo, ".satelle", "satelle.toml")
	if err := os.WriteFile(cfgPath, []byte("[sync]\nproject = \"probe\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	skillsDir := filepath.Join(repo, ".satelle", "skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillsDir, "keep.md"), []byte("---\ntype: skill\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillsDir, "secret.local.md"), []byte("SECRET\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runRoot(t, "sync", "scopes")
	if err != nil {
		t.Fatalf("sync scopes: %v\n%s", err, out)
	}
	if !strings.Contains(out, "withheld:") || !strings.Contains(out, "secret.local.md") {
		t.Errorf("scopes should list withheld secret.local.md:\n%s", out)
	}
}

// TestSyncNoopMakesNoContentUploads (sty_88e83180 AC7): after a successful
// workstate push, a second workstate push issues no POST. Config/docs legs are
// covered by their own skip tests; this pins the workstate half of the no-op.
func TestSyncNoopMakesNoContentUploads(t *testing.T) {
	ts, f := newFakeWorkstateServer(t)
	seedCred(t, ts.URL)
	workstateRepo(t, "[sync]\n\n[hosted]\nproject = \"probe\"\n")
	out, err := runRoot(t, "story", "create",
		"--title", "Noop", "--body", "b", "--acceptance", "1. ok", "--category", "chore")
	if err != nil {
		t.Fatalf("create: %v\n%s", err, out)
	}
	if out, err = runRoot(t, "sync", "workstate", "push", "--server", ts.URL); err != nil {
		t.Fatalf("first: %v\n%s", err, out)
	}
	n1 := f.postCount("probe")
	if n1 == 0 {
		t.Fatal("first push posted nothing")
	}
	if out, err = runRoot(t, "sync", "workstate", "push", "--server", ts.URL); err != nil {
		t.Fatalf("second: %v\n%s", err, out)
	}
	if f.postCount("probe") != n1 {
		t.Fatalf("noop workstate posts = %d, want %d", f.postCount("probe"), n1)
	}
}

// countingSyncFake is a multi-leg fake that counts config PUTs, document PUTs,
// and workstate POSTs (sty_88e83180 AC7).
type countingSyncFake struct {
	mu            sync.Mutex
	configPUTs    int
	documentPUTs  int
	workstatePOST int
	cfg           *fakeConfigStore
	docs          *fakeDocStore
	ws            *fakeWorkstateServer
}

func newCountingSyncServer(t *testing.T) (*httptest.Server, *countingSyncFake) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	hosted.DocumentSyncStatePathOverride = filepath.Join(t.TempDir(), "document-sync-state.json")
	t.Cleanup(func() { hosted.DocumentSyncStatePathOverride = "" })
	f := &countingSyncFake{
		cfg:  &fakeConfigStore{data: map[string]map[string][][]byte{}},
		docs: newFakeDocStore(),
		ws: &fakeWorkstateServer{
			posts:      map[string][]map[string]any{},
			itemsByID:  map[string]map[string]any{},
			ledgerByID: map[string]map[string]any{},
		},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/projects/", func(w http.ResponseWriter, r *http.Request) {
		rest := strings.TrimPrefix(r.URL.Path, "/api/v1/projects/")
		segs := strings.SplitN(rest, "/", 3)
		if len(segs) < 2 {
			http.NotFound(w, r)
			return
		}
		project, kind := segs[0], segs[1]
		path := ""
		if len(segs) == 3 {
			path = segs[2]
		}
		switch kind {
		case "config":
			switch {
			case r.Method == http.MethodPut:
				body, _ := io.ReadAll(r.Body)
				f.mu.Lock()
				f.configPUTs++
				f.mu.Unlock()
				sha, ver, created := f.cfg.put(project, path, body)
				status := http.StatusOK
				if created {
					status = http.StatusCreated
				}
				w.WriteHeader(status)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"path": path, "version": ver, "blob_sha256": sha, "size": len(body), "created": created,
				})
			case r.Method == http.MethodGet && path == "":
				_ = json.NewEncoder(w).Encode(f.cfg.manifest(project))
			default:
				http.NotFound(w, r)
			}
		case "documents":
			switch {
			case r.Method == http.MethodPut:
				body, _ := io.ReadAll(r.Body)
				f.mu.Lock()
				f.documentPUTs++
				f.mu.Unlock()
				sha, ver, created := f.docs.put(project, path, body)
				status := http.StatusOK
				if created {
					status = http.StatusCreated
				}
				w.WriteHeader(status)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"path": path, "version": ver, "blob_sha256": sha, "size": len(body), "created": created,
				})
			case r.Method == http.MethodGet && path == "":
				items, cursor := f.docs.changes(project, r.URL.Query().Get("since"))
				_ = json.NewEncoder(w).Encode(map[string]any{"items": items, "cursor": cursor})
			default:
				http.NotFound(w, r)
			}
		default:
			http.NotFound(w, r)
		}
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	attachTestGRPC(t, &countingSyncGRPC{f: f}, func(gs *grpc.Server) { syncpb.RegisterChainServer(gs, newFakeChain(f.ws)) })
	return ts, f
}

type countingSyncGRPC struct {
	syncpb.UnimplementedSyncServer
	f *countingSyncFake
}

func (s *countingSyncGRPC) Apply(ctx context.Context, req *syncpb.ApplyRequest) (*syncpb.ApplyResponse, error) {
	s.f.mu.Lock()
	s.f.workstatePOST++
	s.f.mu.Unlock()
	return (&fakeWorkstateGRPC{f: s.f.ws}).Apply(ctx, req)
}

// TestSyncNoopZeroContentUploads (sty_88e83180 AC7): a second full `satelle
// sync` on a populated repo issues zero config PUTs, zero document PUTs, and
// zero workstate POSTs.
func TestSyncNoopZeroContentUploads(t *testing.T) {
	ts, f := newCountingSyncServer(t)
	seedCred(t, ts.URL)
	repo := syncConfigRepo(t, "[sync]\n"+boundProjectToml)
	writeRepoFile(t, repo, ".satelle/principles/rule.md", "rule body\n")
	writeRepoFile(t, repo, ".satelle/documents/note.md", "note body\n")
	pointAt(t, repo)
	// Story for workstate.
	if out, err := runRoot(t, "story", "create",
		"--title", "Sync noop", "--body", "b", "--acceptance", "1. ok", "--category", "chore"); err != nil {
		t.Fatalf("create: %v\n%s", err, out)
	}

	if out, err := runRoot(t, "sync", "--server", ts.URL); err != nil {
		t.Fatalf("first sync: %v\n%s", err, out)
	}
	f.mu.Lock()
	c1, d1, w1 := f.configPUTs, f.documentPUTs, f.workstatePOST
	f.mu.Unlock()
	if c1 == 0 || d1 == 0 || w1 == 0 {
		t.Fatalf("first sync expected content traffic, got config=%d docs=%d ws=%d", c1, d1, w1)
	}

	if out, err := runRoot(t, "sync", "--server", ts.URL); err != nil {
		t.Fatalf("second sync: %v\n%s", err, out)
	}
	f.mu.Lock()
	c2, d2, w2 := f.configPUTs, f.documentPUTs, f.workstatePOST
	f.mu.Unlock()
	if c2 != c1 || d2 != d1 || w2 != w1 {
		t.Fatalf("noop sync must not upload: config %d→%d docs %d→%d ws %d→%d",
			c1, c2, d1, d2, w1, w2)
	}
}
