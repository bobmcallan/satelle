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
