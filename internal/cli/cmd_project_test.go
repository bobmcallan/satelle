package cli

import (
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
)

// seedCred saves a usable credential for server so the project commands
// authenticate in tests.
func seedCred(t *testing.T, server string) {
	t.Helper()
	if err := (hosted.FileStore{}).Save(hosted.Credential{ServerURL: server, AccessToken: "good", RefreshToken: "r"}); err != nil {
		t.Fatal(err)
	}
}

func TestRunProjectCreateHappy(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/projects", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(hosted.Project{ID: "p1", Slug: "acme", Name: "Acme Co"})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()
	seedCred(t, ts.URL)

	cmd, buf := testCmd()
	if err := runProjectCreate(cmd, ts.URL, "acme", "Acme Co"); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "p1") || !strings.Contains(out, "acme") || !strings.Contains(out, "Acme Co") {
		t.Fatalf("output missing id/slug/name: %q", out)
	}
}

// Missing --slug/--name is rejected client-side with NO network call.
func TestRunProjectCreateValidation(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected network call to %s", r.URL.Path)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()
	seedCred(t, ts.URL)

	cmd, _ := testCmd()
	if err := runProjectCreate(cmd, ts.URL, "  ", "Acme"); err == nil || !strings.Contains(err.Error(), "slug") {
		t.Fatalf("blank slug: expected slug error, got %v", err)
	}
	if err := runProjectCreate(cmd, ts.URL, "acme", ""); err == nil || !strings.Contains(err.Error(), "name") {
		t.Fatalf("blank name: expected name error, got %v", err)
	}
}

func TestRunProjectCreateSlugConflict(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	const secret = "raw-body-should-not-appear"
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/projects", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": secret})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()
	seedCred(t, ts.URL)

	cmd, _ := testCmd()
	err := runProjectCreate(cmd, ts.URL, "dup", "Dup")
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("expected slug-exists error, got %v", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("error leaked raw body: %v", err)
	}
}

func TestRunProjectCreateNotSignedIn(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer ts.Close()
	// No credential seeded.

	cmd, _ := testCmd()
	err := runProjectCreate(cmd, ts.URL, "acme", "Acme")
	if !errors.Is(err, hosted.ErrLoginRequired) {
		t.Fatalf("expected ErrLoginRequired, got %v", err)
	}
	if !strings.Contains(err.Error(), "satelle login") {
		t.Errorf("error should mention satelle login: %v", err)
	}
}

func TestRunProjectListHappy(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/projects", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]hosted.Project{
			{ID: "p1", Slug: "acme", Name: "Acme Co", Role: "owner"},
		})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()
	seedCred(t, ts.URL)

	cmd, buf := testCmd()
	if err := runProjectList(cmd, ts.URL); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"p1", "acme", "Acme Co", "owner"} {
		if !strings.Contains(out, want) {
			t.Fatalf("list output missing %q: %q", want, out)
		}
	}
}

func TestRunProjectListEmpty(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/projects", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]hosted.Project{})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()
	seedCred(t, ts.URL)

	cmd, buf := testCmd()
	if err := runProjectList(cmd, ts.URL); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "No projects") {
		t.Fatalf("expected empty-list message, got %q", buf.String())
	}
}

// TestRunProjectShow reports hosted server, bound project, and sign-in state.
func TestRunProjectShow(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	// Isolate from this checkout's satelle.toml so the unbound case is real.
	dir := filepath.Join(t.TempDir(), ".satelle")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	toml := filepath.Join(dir, "satelle.toml")
	if err := os.WriteFile(toml, []byte("data_dir = \".satelle\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SATELLE_CONFIG", toml)

	// Unbound repo: bound project line shows the none-hint.
	cmd, buf := testCmd()
	if err := runProjectShow(cmd, "https://h.example"); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "hosted server: https://h.example") {
		t.Fatalf("show missing hosted-server line: %q", out)
	}
	if !strings.Contains(out, "sign-in state: signed out") {
		t.Fatalf("show missing signed-out state: %q", out)
	}
	if !strings.Contains(out, "bound project: (none") {
		t.Fatalf("show should report unbound project: %q", out)
	}

	// Signed in for the server: sign-in state flips.
	ts := httptest.NewServer(http.NewServeMux())
	defer ts.Close()
	seedCred(t, ts.URL)
	cmd2, buf2 := testCmd()
	if err := runProjectShow(cmd2, ts.URL); err != nil {
		t.Fatal(err)
	}
	out2 := buf2.String()
	if !strings.Contains(out2, "sign-in state: signed in") {
		t.Fatalf("show should report signed in after seedCred: %q", out2)
	}
}

