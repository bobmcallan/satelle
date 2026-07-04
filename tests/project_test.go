//go:build integration

package tests

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// stubProjectsServer is a hermetic /api/v1/projects endpoint for driving the
// real `satelle project` binary. It accepts the bearer "acc" (the token the
// seeded credential carries), records the last created project, and 409s a
// duplicate slug.
func stubProjectsServer(t *testing.T) *httptest.Server {
	t.Helper()
	created := map[string]bool{}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/projects", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer acc" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var in struct{ Slug, Name string }
		_ = json.NewDecoder(r.Body).Decode(&in)
		if created[in.Slug] {
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "could not create project"})
			return
		}
		created[in.Slug] = true
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "p-" + in.Slug, "slug": in.Slug, "name": in.Name})
	})
	mux.HandleFunc("GET /api/v1/projects", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer acc" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		out := []map[string]string{}
		for slug := range created {
			out = append(out, map[string]string{"id": "p-" + slug, "slug": slug, "name": slug + " project", "role": "owner"})
		}
		_ = json.NewEncoder(w).Encode(out)
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

// seedCredential writes a per-user credential file under xdg so the binary
// authenticates to server without running the OAuth flow.
func seedCredential(t *testing.T, xdg, server string) {
	t.Helper()
	dir := filepath.Join(xdg, "satelle")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	body := "[[credential]]\nserver_url = \"" + server + "\"\naccess_token = \"acc\"\nrefresh_token = \"ref\"\ntoken_type = \"Bearer\"\n"
	if err := os.WriteFile(filepath.Join(dir, "credentials.toml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// projectCmd builds a `satelle project …` command bound to repo + xdg.
func projectCmd(bin, repo, xdg string, args ...string) *exec.Cmd {
	cmd := exec.Command(bin, args...)
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), "XDG_CONFIG_HOME="+xdg)
	return cmd
}

// TestProjectCreateAndListEndToEnd drives the real binary through project
// create (201), a duplicate (409 → clear message, no raw body), and list —
// against a stub server, with the credential seeded in the per-user XDG store.
func TestProjectCreateAndListEndToEnd(t *testing.T) {
	bin := testBin
	repo := t.TempDir()
	xdg := t.TempDir()
	mustRun(t, bin, repo, "init")

	ts := stubProjectsServer(t)
	seedCredential(t, xdg, ts.URL)

	// create → 201, prints id/slug/name.
	out, err := projectCmd(bin, repo, xdg, "project", "create", "--server", ts.URL, "--slug", "acme", "--name", "Acme Co").CombinedOutput()
	if err != nil {
		t.Fatalf("project create: %v\n%s", err, out)
	}
	for _, want := range []string{"p-acme", "acme", "Acme Co"} {
		if !strings.Contains(string(out), want) {
			t.Fatalf("create output missing %q:\n%s", want, out)
		}
	}

	// duplicate slug → 409, clear "already exists", non-zero exit, no raw body.
	dupOut, dupErr := projectCmd(bin, repo, xdg, "project", "create", "--server", ts.URL, "--slug", "acme", "--name", "Acme Co").CombinedOutput()
	if dupErr == nil {
		t.Fatalf("duplicate create should fail, got:\n%s", dupOut)
	}
	if !strings.Contains(string(dupOut), "already exists") {
		t.Fatalf("409 message unclear:\n%s", dupOut)
	}
	if strings.Contains(string(dupOut), "could not create project") {
		t.Fatalf("409 leaked raw server body:\n%s", dupOut)
	}

	// list → shows the created project with its role.
	listOut, err := projectCmd(bin, repo, xdg, "project", "list", "--server", ts.URL).CombinedOutput()
	if err != nil {
		t.Fatalf("project list: %v\n%s", err, listOut)
	}
	for _, want := range []string{"p-acme", "acme", "owner"} {
		if !strings.Contains(string(listOut), want) {
			t.Fatalf("list output missing %q:\n%s", want, listOut)
		}
	}
}

// TestProjectCreateValidationNoNetwork proves missing --slug/--name is rejected
// client-side: a create with a blank slug against an unreachable server still
// fails on validation, not a network error.
func TestProjectCreateValidationNoNetwork(t *testing.T) {
	bin := testBin
	repo := t.TempDir()
	xdg := t.TempDir()
	mustRun(t, bin, repo, "init")

	out, err := projectCmd(bin, repo, xdg, "project", "create", "--server", "https://127.0.0.1:1", "--name", "Acme").CombinedOutput()
	if err == nil {
		t.Fatalf("blank slug should fail:\n%s", out)
	}
	if !strings.Contains(string(out), "slug") {
		t.Fatalf("expected slug validation error:\n%s", out)
	}
}

// TestProjectCreateNotSignedIn proves an absent credential yields the clear
// "run satelle login" message rather than a raw 401.
func TestProjectCreateNotSignedIn(t *testing.T) {
	bin := testBin
	repo := t.TempDir()
	xdg := t.TempDir() // no credential seeded
	mustRun(t, bin, repo, "init")

	out, err := projectCmd(bin, repo, xdg, "project", "create", "--server", "https://demo.example", "--slug", "acme", "--name", "Acme").CombinedOutput()
	if err == nil {
		t.Fatalf("create without credential should fail:\n%s", out)
	}
	if !strings.Contains(string(out), "satelle login") {
		t.Fatalf("expected login prompt:\n%s", out)
	}
}
