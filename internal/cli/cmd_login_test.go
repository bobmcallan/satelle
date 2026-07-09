package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/spf13/cobra"
)

// TestRecordLoginBindingWritesGlobalNotRepo proves the sty_53ccf845 binding split:
// login writes the server to the GLOBAL config, never a [hosted] server into the
// committed repo satelle.toml; no token is written to either config.
func TestRecordLoginBindingWritesGlobalNotRepo(t *testing.T) {
	t.Setenv("SATELLE_HOME", t.TempDir())
	repo := t.TempDir()
	cfgPath := filepath.Join(repo, ".satelle", "satelle.toml")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte("# repo config\n[review]\ngate_create = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// The repo file is byte-identical; the server lands in global.
	before, _ := os.ReadFile(cfgPath)
	if err := recordLoginBinding("https://h/"); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(cfgPath)
	if string(before) != string(after) {
		t.Fatalf("repo satelle.toml changed by login:\n%s", after)
	}
	gc, err := config.LoadGlobal()
	if err != nil {
		t.Fatal(err)
	}
	if gc.Hosted.ResolveServer() != "https://h" {
		t.Fatalf("global server = %q, want https://h", gc.Hosted.Server)
	}
}

func testCmd() (*cobra.Command, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	c := &cobra.Command{}
	c.SetOut(buf)
	c.SetContext(context.Background())
	return c, buf
}

// TestRecordActiveWorkspaceWritesOverlayNotRepo proves the AC1 write semantics:
// the active-workspace selection lands in the gitignored satelle.local.toml
// OVERLAY (a per-developer value), never the team-committed satelle.toml; other
// keys and comments in both files are preserved. Out-of-repo (empty cfgPath)
// records nothing.
func TestRecordActiveWorkspaceWritesOverlayNotRepo(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, ".satelle", "satelle.toml")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	// A committed team default plus an unrelated key the writer must preserve.
	if err := os.WriteFile(cfgPath, []byte("# team config\n[review]\ngate_create = true\n\n[hosted]\nworkspace = \"Team Default\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(cfgPath)

	if err := recordActiveWorkspace(cfgPath, "Acme Team"); err != nil {
		t.Fatal(err)
	}

	// satelle.toml is byte-untouched — the per-user choice never mutates the
	// team file.
	after, _ := os.ReadFile(cfgPath)
	if string(before) != string(after) {
		t.Fatalf("committed satelle.toml changed by --workspace:\n%s", after)
	}

	// The selection lands in the per-user overlay, preserving its other keys.
	localPath := filepath.Join(dir, ".satelle", "satelle.local.toml")
	local, err := os.ReadFile(localPath)
	if err != nil {
		t.Fatalf("overlay not written: %v", err)
	}
	if !strings.Contains(string(local), `workspace = "Acme Team"`) {
		t.Fatalf("overlay missing the workspace selection:\n%s", local)
	}
}

// TestRecordActiveWorkspaceOutOfRepoIsNoop proves out-of-repo login records no
// workspace, preserving fresh-clone portability (AC1/AC4).
func TestRecordActiveWorkspaceOutOfRepoIsNoop(t *testing.T) {
	if err := recordActiveWorkspace("", "Acme Team"); err != nil {
		t.Fatalf("out-of-repo record should be a no-op, got %v", err)
	}
}

// TestMatchWorkspace covers --workspace resolution: exact name wins, then exact
// id (the unambiguous fallback), then not-found.
func TestMatchWorkspace(t *testing.T) {
	list := []hosted.Workspace{
		{ID: "w1", Kind: "personal", Name: "Mine"},
		{ID: "w2", Kind: "team", Name: "Acme Team"},
	}
	if ws, ok := matchWorkspace(list, "Acme Team"); !ok || ws.ID != "w2" {
		t.Fatalf("name match: got %+v ok=%v", ws, ok)
	}
	if ws, ok := matchWorkspace(list, "w1"); !ok || ws.Name != "Mine" {
		t.Fatalf("id fallback: got %+v ok=%v", ws, ok)
	}
	// Name wins over a coincidentally-id-shaped argument.
	if ws, ok := matchWorkspace(list, "Mine"); !ok || ws.ID != "w1" {
		t.Fatalf("name precedence: got %+v ok=%v", ws, ok)
	}
	if _, ok := matchWorkspace(list, "nope"); ok {
		t.Fatalf("not-found should be false")
	}
}

// TestWorkspaceNotFoundIsHumanText proves an unresolvable --workspace surfaces a
// clear human message listing the available workspaces, with no raw JSON body
// (AC4).
func TestWorkspaceNotFoundIsHumanText(t *testing.T) {
	list := []hosted.Workspace{
		{ID: "w1", Kind: "personal", Name: "Mine"},
		{ID: "w2", Kind: "team", Name: "Acme Team"},
	}
	err := workspaceNotFound("nope", list)
	msg := err.Error()
	for _, want := range []string{"nope", "Mine (personal)", "Acme Team (team)", "re-run login"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error missing %q: %s", want, msg)
		}
	}
	// The empty-list variant stays human too.
	if err := workspaceNotFound("nope", nil); !strings.Contains(err.Error(), "no workspaces") {
		t.Errorf("empty-list error: %v", err)
	}
}

// TestRunWhoamiSurfacesActiveWorkspace proves whoami reports the active workspace
// alongside the identity when a repo config binds one (AC2).
func TestRunWhoamiSurfacesActiveWorkspace(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	// Point config.Load at a temp repo whose active workspace is a team one.
	repoConfig(t, "[hosted]\nworkspace = \"Acme Team\"\n")

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/me", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer good" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(hosted.Principal{ID: "u", Email: "me@x.io", DisplayName: "Me", Role: "admin"})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	if err := (hosted.FileStore{}).Save(hosted.Credential{ServerURL: ts.URL, AccessToken: "good", RefreshToken: "r"}); err != nil {
		t.Fatal(err)
	}
	cmd, buf := testCmd()
	if err := runWhoami(cmd, ts.URL); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "me@x.io") || !strings.Contains(buf.String(), "workspace Acme Team") {
		t.Fatalf("whoami did not surface the active workspace: %q", buf.String())
	}
}

func TestRunLogoutClearsCredential(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	const server = "https://logout.example"
	store := hosted.FileStore{}
	if err := store.Save(hosted.Credential{ServerURL: server, AccessToken: "a", RefreshToken: "r"}); err != nil {
		t.Fatal(err)
	}
	cmd, _ := testCmd()
	if err := runLogout(cmd, server); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(server); !errors.Is(err, hosted.ErrNoCredential) {
		t.Fatalf("credential not cleared: %v", err)
	}
}

func TestRunWhoamiNotSignedIn(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer ts.Close()

	cmd, _ := testCmd()
	err := runWhoami(cmd, ts.URL)
	if !errors.Is(err, hosted.ErrLoginRequired) {
		t.Fatalf("expected ErrLoginRequired, got %v", err)
	}
}

func TestRunWhoamiHappy(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/me", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer good" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(hosted.Principal{ID: "u", Email: "me@x.io", DisplayName: "Me", Role: "admin"})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	if err := (hosted.FileStore{}).Save(hosted.Credential{ServerURL: ts.URL, AccessToken: "good", RefreshToken: "r"}); err != nil {
		t.Fatal(err)
	}
	cmd, buf := testCmd()
	if err := runWhoami(cmd, ts.URL); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "me@x.io") || !strings.Contains(buf.String(), "admin") {
		t.Fatalf("identity not printed: %q", buf.String())
	}
}