// TestRunProjectBindAndShow: bind writes the slug into committed satelle.toml
// and show reads it back (sty_0aa3df89).
func TestRunProjectBindAndShow(t *testing.T) {
	t.Setenv("SATELLE_HOME", t.TempDir()) // bind may touch GlobalDir paths (sty_c36c211f)
	dir := filepath.Join(t.TempDir(), ".satelle")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	toml := filepath.Join(dir, "satelle.toml")
	if err := os.WriteFile(toml, []byte("data_dir = \".satelle\"\n[hosted]\n# keep this comment\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SATELLE_CONFIG", toml)

	cmd, buf := testCmd()
	if err := runProjectBind(cmd, "acme"); err != nil {
		t.Fatalf("bind: %v", err)
	}
	b, _ := os.ReadFile(toml)
	if !strings.Contains(string(b), `project = "acme"`) {
		t.Fatalf("bind did not record the project:\n%s", b)
	}
	if !strings.Contains(string(b), "keep this comment") || !strings.Contains(string(b), "data_dir") {
		t.Fatalf("bind clobbered unrelated config:\n%s", b)
	}
	if !strings.Contains(buf.String(), "acme") {
		t.Errorf("bind output missing slug: %q", buf.String())
	}

	cmd2, buf2 := testCmd()
	if err := runProjectShow(cmd2, ""); err != nil {
		t.Fatalf("show: %v", err)
	}
	if !strings.Contains(buf2.String(), "bound project: acme") {
		t.Errorf("show did not report the bound project:\n%s", buf2.String())
	}
}

// TestResolveBoundProjectEmpty: blank hosted.project yields the clear AC5 error.
func TestResolveBoundProjectEmpty(t *testing.T) {
	_, err := resolveBoundProject(config.Config{})
	if err == nil || !strings.Contains(err.Error(), "no hosted project bound") {
		t.Fatalf("expected unbound error, got %v", err)
	}
	// Actionable guidance names the full operator path (login / create / bind / toml).
	for _, want := range []string{"satelle login", "project create", "project bind", "[hosted] project"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("unbound error missing %q: %v", want, err)
		}
	}
	slug, err := resolveBoundProject(config.Config{Hosted: config.HostedConfig{Project: "  acme  "}})
	if err != nil || slug != "acme" {
		t.Fatalf("trim/bound = %q, %v", slug, err)
	}
}

// TestProjectBindCreatesTomlWhenAbsent: empty-tree bind writes .satelle/satelle.toml
// without requiring a prior satelle init (workspace-rehydrate order:4).
func TestProjectBindCreatesTomlWhenAbsent(t *testing.T) {
	repo := t.TempDir()
	t.Chdir(repo)
	t.Setenv("SATELLE_CONFIG", "") // ensure Load walks CWD
	// Clear any SATELLE_CONFIG from other tests.
	_ = os.Unsetenv("SATELLE_CONFIG")

	cmd, buf := testCmd()
	if err := runProjectBind(cmd, "fresh-slug"); err != nil {
		t.Fatalf("bind: %v\n%s", err, buf.String())
	}
	cfgPath := filepath.Join(repo, ".satelle", "satelle.toml")
	b, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("expected satelle.toml after bind: %v", err)
	}
	if !strings.Contains(string(b), "fresh-slug") {
		t.Fatalf("toml missing project slug: %s", b)
	}
	if !strings.Contains(buf.String(), "fresh-slug") {
		t.Fatalf("output: %s", buf.String())
	}
}
