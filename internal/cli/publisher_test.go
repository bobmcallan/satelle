package cli

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/mirror"
	"github.com/bobmcallan/satelle/internal/web"
)

// TestChangePublisherCLI wires sty_126228b2 + sty_9ba3d709: with [server]
// endpoint, story create delivers change + snapshot before exit; with no
// config the publisher is inert; unreachable/black-holed endpoints do not
// fail the verb and add only bounded latency.
func TestChangePublisherCLI(t *testing.T) {
	var changeHits, snapHits atomic.Int32
	var lastChangeBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		switch r.URL.Path {
		case "/ingest/change":
			changeHits.Add(1)
			lastChangeBody = body
		case "/ingest/snapshot":
			snapHits.Add(1)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)

	// --- active publisher: both paths arrive before runRoot returns ---
	_ = tempRepo(t)
	t.Setenv(EnvServerEndpoint, srv.URL)

	out, err := runRoot(t, "story", "create",
		"--title", "Push Probe",
		"--body", "emit change event",
		"--acceptance", "1. posted",
		"--category", "chore",
	)
	if err != nil {
		t.Fatalf("story create: %v\n%s", err, out)
	}
	// No sleep: drain is synchronous before exit (sty_9ba3d709 AC1).
	if changeHits.Load() < 1 {
		t.Fatalf("expected ≥1 /ingest/change, got %d", changeHits.Load())
	}
	if snapHits.Load() != 1 {
		t.Fatalf("expected exactly 1 /ingest/snapshot, got %d", snapHits.Load())
	}
	var ev struct {
		RepoKey string `json:"repo_key"`
		Topic   string `json:"topic"`
		Entity  string `json:"entity"`
	}
	if err := json.Unmarshal(lastChangeBody, &ev); err != nil {
		t.Fatalf("event json: %v body=%s", err, lastChangeBody)
	}
	if ev.Topic != "stories" || ev.Entity != "story" || ev.RepoKey == "" {
		t.Errorf("event = %+v", ev)
	}

	// --- inert (SATELLE_SERVER_ENDPOINT=none from tempRepo) ---
	changeHits.Store(0)
	snapHits.Store(0)
	repo2 := tempRepo(t)
	out, err = runRoot(t, "story", "create",
		"--title", "No Push",
		"--body", "publisher off",
		"--acceptance", "1. ok",
		"--category", "chore",
	)
	if err != nil {
		t.Fatalf("create without server: %v\n%s", err, out)
	}
	if changeHits.Load() != 0 || snapHits.Load() != 0 {
		t.Fatalf("inert config made change=%d snap=%d POSTs", changeHits.Load(), snapHits.Load())
	}
	_ = repo2

	// --- fail-silent connection refused ---
	_ = tempRepo(t)
	t.Setenv(EnvServerEndpoint, "http://127.0.0.1:1")
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
	// Connection-refused returns instantly; allow headroom for store open.
	if elapsed > 2*time.Second {
		t.Errorf("create took %v with refused endpoint — drain may be blocking", elapsed)
	}
	if !strings.Contains(out, `"id"`) {
		t.Errorf("expected story JSON, got:\n%s", out)
	}

	// --- true black-hole: accept, never respond; budget bounds total latency ---
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			// Hold the connection open until test ends; never respond.
			go func(conn net.Conn) {
				defer conn.Close()
				buf := make([]byte, 1)
				for {
					if _, err := conn.Read(buf); err != nil {
						return
					}
				}
			}(c)
		}
	}()
	_ = tempRepo(t)
	ep := "http://" + ln.Addr().String()
	t.Setenv(EnvServerEndpoint, ep)
	start = time.Now()
	out, err = runRoot(t, "story", "create",
		"--title", "Hang Probe",
		"--body", "black-holed endpoint",
		"--acceptance", "1. ok",
		"--category", "chore",
	)
	elapsed = time.Since(start)
	if err != nil {
		t.Fatalf("create with hang endpoint must succeed: %v\n%s", err, out)
	}
	if elapsed > 3*time.Second {
		t.Errorf("create took %v with hang endpoint — want ≤3s (budget 1.5s + store open)", elapsed)
	}
	if !strings.Contains(out, `"id"`) {
		t.Errorf("expected story JSON, got:\n%s", out)
	}
}

