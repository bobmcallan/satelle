package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
)

// TestWorkspaceAddRegistersAndSeeds (sty_805bee9c AC1): with [server] endpoint,
// workspace add registers the repo and POSTs exactly one snapshot.
func TestWorkspaceAddRegistersAndSeeds(t *testing.T) {
	var snapHits atomic.Int32
	var lastSnap []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ingest/snapshot" {
			snapHits.Add(1)
			lastSnap, _ = io.ReadAll(r.Body)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)

	repo := tempRepo(t)
	cfgPath := filepath.Join(repo, ".satelle", "satelle.toml")
	if err := os.WriteFile(cfgPath, []byte("web_port = 8181\n\n[server]\nendpoint = \""+srv.URL+"\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SATELLE_CONFIG", cfgPath)

	// Seed a story so the snapshot body is non-empty.
	if out, err := runRoot(t, "story", "create",
		"--title", "Workspace Seed",
		"--body", "join seeds mirror",
		"--acceptance", "1. in snapshot",
		"--category", "chore",
	); err != nil {
		t.Fatalf("create: %v\n%s", err, out)
	}

	snapHits.Store(0)
	out, err := runRoot(t, "workspace", "add")
	if err != nil {
		t.Fatalf("workspace add: %v\n%s", err, out)
	}
	if !strings.Contains(out, "workspace add: ok") {
		t.Fatalf("expected ok line, got:\n%s", out)
	}
	if snapHits.Load() != 1 {
		t.Fatalf("snapshot posts = %d, want 1", snapHits.Load())
	}
	abs, _ := filepath.Abs(repo)
	gc, err := config.LoadGlobal()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range gc.Workspace.Repos {
		if r == abs {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("registry missing %s: %v", abs, gc.Workspace.Repos)
	}
	var snap struct {
		Stories []json.RawMessage `json:"stories"`
	}
	if err := json.Unmarshal(lastSnap, &snap); err != nil {
		t.Fatalf("snap json: %v", err)
	}
	if len(snap.Stories) == 0 {
		t.Fatal("snapshot had no stories")
	}
}

// TestWorkspaceAddBootstrapsEndpointFromServicePort (sty_0122610a AC1/AC5): no
// [server] endpoint, but a live serve on the global service port → write
// local.toml, seed once, exit 0.
func TestWorkspaceAddBootstrapsEndpointFromServicePort(t *testing.T) {
	var snapHits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
		case "/ingest/snapshot":
			snapHits.Add(1)
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatal(err)
	}

	repo := tempRepo(t)
	// tempRepo already isolated SATELLE_HOME; pin service port to httptest's.
	if err := config.SaveGlobal(config.GlobalConfig{
		Service: config.ServiceConfig{Port: port},
	}); err != nil {
		t.Fatal(err)
	}

	// No [server] in committed config.
	cfgPath := filepath.Join(repo, ".satelle", "satelle.toml")
	if err := os.WriteFile(cfgPath, []byte("web_port = 8181\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SATELLE_CONFIG", cfgPath)

	out, err := runRoot(t, "workspace", "add")
	if err != nil {
		t.Fatalf("workspace add: %v\n%s", err, out)
	}
	if !strings.Contains(out, "bootstrapped") {
		t.Fatalf("expected bootstrap line:\n%s", out)
	}
	if !strings.Contains(out, "workspace add: ok") {
		t.Fatalf("expected ok line:\n%s", out)
	}
	if snapHits.Load() != 1 {
		t.Fatalf("snapshot posts = %d, want 1", snapHits.Load())
	}
	localPath := filepath.Join(repo, ".satelle", "satelle.local.toml")
	b, err := os.ReadFile(localPath)
	if err != nil {
		t.Fatalf("local.toml missing after bootstrap: %v", err)
	}
	if !strings.Contains(string(b), "endpoint") || !strings.Contains(string(b), srv.URL) {
		t.Fatalf("local.toml missing endpoint=%s:\n%s", srv.URL, b)
	}
}

// TestWorkspaceAddNoServeFailsWithRemedy (sty_0122610a AC2/AC5): no endpoint and
// no serve → non-zero, names file/keys/URL/re-run, registration kept.
func TestWorkspaceAddNoServeFailsWithRemedy(t *testing.T) {
	repo := tempRepo(t)
	// Pin service port to something nothing is listening on.
	if err := config.SaveGlobal(config.GlobalConfig{
		Service: config.ServiceConfig{Port: 1},
	}); err != nil {
		t.Fatal(err)
	}

	out, err := runRoot(t, "workspace", "add")
	if err == nil {
		t.Fatalf("expected non-zero when no serve, got ok:\n%s", out)
	}
	for _, want := range []string{
		"satelle.local.toml",
		"[server]",
		"endpoint",
		"http://127.0.0.1:1",
		"workspace add",
	} {
		if !strings.Contains(out, want) && !strings.Contains(err.Error(), want) {
			// cobra surfaces RunE error; combined out may hold stderr/stdout only
			combined := out + "\n" + err.Error()
			if !strings.Contains(combined, want) {
				t.Errorf("remedy missing %q:\n%s\nerr=%v", want, out, err)
			}
		}
	}
	abs, _ := filepath.Abs(repo)
	gc, gerr := config.LoadGlobal()
	if gerr != nil {
		t.Fatal(gerr)
	}
	found := false
	for _, r := range gc.Workspace.Repos {
		if r == abs {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("registration must survive seed failure: %v", gc.Workspace.Repos)
	}
}

// TestWorkspaceAddRespectsConfiguredEndpoint (sty_0122610a direction item 3):
// endpoint already in satelle.toml → no probe, no local.toml write.
func TestWorkspaceAddRespectsConfiguredEndpoint(t *testing.T) {
	var snapHits atomic.Int32
	var healthzHits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz":
			healthzHits.Add(1)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
		case "/ingest/snapshot":
			snapHits.Add(1)
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	repo := tempRepo(t)
	cfgPath := filepath.Join(repo, ".satelle", "satelle.toml")
	if err := os.WriteFile(cfgPath, []byte("web_port = 8181\n\n[server]\nendpoint = \""+srv.URL+"\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SATELLE_CONFIG", cfgPath)

	out, err := runRoot(t, "workspace", "add")
	if err != nil {
		t.Fatalf("workspace add: %v\n%s", err, out)
	}
	if strings.Contains(out, "bootstrapped") {
		t.Fatalf("must not bootstrap when endpoint configured:\n%s", out)
	}
	if snapHits.Load() != 1 {
		t.Fatalf("snapshot posts = %d, want 1", snapHits.Load())
	}
	if healthzHits.Load() != 0 {
		t.Fatalf("healthz probes = %d, want 0 when endpoint configured", healthzHits.Load())
	}
	localPath := filepath.Join(repo, ".satelle", "satelle.local.toml")
	if _, err := os.Stat(localPath); err == nil {
		t.Fatalf("local.toml must not be created when endpoint already configured")
	}
}

// TestWorkspaceAddPushFailureKeepsRegistration (AC1): 500 after register.
func TestWorkspaceAddPushFailureKeepsRegistration(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	repo := tempRepo(t)
	cfgPath := filepath.Join(repo, ".satelle", "satelle.toml")
	if err := os.WriteFile(cfgPath, []byte("web_port = 8181\n\n[server]\nendpoint = \""+srv.URL+"\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SATELLE_CONFIG", cfgPath)

	out, err := runRoot(t, "workspace", "add")
	if err == nil {
		t.Fatalf("expected push failure, got ok:\n%s", out)
	}
	abs, _ := filepath.Abs(repo)
	gc, gerr := config.LoadGlobal()
	if gerr != nil {
		t.Fatal(gerr)
	}
	found := false
	for _, r := range gc.Workspace.Repos {
		if r == abs {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("registration must survive push failure: %v", gc.Workspace.Repos)
	}
}
