package cli

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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

// TestRunProjectShow covers show's surviving output after the substrate-backup
// removal (sty_ea7f2c39): it still reports the hosted server and sign-in state,
// and no longer prints a 'bound project' line (that binding went with push/pull).
func TestRunProjectShow(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	// Signed out, explicit server: the server + sign-in lines render; no binding.
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
	if strings.Contains(out, "bound project") {
		t.Fatalf("show must NOT print a 'bound project' line after removal: %q", out)
	}

	// Signed in for the server: sign-in state flips; still no binding line.
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
	if strings.Contains(out2, "bound project") {
		t.Fatalf("show must NOT print a 'bound project' line after removal: %q", out2)
	}
}
