package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// countGitSpawns puts a `git` shim first on PATH that logs each invocation's
// arguments and then runs the real git, and returns a func reporting the logged
// invocations. It counts spawns without a seam in non-test code.
func countGitSpawns(t *testing.T) func() []string {
	t.Helper()
	real, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "spawns.log")
	shim := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + log + "\nexec " + real + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(shim), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func() []string {
		b, err := os.ReadFile(log)
		if err != nil {
			return nil
		}
		return strings.Split(strings.TrimSpace(string(b)), "\n")
	}
}

// AC5: one hook call loads the process config once. Loading it resolves the
// canonical repo root through `git rev-parse --git-common-dir`, so rule
// evaluation must share the containment check's load rather than make its own:
// the spawn count is one with rules authored, and one with none. SATELLE_CONFIG
// is cleared because, when set, the load never resolves a root and spawns
// nothing, which would make the count vacuous.
//
// Only commands that read no seat are counted: a rule that matches an allow list
// opens the store, which resolves its own identity — a separate, matched-command
// cost, not the config load this test pins.
func TestCommitgateLoadsConfigOncePerCall(t *testing.T) {
	const denyEverywhere = "\n[[gate.command_rule]]\npattern = \"orbctl nuke\"\naction = \"deny\"\nmessage = \"never\"\n"
	for _, tc := range []struct {
		name, rules, command string
	}{
		{"no rules", "", "ls"},
		{"rules, no match", exampleRules(t), "ls"},
		{"rules, deny match without allow list", denyEverywhere, "orbctl nuke"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo, _ := ruleRepo(t, seatMine, tc.rules)
			if out, err := exec.Command("git", "-C", repo, "init", "-q").CombinedOutput(); err != nil {
				t.Fatalf("git init: %v\n%s", err, out)
			}
			t.Setenv("SATELLE_CONFIG", "")
			spawns := countGitSpawns(t)
			_, _ = commitgateOn(t, "claude", tc.command) // a deny is an error; the count is what is judged
			var loads []string
			for _, s := range spawns() {
				if strings.Contains(s, "--git-common-dir") {
					loads = append(loads, s)
				}
			}
			if len(loads) != 1 {
				t.Errorf("%q started %d `git rev-parse --git-common-dir` (want 1, one config load): %q",
					tc.command, len(loads), loads)
			}
		})
	}
}
