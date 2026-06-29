//go:build integration

package tests

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestLocalBinaryReexec drives the real binary's repo-local precedence
// (sty_fe3ee313): with a .satelle/satelle pin present, the globally-invoked
// satelle re-execs the pin; the loop-guard env marker suppresses that
// (so the in-process binary runs). The pin is a tiny script that prints a
// recognisable marker, so the test can tell which binary actually ran.
func TestLocalBinaryReexec(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".satelle"), 0o755); err != nil {
		t.Fatal(err)
	}
	pin := filepath.Join(repo, ".satelle", "satelle")
	if err := os.WriteFile(pin, []byte("#!/bin/sh\necho LOCAL-PIN-RAN\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(repo, "nested")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	// From inside the repo, the global binary must re-exec the pin.
	cmd := exec.Command(testBin, "version")
	cmd.Dir = sub
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("version (should re-exec pin): %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "LOCAL-PIN-RAN") {
		t.Errorf("expected the repo-local pin to run, got:\n%s", out)
	}

	// With the loop-guard marker set, the in-process binary runs (no re-exec).
	cmd = exec.Command(testBin, "version")
	cmd.Dir = sub
	cmd.Env = append(os.Environ(), "SATELLE_LOCAL_EXEC=1")
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("version (guard set): %v\n%s", err, out)
	}
	if strings.Contains(string(out), "LOCAL-PIN-RAN") {
		t.Errorf("loop guard should suppress re-exec, but the pin ran:\n%s", out)
	}
	if !strings.Contains(string(out), "satelle ") {
		t.Errorf("expected the real satelle version line, got:\n%s", out)
	}
}
