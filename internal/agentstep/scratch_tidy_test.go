package agentstep

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
)

// The tidy area (sty_d74e9b1b AC1) is a sibling of the per-dispatch scratch
// leaf, under the story-level scratch parent — so finishing the dispatch that
// created the debris does not take the tidied copy with it.
func TestTidyAreaOutlivesDispatchScratch(t *testing.T) {
	repo := t.TempDir()
	const story = "sty_tidyouts"
	dir, err := newScratch(repo, story)
	if err != nil {
		t.Fatal(err)
	}
	parent := config.StoryScratchDirIn(os.TempDir(), repo, story)
	t.Cleanup(func() { _ = os.RemoveAll(parent) })

	if filepath.Dir(dir) != parent {
		t.Fatalf("dispatch scratch %s is not a child of the story scratch parent %s", dir, parent)
	}
	// From inside the dispatch (TMPDIR == the dispatch dir) the tidy area must
	// still resolve to the story-level sibling newScratch's layout implies.
	t.Setenv(config.ScratchEnv, dir)
	t.Setenv("TMPDIR", dir)
	if got, want := config.TidyDir(repo, story), filepath.Join(parent, "tidy"); got != want {
		t.Fatalf("TidyDir from inside the dispatch = %s, want %s", got, want)
	}
	tidied := filepath.Join(parent, "tidy", "stray.txt")
	if err := os.MkdirAll(filepath.Dir(tidied), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tidied, []byte("kept\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	finishScratch(dir, false)

	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("dispatch scratch should be removed, stat err=%v", err)
	}
	if b, err := os.ReadFile(tidied); err != nil || string(b) != "kept\n" {
		t.Errorf("tidied file lost with the dispatch scratch: %v %q", err, b)
	}
}
