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
	"time"
)

// TestChangePublisherCLI wires sty_126228b2: with [server] endpoint, story
// create posts a change event; with no config the publisher is inert; an
// unreachable endpoint does not fail the verb.
func TestChangePublisherCLI(t *testing.T) {
	var hits atomic.Int32
	var lastBody []byte
	done := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		lastBody, _ = io.ReadAll(r.Body)
		select {
		case done <- struct{}{}:
		default:
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)

	// --- active publisher ---
	repo := tempRepo(t)
	cfgPath := filepath.Join(repo, ".satelle", "satelle.toml")
	if err := os.WriteFile(cfgPath, []byte("web_port = 8181\n\n[server]\nendpoint = \""+srv.URL+"\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SATELLE_CONFIG", cfgPath)

	out, err := runRoot(t, "story", "create",
		"--title", "Push Probe",
		"--body", "emit change event",
		"--acceptance", "1. posted",
		"--category", "chore",
	)
	if err != nil {
		t.Fatalf("story create: %v\n%s", err, out)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("publisher did not POST within 2s")
	}
	var ev struct {
		RepoKey string `json:"repo_key"`
		Topic   string `json:"topic"`
		Entity  string `json:"entity"`
	}
	if err := json.Unmarshal(lastBody, &ev); err != nil {
		t.Fatalf("event json: %v body=%s", err, lastBody)
	}
	if ev.Topic != "stories" || ev.Entity != "story" || ev.RepoKey == "" {
		t.Errorf("event = %+v", ev)
	}

	// --- inert (no [server]) ---
	hits.Store(0)
	repo2 := tempRepo(t)
	// tempRepo already sets SATELLE_CONFIG without [server]
	out, err = runRoot(t, "story", "create",
		"--title", "No Push",
		"--body", "publisher off",
		"--acceptance", "1. ok",
		"--category", "chore",
	)
	if err != nil {
		t.Fatalf("create without server: %v\n%s", err, out)
	}
	time.Sleep(80 * time.Millisecond)
	if hits.Load() != 0 {
		t.Fatalf("inert config made %d POSTs", hits.Load())
	}
	_ = repo2

	// --- fail-silent unreachable ---
	repo3 := tempRepo(t)
	cfg3 := filepath.Join(repo3, ".satelle", "satelle.toml")
	if err := os.WriteFile(cfg3, []byte("web_port = 8181\n\n[server]\nendpoint = \"http://127.0.0.1:1\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SATELLE_CONFIG", cfg3)
	start := time.Now()
	out, err = runRoot(t, "story", "create",
		"--title", "Blackhole",
		"--body", "dead endpoint",
		"--acceptance", "1. ok",
		"--category", "chore",
	)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("create with blackhole endpoint must succeed: %v\n%s", err, out)
	}
	// Verb path must stay snappy — publisher is async (DefaultTimeout 500ms in
	// background only). Allow generous headroom for store open.
	if elapsed > 2*time.Second {
		t.Errorf("create took %v with blackhole endpoint — publisher may be blocking", elapsed)
	}
	if !strings.Contains(out, `"id"`) {
		t.Errorf("expected story JSON, got:\n%s", out)
	}
}

// TestInitScaffoldsServerBlock proves init's committed satelle.toml documents
// the [server] endpoint knob (commented; push off by default).
func TestInitScaffoldsServerBlock(t *testing.T) {
	if !strings.Contains(scaffoldToml, "[server]") || !strings.Contains(scaffoldToml, "endpoint") {
		t.Fatal("scaffoldToml must document commented [server] endpoint")
	}
	if !strings.Contains(scaffoldToml, "epic:serve-split") && !strings.Contains(scaffoldToml, "push-fed") {
		// soft: comment names the purpose
		t.Log("scaffold comment should name serve-split / push purpose")
	}
}
