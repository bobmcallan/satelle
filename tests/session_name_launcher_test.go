//go:build integration

package tests

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestNamedLauncherCarriesTheSessionName (sty_88603414 AC3) drives the real
// binary end to end: from a linked worktree that holds a live seat, the
// installed satelle-claude launcher execs claude with `--name` equal to what
// `satelle session name` prints there, and that name carries the worktree
// directory and the engaged story. claude is a stub that records its argv.
func TestNamedLauncherCarriesTheSessionName(t *testing.T) {
	repo := t.TempDir()
	gitInitRepo(t, repo)
	mustRun(t, testBin, repo, "init")
	writeSpineFixture(t, repo, "", "", "", "in_progress|executor|||", "done||||")
	mustRun(t, testBin, repo, "reindex")
	setEngagementMode(t, repo, "epic")
	tree := addWorktree(t, repo)

	epic := extractID(mustRun(t, testBin, repo, "story", "create", "--title", "Container",
		"--body", "goal", "--acceptance", "1. engages", "--category", "chore"), "sty_")
	child := extractID(mustRun(t, testBin, repo, "story", "create", "--title", "Child",
		"--body", "goal", "--acceptance", "1. engages", "--category", "chore", "--parent", epic), "sty_")
	mustRun(t, testBin, tree, "story", "set", child, "--status", "in_progress")

	// The launcher lands under the isolated SATELLE_HOME; install from a scratch
	// directory so the hook scaffolds it also writes stay out of the repo.
	out := mustRun(t, testBin, t.TempDir(), "agents", "install", "claude")
	launcher := extractLauncherPath(out, "claude")
	if launcher == "" {
		t.Fatalf("install did not name the claude launcher:\n%s", out)
	}
	for _, want := range []string{launcher, filepath.Dir(launcher), "PATH"} {
		if !strings.Contains(out, want) {
			t.Errorf("install output should tell the operator %q:\n%s", want, out)
		}
	}

	stubs := t.TempDir()
	argvFile := filepath.Join(t.TempDir(), "argv")
	stub := "#!/bin/sh\nfor a in \"$@\"; do printf '%s\\n' \"$a\" >> \"" + argvFile + "\"; done\n"
	if err := os.WriteFile(filepath.Join(stubs, "claude"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	// The stub claude and the binary under test, ahead of any real claude/satelle.
	pathEnv := []string{"PATH=" + stubs + string(os.PathListSeparator) + filepath.Dir(testBin) + string(os.PathListSeparator) + os.Getenv("PATH")}

	want := strings.TrimSpace(mustRunEnv(t, testBin, tree, pathEnv, "session", "name"))
	for _, seg := range []string{filepath.Base(tree), child} {
		if !strings.Contains(want, seg) {
			t.Errorf("session name %q should carry %q", want, seg)
		}
	}

	cmd := exec.Command(launcher, "--resume", "x")
	cmd.Dir = tree
	cmd.Env = append(isolatedEnv(t), pathEnv...)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("launcher: %v\n%s", err, b)
	}
	raw, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	if len(got) != 4 || got[0] != "--name" || got[1] != want || got[2] != "--resume" || got[3] != "x" {
		t.Fatalf("claude received argv %q, want [--name %q --resume x]", got, want)
	}
}