// TestUIDrainDeliversBeforeExit proves AC1/AC2: after runRoot returns (no
// sleeps), change ≥1 and snapshot == 1 for create and set; coalesced.
func TestUIDrainDeliversBeforeExit(t *testing.T) {
	var changeHits, snapHits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ingest/change":
			changeHits.Add(1)
		case "/ingest/snapshot":
			snapHits.Add(1)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)

	_ = tempRepo(t)
	t.Setenv(EnvServerEndpoint, srv.URL)

	out, err := runRoot(t, "story", "create",
		"--title", "Drain Create",
		"--body", "both paths before exit",
		"--acceptance", "1. posted",
		"--category", "chore",
	)
	if err != nil {
		t.Fatalf("create: %v\n%s", err, out)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(out), &created); err != nil || created.ID == "" {
		t.Fatalf("parse create: %v out=%s", err, out)
	}
	if changeHits.Load() < 1 || snapHits.Load() != 1 {
		t.Fatalf("after create: change=%d snap=%d", changeHits.Load(), snapHits.Load())
	}

	changeHits.Store(0)
	snapHits.Store(0)
	// story set can notify more than once (status/tags); snapshot must still be 1.
	out, err = runRoot(t, "story", "set", created.ID, "--add-tags", "drain:probe")
	if err != nil {
		t.Fatalf("set tags: %v\n%s", err, out)
	}
	if changeHits.Load() < 1 {
		t.Fatalf("after set: expected ≥1 change, got %d", changeHits.Load())
	}
	if snapHits.Load() != 1 {
		t.Fatalf("after set: expected exactly 1 snapshot (coalesced), got %d", snapHits.Load())
	}
}

// TestUIDrainConvergesMirror proves AC4: after story create against a real
// mirror ingest handler, the new story is listable — no manual ui push.
func TestUIDrainConvergesMirror(t *testing.T) {
	ms, err := mirror.Open(filepath.Join(t.TempDir(), "mirror.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ms.Close() })

	webSrv := web.NewMirror(ms)
	srv := httptest.NewServer(webSrv.Handler)
	t.Cleanup(srv.Close)

	repo := tempRepo(t)
	t.Setenv(EnvServerEndpoint, srv.URL)

	out, err := runRoot(t, "story", "create",
		"--title", "Mirror Converge",
		"--body", "visible without ui push",
		"--acceptance", "1. in mirror",
		"--category", "chore",
	)
	if err != nil {
		t.Fatalf("create: %v\n%s", err, out)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(out), &created); err != nil || created.ID == "" {
		t.Fatalf("parse: %v out=%s", err, out)
	}

	repoKey := config.RepoKey(repo)
	items, err := ms.ListItems(context.Background(), repoKey, "story")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, it := range items {
		if it.ID == created.ID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("mirror missing story %s after create (no ui push); items=%d", created.ID, len(items))
	}
}

// TestInitScaffoldsServerBlock proves init's committed satelle.toml points
// listen/push at machine [service] endpoint, not a repo [server] table.
func TestInitScaffoldsServerBlock(t *testing.T) {
	if strings.Contains(scaffoldToml, "\n[server]\n") || strings.Contains(scaffoldToml, "\n# [server]\n") {
		t.Fatal("scaffoldToml must not teach a repo [server] table")
	}
	if !strings.Contains(scaffoldToml, "[service] endpoint") && !strings.Contains(scaffoldToml, "config.toml") {
		t.Fatal("scaffoldToml must document machine-scope [service] endpoint")
	}
}
