//go:build integration

package tests

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
)

// TestDefaultCwdIsOutsideAnyRepo (sty_5f3d3da6) pins the suite's default working
// directory: a satelle process started without choosing a directory runs outside
// any satelle repo, whichever checkout runs the suite. TestMain moves the test
// process into such a directory; a verb that behaves differently inside a repo
// would otherwise pass in a gate worktree (no .satelle) and fail from an
// operator checkout (which has one). Removing the chdir fails step 1 in every
// checkout and step 3 wherever the running checkout carries a .satelle.
func TestDefaultCwdIsOutsideAnyRepo(t *testing.T) {
	// 1. The test process itself sits outside the checkout and outside any repo.
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if reason := cwdInsideRepo(wd); reason != "" {
		t.Fatalf("suite working directory is inside a satelle repo: %s", reason)
	}

	// 2. Control: a directory the test sets up as a repo IS one, so step 3 is
	// not passing only because the binary can never find a repo.
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	if _, ok := config.FindDataDir(repo); !ok {
		t.Fatalf("control repo %s not recognised as a satelle repo after init", repo)
	}
	if out, err := run(t, testBin, repo, "story", "list"); err != nil {
		t.Fatalf("story list inside the control repo: %v\n%s", err, out)
	}

	// 3. A satelle process started without choosing a directory is not in a repo.
	cmd := exec.Command(testBin, "story", "list")
	cmd.Env = isolatedEnv(t)
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "not a satelle repo") {
		t.Fatalf("story list with no chosen directory should report it is not in a satelle repo; err=%v\n%s", err, out)
	}
}
