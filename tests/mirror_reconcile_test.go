//go:build integration

package tests

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeReconcileRoute lands an ungated lifecycle: this test is about a dropped
// push, not about gates.
func writeReconcileRoute(t *testing.T, repo string) {
	t.Helper()
	writeSpineFixture(t, repo, "", "", "", "plan|executor|||", "done||||")
}

// TestMirrorReconcilesTerminalStateAfterServiceRestart proves AC2 of
// sty_e6e467fe end to end, with the real binary, the real snapshot and the real
// ingest: a story that reaches its terminal state while the web service is being
// restarted loses its final push, and the mirror is left showing the earlier
// frame — until the restarted service re-requests state on its own and the view
// shows the terminal state with NO operator action.
func TestMirrorReconcilesTerminalStateAfterServiceRestart(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init", "--harness", "claude")
	writeReconcileRoute(t, repo)
	mustRun(t, testBin, repo, "reindex")

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	host := fmt.Sprintf("http://127.0.0.1:%d", port)

	writeFile(t, filepath.Join(repo, ".satelle", "satelle.local.toml"),
		"[review]\ngate_create = false\n")

	// The service and the CLI share one home — the same machine, the same
	// per-repo database. (A service under a foreign home is refused a target by
	// design; see internal/serve.)
	home := isolatedHome(t)
	// Machine-scope endpoint (sty_21a7d16d): do not write [server] endpoint in
	// the repo. Pin the isolated home so an unset SATELLE_SERVER_ENDPOINT cannot
	// default to the operator's live :8787.
	writeFile(t, filepath.Join(home, "config.toml"),
		fmt.Sprintf("[service]\nendpoint = %q\nport = %d\n", host, port))
	var live *ServeHandle
	var serveOut strings.Builder
	startServe := func(reconcile bool) {
		env := []string{"SATELLE_HOME=" + home}
		for _, kv := range os.Environ() {
			// Drop any ambient off-switch: this test decides it per start.
			if !strings.HasPrefix(kv, "SATELLE_SERVER_ENDPOINT=") && !strings.HasPrefix(kv, "SATELLE_HOME=") {
				env = append(env, kv)
			}
		}
		if !reconcile {
			// The pre-fix behaviour, so the dropped push stays observable.
			env = append(env, "SATELLE_SERVER_ENDPOINT=none")
		} else {
			// Reconcile-on must target THIS test serve, never the live :8787 default.
			env = append(env, "SATELLE_SERVER_ENDPOINT="+host)
		}
		h := StartServeWithOutput(t, testBin, repo, env, &serveOut,
			"--addr", "127.0.0.1", "--port", fmt.Sprint(port))
		if !waitHealthy(t, host+"/healthz", 10*time.Second) {
			h.Stop()
			t.Fatal("serve never became healthy")
		}
		live = h
	}
	stopServe := func() {
		if live == nil {
			return
		}
		live.Stop()
		live = nil
	}
	t.Cleanup(stopServe)

	// --- a story mid-flight, mirrored ---
	out := mustRun(t, testBin, repo, "story", "create",
		"--title", "Reconcile Probe Story",
		"--body", "reaches done while the service restarts",
		"--acceptance", "1. mirrored at done",
		"--category", "chore",
	)
	id := extractID(out, "sty_")
	if id == "" {
		t.Fatalf("no story id in:\n%s", out)
	}
	mustRunEnv(t, testBin, repo, []string{"SATELLE_SERVER_ENDPOINT=" + host}, "story", "set", id, "--status", "plan")

	startServe(false)
	seedWorkspaceAdd(t, testBin, repo, host)
	// The story's own page: seeded tasks carry statuses of their own, so the
	// assertion must be about THIS story.
	storyURL := host + "/r/" + filepath.Base(repo) + "/story/" + id
	if page := httpGet(t, storyURL); !strings.Contains(page, "s-plan") {
		t.Fatalf("mirror should show the pre-terminal frame:\n%s", page)
	}

	// --- the release restart: the service is gone when the final push fires ---
	stopServe()
	mustRunEnv(t, testBin, repo, []string{"SATELLE_SERVER_ENDPOINT=" + host}, "story", "set", id, "--status", "done")
	got := mustRun(t, testBin, repo, "story", "get", id)
	if !strings.Contains(got, `"status": "done"`) {
		t.Fatalf("the repo database must hold done:\n%s", got)
	}

	// The mirror survived the restart holding the stale frame — this is the
	// defect the story reported, and it must still be observable when the
	// repair loop is off.
	startServe(false)
	stalePage := httpGet(t, storyURL)
	if !strings.Contains(stalePage, "s-plan") {
		t.Fatalf("push should have been dropped while the service was down:\n%s", stalePage)
	}
	stopServe()

	// --- the fix: the restarted service re-requests state by itself ---
	startServe(true)
	deadline := time.Now().Add(45 * time.Second)
	for {
		page := httpGet(t, storyURL)
		if strings.Contains(page, "s-done") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("mirror never reconciled to the terminal state.\nservice log:\n%s\npage:\n%s", serveOut.String(), page)
		}
		time.Sleep(250 * time.Millisecond)
	}
}
