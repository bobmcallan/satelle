//go:build integration

package tests

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestHermeticWorkspaceAddDoesNotPoisonLiveDefaultPort proves sty_5aa08259 AC1
// with a suite-owned decoy serve whose mirror is fingerprinted: hermetic
// workspace add from a different SATELLE_HOME must not seed that decoy
// (SATELLE_SERVER_ENDPOINT=none and instance-identity mismatch).
//
// Full-suite AC1 is also enforced by TestMain's host mirror partition-key
// fingerprint when a live operator serve has written ~/.satelle/serve/mirror.db.
func TestHermeticWorkspaceAddDoesNotPoisonLiveDefaultPort(t *testing.T) {
	decoyHome := t.TempDir()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	if err := os.WriteFile(filepath.Join(decoyHome, "config.toml"),
		[]byte(fmt.Sprintf("[service]\nport = %d\n", port)), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(testBin, "serve", "--addr", "127.0.0.1", "--port", fmt.Sprint(port))
	cmd.Env = append(os.Environ(), "SATELLE_HOME="+decoyHome)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		done := make(chan struct{})
		go func() { _, _ = cmd.Process.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = cmd.Process.Kill()
		}
	})
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	if !waitHealthy(t, base+"/healthz", 8*time.Second) {
		t.Fatal("decoy serve not healthy")
	}

	before := captureMirrorPartitionKeys(decoyHome)

	attackerHome := t.TempDir()
	repo := filepath.Join(t.TempDir(), "poison-me")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	initCmd := exec.Command(testBin, "init")
	initCmd.Dir = repo
	initCmd.Env = append(os.Environ(), "SATELLE_HOME="+attackerHome, "SATELLE_SERVER_ENDPOINT=none")
	if out, err := initCmd.CombinedOutput(); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	// Pin attacker's service port to the decoy so auto-bootstrap would probe it.
	if err := os.WriteFile(filepath.Join(attackerHome, "config.toml"),
		[]byte(fmt.Sprintf("[service]\nport = %d\n", port)), 0o644); err != nil {
		t.Fatal(err)
	}

	// Attack 1: suite-style none.
	add1 := exec.Command(testBin, "workspace", "add")
	add1.Dir = repo
	add1.Env = append(os.Environ(), "SATELLE_HOME="+attackerHome, "SATELLE_SERVER_ENDPOINT=none")
	out1, err1 := add1.CombinedOutput()
	if err1 != nil {
		t.Fatalf("workspace add (none): %v\n%s", err1, out1)
	}
	if !strings.Contains(string(out1), "seed skipped") && !strings.Contains(string(out1), "already registered") {
		if strings.Contains(string(out1), "workspace add: ok") {
			t.Fatalf("none must not seed:\n%s", out1)
		}
	}

	// Attack 2: no off-switch — instance identity alone must refuse the decoy.
	add2 := exec.Command(testBin, "workspace", "add")
	add2.Dir = repo
	add2.Env = append(os.Environ(), "SATELLE_HOME="+attackerHome)
	out2, err2 := add2.CombinedOutput()
	if err2 != nil {
		t.Fatalf("workspace add (identity): %v\n%s", err2, out2)
	}
	if strings.Contains(string(out2), "workspace add: ok") || strings.Contains(string(out2), "ingest/snapshot") {
		t.Fatalf("must not seed foreign decoy via auto-bootstrap:\n%s", out2)
	}
	if !strings.Contains(string(out2), "seed skipped") {
		t.Fatalf("expected seed skipped for foreign instance:\n%s", out2)
	}

	after := captureMirrorPartitionKeys(decoyHome)
	if diffs := diffMirrorPartitionKeys(before, after); len(diffs) > 0 {
		t.Fatalf("decoy mirror poisoned: %v\nbefore=%v after=%v\nout1=%s\nout2=%s",
			diffs, before, after, out1, out2)
	}
	resp, err := http.Get(base + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
}
