//go:build integration

package tests

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestUpdateCheckSendsTokenToReleaseAPI drives the real `satelle update --check`
// — the command the session-start release refresh spawns as a detached child
// that inherits the operator's environment (sty_66dce129). With GH_TOKEN set the
// stub release API must see it as a bearer token; the token must not appear in
// what the binary printed.
func TestUpdateCheckSendsTokenToReleaseAPI(t *testing.T) {
	const token = "satelle-it-token-7f3a91"
	var mu sync.Mutex
	var seen []string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Header.Get("Authorization"))
		mu.Unlock()
		fmt.Fprint(w, `{"tag_name":"v9.9.9"}`)
	})
	mux.HandleFunc("/api/releases", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `[]`) })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cmd := exec.Command(testBin, "update", "--check")
	cmd.Env = append(os.Environ(),
		"SATELLE_HOME="+isolatedHome(t),
		"SATELLE_RELEASE_API="+srv.URL+"/api/releases/latest",
		"SATELLE_RELEASE_LIST_API="+srv.URL+"/api/releases",
		"SATELLE_INSTALL_DIR="+t.TempDir(),
		"GITHUB_TOKEN=",
		"GH_TOKEN="+token,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("update --check: %v\n%s", err, out)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) == 0 || seen[0] != "Bearer "+token {
		t.Errorf("release API saw Authorization %q, want %q", seen, "Bearer "+token)
	}
	if strings.Contains(string(out), token) {
		t.Errorf("output carries the token:\n%s", out)
	}
}

// TestUpdateCheckReportsRateLimit: a rate-limit 403 from the release API reaches
// stderr as a rate limit with the remedy, not as a bare 403 Forbidden.
func TestUpdateCheckReportsRateLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.Header().Set("X-RateLimit-Reset", fmt.Sprint(time.Now().Add(10*time.Minute).Unix()))
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	cmd := exec.Command(testBin, "update", "--check")
	cmd.Env = append(os.Environ(),
		"SATELLE_HOME="+isolatedHome(t),
		"SATELLE_RELEASE_API="+srv.URL+"/api/releases/latest",
		"SATELLE_RELEASE_LIST_API="+srv.URL+"/api/releases",
		"SATELLE_INSTALL_DIR="+t.TempDir(),
		"GITHUB_TOKEN=",
		"GH_TOKEN=",
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("want a failing exit, got:\n%s", out)
	}
	for _, want := range []string{"rate limit", "resets at", "GH_TOKEN"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("stderr lacks %q:\n%s", want, out)
		}
	}
}
