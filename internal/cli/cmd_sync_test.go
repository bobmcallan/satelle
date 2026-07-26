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

	"github.com/bobmcallan/satelle/internal/hosted"
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

// TestSyncScopesConfiguredAndOverlay exercises AC1 (committed [sync] + a
// satelle.local.toml per-dev override) end to end via the CLI, and AC4 (the
// command is the resolver's production caller).
func TestSyncScopesConfiguredAndOverlay(t *testing.T) {
	repo := tempRepo(t)
	cfgPath := filepath.Join(repo, ".satelle", "satelle.toml")
	committed := "web_port = 8181\n\n[sync]\nskills = \"shared\"\ndocuments = \"personal\"\n"
	if err := os.WriteFile(cfgPath, []byte(committed), 0o644); err != nil {
		t.Fatal(err)
	}
	local := "[sync]\ndocuments = \"local\"\n"
	if err := os.WriteFile(filepath.Join(repo, ".satelle", "satelle.local.toml"), []byte(local), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := runRoot(t, "sync", "scopes")
	if err != nil {
		t.Fatalf("sync scopes: %v\n%s", err, out)
	}
	scopes := areaScopes(out)
	if scopes["skills"] != "shared" {
		t.Errorf("skills scope = %q, want shared (committed value, no overlay)", scopes["skills"])
	}
	if scopes["documents"] != "local" {
		t.Errorf("documents scope = %q, want local (overlay override of committed personal)", scopes["documents"])
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
	cfgPath := filepath.Join(repo, ".satelle", "satelle.toml")
	cfg := "web_port = 8181\n\n[sync]\nskills = \"personal\"\ndocuments = \"personal\"\nstories = \"personal\"\n"
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
	// Project-addressed config/documents/workstate (sty_ca64d0cb). Store key is
	// the project id/slug alone.
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
		case "workstate":
			if r.Method != http.MethodPost {
				http.NotFound(w, r)
				return
			}
			body, _ := io.ReadAll(r.Body)
			var batch map[string]any
			_ = json.Unmarshal(body, &batch)
			f.mu.Lock()
			f.workstate++
			f.mu.Unlock()
			items, _ := batch["items"].([]any)
			ledger, _ := batch["ledger"].([]any)
			_ = json.NewEncoder(w).Encode(map[string]int{"items": len(items), "ledger": len(ledger)})
		default:
			http.NotFound(w, r)
		}
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, f
}

// TestSyncDefaultRunsFullBundleLive drives bare `satelle sync` (non-dry-run)
// through every opted-in verb against one combined server and asserts each
// actually executed: config push, documents push, documents PULL (a server-only
// file must land locally — the branch a preview test can't pin), and work-state
// push (default-action AC1).
func TestSyncDefaultRunsFullBundleLive(t *testing.T) {
	ts, f := newFakeSyncServer(t)
	seedCred(t, ts.URL)

	repo := syncConfigRepo(t, "[sync]\nskills = \"personal\"\ndocuments = \"personal\"\nstories = \"personal\"\n"+boundProjectToml)
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

	repo := syncConfigRepo(t, "[sync]\nskills = \"personal\"\ndocuments = \"personal\"\nstories = \"personal\"\n"+boundProjectToml)
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

// TestSyncScopesInvalidScopeRefuses: an area explicitly set to a value outside
// local|personal|shared refuses the command rather than silently coercing to
// local (plan addendum #2).
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

// TestSyncScopesListsSharedFiles: a file with `shared: true` frontmatter inside
// a personal-scope area is listed by the command (AC2, AC4).
func TestSyncScopesListsSharedFiles(t *testing.T) {
	repo := tempRepo(t)
	cfgPath := filepath.Join(repo, ".satelle", "satelle.toml")
	if err := os.WriteFile(cfgPath, []byte("[sync]\nskills = \"personal\"\n"), 0o644); err != nil {
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

// TestSyncRehydrateLocalOnly: with no personal opt-in, rehydrate runs each step
// as a local skip / empty deploy and exits 0 (order:4 AC4).
func TestSyncRehydrateLocalOnly(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	repo := tempRepo(t)
	// Bound project so deploy can resolve; fake server returns empty manifest.
	cfgPath := filepath.Join(repo, ".satelle", "satelle.toml")
	if err := os.WriteFile(cfgPath, []byte("web_port = 8181\n\n[hosted]\nproject = \"probe\"\n"), 0o644); err != nil {
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
	if !strings.Contains(out, "every .satelle area is still local after config deploy") {
		t.Errorf("expected post-deploy all-local explain note:\n%s", out)
	}
}

// TestSyncRehydrateEmptyTreeHappyPath: bind-created toml + deploy restores
// [sync] personal + documents path + workstate items (order:4 AC5 hermetic).
func TestSyncRehydrateEmptyTreeHappyPath(t *testing.T) {
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
	hostedToml := "web_port = 8181\n\n[sync]\nall = \"personal\"\n\n[hosted]\nproject = \"probe\"\n"
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
	// workstate items after deploy opt-in
	itemRec := map[string]any{
		"id": "sty_rehy1", "kind": "story", "status": "backlog", "title": "Rehydrated",
		"body": "from hosted", "acceptance_criteria": "1. ok",
	}
	recBytes, _ := json.Marshal(itemRec)
	mux.HandleFunc("GET /api/v1/projects/{project}/workstate/items", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{{
			"id": "sty_rehy1", "kind": "story", "type": "stories", "status": "backlog",
			"title": "Rehydrated", "origin": "cli-sync", "record": json.RawMessage(recBytes),
		}})
	})
	mux.HandleFunc("GET /api/v1/projects/{project}/workstate/ledger", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]any{})
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
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
	// Deployed scopes should opt in — no all-local note.
	if strings.Contains(out, "every .satelle area is still local after config deploy") {
		t.Errorf("unexpected all-local note after deploy with personal scopes:\n%s", out)
	}
	// Story materialised.
	got, gerr := runRoot(t, "story", "get", "sty_rehy1")
	if gerr != nil {
		t.Fatalf("story get after rehydrate: %v\n%s\nrehydrate out:\n%s", gerr, got, out)
	}
	if !strings.Contains(got, "Rehydrated") {
		t.Errorf("story not restored: %s", got)
	}
}

// TestSyncScopesReportsWithheldFiles (sty_698e70b6 AC5): scopes lists withheld
// .local files under non-local areas.
func TestSyncScopesReportsWithheldFiles(t *testing.T) {
	repo := tempRepo(t)
	cfgPath := filepath.Join(repo, ".satelle", "satelle.toml")
	if err := os.WriteFile(cfgPath, []byte("[sync]\nskills = \"personal\"\n"), 0o644); err != nil {
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
