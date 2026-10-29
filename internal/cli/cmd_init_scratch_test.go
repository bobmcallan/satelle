package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/testutil"
)

// initScratchCase runs a real init of repo against a fresh SATELLE_HOME and
// returns its output and the registry it left behind.
func initScratchCase(t *testing.T, repo string) (string, []string) {
	t.Helper()
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := runInit(&out, repo, false, nil); err != nil {
		t.Fatalf("runInit: %v\n%s", err, out.String())
	}
	gc, err := config.LoadGlobal()
	if err != nil {
		t.Fatal(err)
	}
	return out.String(), gc.Workspace.Repos
}

// isolateScratchHome pins HOME/SATELLE_HOME to a fresh dir and TMPDIR to its own
// temp root, so the scratch root is <tmp>/satelle under this test alone.
func isolateScratchHome(t *testing.T) string {
	t.Helper()
	isolateUserHome(t)
	testutil.IsolateHome(t)
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	t.Setenv(config.ScratchEnv, "")
	return tmp
}

// sty_e3a1c1d5 AC1/AC2: init skips the machine registry for a repo inside the
// disposable scratch tree, and still registers one outside it.
func TestRunInitScratchRepoSkipsWorkspace(t *testing.T) {
	assertSkipped := func(t *testing.T, out string, repos []string) {
		t.Helper()
		if len(repos) != 0 {
			t.Fatalf("scratch repo must not be registered, registry = %v", repos)
		}
		if !strings.Contains(out, "skipped: scratch repo") {
			t.Errorf("init output missing scratch skip line:\n%s", out)
		}
		if !strings.Contains(out, "workspace: not-member") {
			t.Errorf("init output missing not-member line:\n%s", out)
		}
	}

	t.Run("scratch layout outside a dispatch", func(t *testing.T) {
		tmp := isolateScratchHome(t)
		repo := filepath.Join(tmp, "satelle", "abcdef012345", "sty_x", "1234abcd", "repo")
		out, repos := initScratchCase(t, repo)
		assertSkipped(t, out, repos)
	})

	t.Run("inside a dispatch", func(t *testing.T) {
		tmp := isolateScratchHome(t)
		dispatch := filepath.Join(tmp, "satelle", "f6e749e4e532", "sty_b48df715", "7f7eb63a")
		if err := os.MkdirAll(dispatch, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv(config.ScratchEnv, dispatch)
		t.Setenv("TMPDIR", dispatch)
		out, repos := initScratchCase(t, filepath.Join(dispatch, "repo"))
		assertSkipped(t, out, repos)
	})

	t.Run("outside the scratch root", func(t *testing.T) {
		isolateScratchHome(t)
		repo := t.TempDir()
		out, repos := initScratchCase(t, repo)
		abs, err := filepath.Abs(repo)
		if err != nil {
			t.Fatal(err)
		}
		if len(repos) != 1 || repos[0] != abs {
			t.Fatalf("registry = %v, want [%s]", repos, abs)
		}
		if !strings.Contains(out, "workspace: member") {
			t.Errorf("init output missing member line:\n%s", out)
		}
	})
}
