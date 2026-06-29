//go:build integration

package tests

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestReviewerCanResolveEmbeddedSkillViaCLI drives the real binary to prove the
// capability a reviewer now relies on (sty_e15c15a4): with the reviewer harness
// granting scoped, read-only `satelle` CLI access, a reviewer can resolve a skill
// that exists ONLY in the embedded layer — invisible to Read/Grep/Glob because it
// is not a file on disk. The reviewer itself is an LLM subprocess (not driven
// here); this asserts the deterministic CLI path it uses: `satelle doc get skills
// <embedded-only-name>` resolves in a fresh repo where no such file exists.
func TestReviewerCanResolveEmbeddedSkillViaCLI(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	run := func(args ...string) (string, error) {
		cmd := exec.Command(testBin, args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "SATELLE_HOME="+home)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	if out, err := run("index"); err != nil {
		t.Fatalf("index: %v\n%s", err, out)
	}

	// satelle-step-summary is embedded canonical substrate; a fresh repo has no
	// file for it on disk. (The structure reviewers were retired in sty_a90d5c49,
	// so the step summariser is the remaining embedded-only skill to probe.)
	const embedded = "satelle-step-summary"
	if _, err := os.Stat(filepath.Join(repo, ".satelle", "skills", embedded+".md")); !os.IsNotExist(err) {
		t.Fatalf("precondition: %s should NOT exist on disk in a fresh repo", embedded)
	}

	// The CLI resolves it from the embedded layer — the read-only path used to
	// confirm an embedded-only skill is reachable.
	out, err := run("doc", "get", "skills", embedded)
	if err != nil {
		t.Fatalf("`satelle doc get skills %s` should resolve the embedded skill, got error: %v\n%s", embedded, err, out)
	}
	if !strings.Contains(out, embedded) || !strings.Contains(out, "per-transition summariser") {
		t.Errorf("resolved doc should be the embedded step-summary body:\n%s", out)
	}
}
