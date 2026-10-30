package serve

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/mirror"
	"github.com/bobmcallan/satelle/internal/testutil"
	"github.com/bobmcallan/satelle/internal/web"
)

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port
}

func waitHealthz(t *testing.T, port int) {
	t.Helper()
	url := fmt.Sprintf("http://127.0.0.1:%d/healthz", port)
	testutil.Eventually(t, waitBudget, func() bool {
		resp, err := http.Get(url)
		if err != nil {
			return false
		}
		resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, fmt.Sprintf("healthz on :%d never answered", port))
}

// TestServePortIgnoresRepoConfig (sty_21a7d16d AC2): repo web_port does not
// change which port satelled binds.
func TestServePortIgnoresRepoConfig(t *testing.T) {
	testutil.IsolateHome(t)
	port := freePort(t)
	if err := config.SaveGlobal(config.GlobalConfig{Service: config.ServiceConfig{Port: port, Addr: "127.0.0.1"}}); err != nil {
		t.Fatal(err)
	}
	repo := t.TempDir()
	dataDir := filepath.Join(repo, config.DefaultDataDir)
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, config.ConfigName), []byte("web_port = 9000\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errc := make(chan error, 1)
	go func() { errc <- Run(ctx, Options{}) }()
	waitHealthz(t, port)
	// 9000 must not be us (bind should have used global port).
	if ln, err := net.Listen("tcp", "127.0.0.1:9000"); err == nil {
		_ = ln.Close()
	}
	cancel()
	select {
	case <-errc:
	case <-time.After(waitBudget):
	}
}

// TestZeroConfigRepoServes (sty_21a7d16d AC6): no machine-scope keys, defaults work.
func TestZeroConfigRepoServes(t *testing.T) {
	testutil.IsolateHome(t)
	port := freePort(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errc := make(chan error, 1)
	go func() { errc <- Run(ctx, Options{Addr: "127.0.0.1", Port: port}) }()
	waitHealthz(t, port)
	cancel()
	select {
	case <-errc:
	case <-time.After(waitBudget):
	}
}

func TestServeAddrUsesGlobalWhenEmpty(t *testing.T) {
	testutil.IsolateHome(t)
	port := freePort(t)
	if err := config.SaveGlobal(config.GlobalConfig{Service: config.ServiceConfig{Port: port, Addr: "127.0.0.1"}}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errc := make(chan error, 1)
	go func() { errc <- Run(ctx, Options{}) }()
	waitHealthz(t, port)
	cancel()
	select {
	case <-errc:
	case <-time.After(waitBudget):
	}
}

// TestPushOffServesUIAndNeverPushes proves sty_a21d9c07 AC4: with [hosted] push =
// false in the machine config, satelled's UI routes answer and a local mutation's
// ingest lands in the mirror, while its Pusher flushes without ever pushing.
func TestPushOffServesUIAndNeverPushes(t *testing.T) {
	// Not IsolateHome: its endpoint off-switch would stop the Pusher before the
	// machine setting is ever asked.
	home := t.TempDir()
	t.Setenv("SATELLE_HOME", home)
	if err := os.WriteFile(filepath.Join(home, config.GlobalConfigName), []byte("[hosted]\npush = false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ms, err := mirror.Open(filepath.Join(t.TempDir(), "mirror.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ms.Close() })
	var asked atomic.Int32
	p := &Pusher{PushOff: func() (bool, string) {
		asked.Add(1)
		return config.MachinePushOff()
	}}
	_, rec := testPusher(t, p)
	srv := httptest.NewServer(web.NewMirrorHooks(ms, "test", p.OnIngest).Handler)
	t.Cleanup(srv.Close)

	get := func(path string) int {
		t.Helper()
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if code := get("/"); code != http.StatusOK {
		t.Fatalf("GET / = %d", code)
	}
	postIngest(t, srv, "/ingest/snapshot", mirror.Snapshot{
		RepoKey: "rk",
		Slug:    "probe",
		Stories: []json.RawMessage{[]byte(`{"id":"sty_a","title":"Hello"}`)},
	})
	if code := get("/r/probe/"); code != http.StatusOK {
		t.Fatalf("GET /r/probe/ after ingest = %d", code)
	}

	waitFor(t, waitBudget, func() bool { return asked.Load() >= 1 && p.dirtyCount() == 0 })
	if n := rec.count(); n != 0 {
		t.Fatalf("push off: %d push(es), want 0: %v", n, rec.snapshot())
	}
}
