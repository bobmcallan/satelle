package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

// TestWorkspaceAddNoEndpointRegisterOnly (AC1): no [server] → register + notice.
func TestWorkspaceAddNoEndpointRegisterOnly(t *testing.T) {
	repo := tempRepo(t)
	// tempRepo has no [server]
	out, err := runRoot(t, "workspace", "add")
	if err != nil {
		t.Fatalf("workspace add: %v\n%s", err, out)
	}
	if !strings.Contains(out, "mirror not seeded") {
		t.Fatalf("expected no-endpoint notice:\n%s", out)
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
		t.Fatalf("must still register without endpoint: %v", gc.Workspace.Repos)
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
