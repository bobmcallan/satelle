//go:build integration

package tests

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// reconcileWorkflow is an ungated lifecycle: this test is about a dropped push,
// not about gates.
const reconcileWorkflow = `---
name: reconcile-probe-wf
type: workflow
scope: project
description: ungated lifecycle for the mirror reconcile probe
applies_to: ["*"]
---

` + "```dot" + `
digraph w {
  backlog [shape=Mdiamond]
  plan        [agent=executor]
  done        [shape=Msquare]
  backlog -> plan -> done
}
` + "```" + `
`

// TestMirrorReconcilesTerminalStateAfterServiceRestart proves AC2 of
// sty_e6e467fe end to end, with the real binary, the real snapshot and the real
// ingest: a story that reaches its terminal state while the web service is being
// restarted loses its final push, and the mirror is left showing the earlier
// frame — until the restarted service re-requests state on its own and the view
// shows the terminal state with NO operator action.
func TestMirrorReconcilesTerminalStateAfterServiceRestart(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init", "--harness", "claude")
	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "reconcile-probe-wf.md"), reconcileWorkflow)
	mustRun(t, testBin, repo, "reindex")

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	host := fmt.Sprintf("http://127.0.0.1:%d", port)

	writeFile(t, filepath.Join(repo, ".satelle", "satelle.local.toml"),
		fmt.Sprintf("[review]\ngate_create = false\n\n[server]\nendpoint = %q\n", host))

	// The service and the CLI share one home — the same machine, the same
	// per-repo database. (A service under a foreign home is refused a target by
	// design; see internal/serve.)
	home := isolatedHome(t)
	var live *exec.Cmd
	var serveOut strings.Builder
	startServe := func(reconcile bool) {
		cmd := exec.Command(testBin, "serve", "--addr", "127.0.0.1", "--port", fmt.Sprint(port))
		cmd.Dir = repo
		cmd.Stdout = &serveOut
		cmd.Stderr = &serveOut
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
		}
		cmd.Env = env
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		if !waitHealthy(t, host+"/healthz", 10*time.Second) {
			_ = cmd.Process.Kill()
			t.Fatal("serve never became healthy")
		}
		live = cmd
	}
	stopServe := func() {
		if live == nil {
			return
		}
		_ = live.Process.Kill()
		_, _ = live.Process.Wait()
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
