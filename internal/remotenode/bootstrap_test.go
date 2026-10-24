//go:build !windows

package remotenode

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func sh(script string) []string { return []string{"sh", "-c", script} }

// Each bootstrap command runs once, in order, in dir, with no stdin.
func TestRunBootstrapRunsEachCommandInOrder(t *testing.T) {
	dir := t.TempDir()
	cmds := [][]string{
		sh("echo first >> order; cat > stdin-1"),
		sh("echo second >> order; pwd > cwd"),
	}
	if err := RunBootstrap(context.Background(), dir, cmds, time.Minute, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "order")); string(got) != "first\nsecond\n" {
		t.Errorf("order = %q, want each command once, in order", got)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "stdin-1")); len(got) != 0 {
		t.Errorf("a command read stdin %q, want none", got)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "cwd")); strings.TrimSpace(string(got)) == "" {
		t.Error("the second command did not run in dir")
	}
}

// A failing command stops the run at its 1-based index; later ones never run.
func TestRunBootstrapStopsAtTheFailingCommand(t *testing.T) {
	dir := t.TempDir()
	cmds := [][]string{sh("echo one >> ran"), sh("exit 3"), sh("echo three >> ran")}
	err := RunBootstrap(context.Background(), dir, cmds, time.Minute, nil)
	var be *BootstrapError
	if !errors.As(err, &be) || be.Index != 2 || be.Reason != "exit status 3" {
		t.Fatalf("err = %v, want BootstrapError at 2 with exit status 3", err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "ran")); string(got) != "one\n" {
		t.Errorf("ran = %q, want only the first command", got)
	}
}

func TestRunBootstrapTimesOut(t *testing.T) {
	start := time.Now()
	err := RunBootstrap(context.Background(), t.TempDir(), [][]string{sh("sleep 5")}, 50*time.Millisecond, nil)
	var be *BootstrapError
	if !errors.As(err, &be) || be.Index != 1 || !strings.HasPrefix(be.Reason, "timed out") {
		t.Fatalf("err = %v, want a timeout at command 1", err)
	}
	if time.Since(start) > 4*time.Second {
		t.Error("the timeout did not bound the command")
	}
}

// The error carries neither the argv, nor the command's output, nor the exec error.
func TestBootstrapErrorHoldsNeitherArgvNorOutput(t *testing.T) {
	var log bytes.Buffer
	for _, argv := range [][]string{
		sh("echo OUT-SENTINEL; echo ERR-SENTINEL >&2; exit 1 # ARGV-SENTINEL"),
		{"/nonexistent/ARGV-SENTINEL"},
		{},
	} {
		err := RunBootstrap(context.Background(), t.TempDir(), [][]string{argv}, time.Minute, &log)
		if err == nil {
			t.Fatalf("%v: want a failure", argv)
		}
		for _, s := range []string{"SENTINEL", "/nonexistent", "sh"} {
			if strings.Contains(err.Error(), s) {
				t.Errorf("%v: error %q holds %q", argv, err, s)
			}
		}
	}
	if !strings.Contains(log.String(), "OUT-SENTINEL") || !strings.Contains(log.String(), "ERR-SENTINEL") {
		t.Errorf("the command's output did not go to the node log: %q", log.String())
	}
}

func TestRunBootstrapWithNoCommandsRunsNothing(t *testing.T) {
	for _, cmds := range [][][]string{nil, {}} {
		if err := RunBootstrap(context.Background(), t.TempDir(), cmds, time.Minute, nil); err != nil {
			t.Errorf("RunBootstrap(%v) = %v, want nil", cmds, err)
		}
	}
}
