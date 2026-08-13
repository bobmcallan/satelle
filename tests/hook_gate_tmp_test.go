//go:build integration

package tests

import (
	"os"
	"path/filepath"
	"testing"
)

// TestHookGateTmpDraftAllowed (sty_e8e1879c AC1/AC4): after init, an out-of-tree
// /tmp draft is allowed with no performing story on both harness envelopes;
// in-repo product code stays denied even when the repo itself lives under /tmp.
func TestHookGateTmpDraftAllowed(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	draft := filepath.Join(os.TempDir(), "itest-epic-buildout", "c1-title.txt")
	if err := os.MkdirAll(filepath.Dir(draft), 0o755); err != nil {
		t.Fatal(err)
	}
	// The draft path must sit outside the test repo (repo is also under TempDir).
	if filepath.Dir(draft) == repo || filepath.Dir(filepath.Dir(draft)) == repo {
		t.Fatalf("draft %q must not live inside repo %q", draft, repo)
	}

	if !gateEvent(t, repo, draft) {
		t.Errorf("edit gate blocked out-of-tree %s (Claude envelope)", draft)
	}
	if !gateEventRaw(t, repo, `{"toolInput":{"file_path":"`+draft+`"}}`) {
		t.Errorf("edit gate blocked out-of-tree %s (Grok envelope)", draft)
	}

	code := filepath.Join(repo, "internal", "cli", "cmd_hook.go")
	if gateEvent(t, repo, code) {
		t.Error("edit gate allowed in-repo product path with no engaged story")
	}
}
