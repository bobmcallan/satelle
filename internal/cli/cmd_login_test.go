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
// committed repo satelle.toml; only the optional --project slug lands in the repo
// file; no token is written to either config.
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

	// No --project → the repo file is byte-identical; the server lands in global.
	before, _ := os.ReadFile(cfgPath)
	if err := recordLoginBinding(cfgPath, "https://h/", ""); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(cfgPath)
	if string(before) != string(after) {
		t.Fatalf("repo satelle.toml changed with no --project:\n%s", after)
	}
	gc, err := config.LoadGlobal()
	if err != nil {
		t.Fatal(err)
	}
	if gc.Hosted.ResolveServer() != "https://h" {
		t.Fatalf("global server = %q, want https://h", gc.Hosted.Server)
	}

	// With --project → only the project key is added to the repo file; still NO server.
	if err := recordLoginBinding(cfgPath, "https://h", "acme"); err != nil {
		t.Fatal(err)
	}
	final, _ := os.ReadFile(cfgPath)
	s := string(final)
	if !strings.Contains(s, `project = "acme"`) {
		t.Fatalf("project slug not written to repo file:\n%s", s)
	}
	if strings.Contains(s, "server =") {
		t.Fatalf("repo satelle.toml must NEVER gain a hosted server:\n%s", s)
	}
	if !strings.Contains(s, "gate_create = true") {
		t.Fatalf("unrelated repo section lost:\n%s", s)
	}
	if strings.Contains(s, "token") {
		t.Fatalf("no token may be written to the repo config:\n%s", s)
	}
}

func testCmd() (*cobra.Command, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	c := &cobra.Command{}
	c.SetOut(buf)
	c.SetContext(context.Background())
	return c, buf
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
