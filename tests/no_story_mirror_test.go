//go:build integration

package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoStoryMirrorWritten drives the real binary to prove the per-story markdown
// mirror is gone (sty_fa1e02e1): after creating and transitioning a story, no
// .satelle/stories/<id>.md file exists — the database is the sole story store —
// while the story is still readable from the DB.
func TestNoStoryMirrorWritten(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	stubReviewerAccept(t, repo) // baseline gates are active (sty_5b8bd8b2) — keep hermetic

	out := mustRun(t, testBin, repo, "story", "create",
		"--title", "No mirror please", "--body", "the goal", "--acceptance", "1. it does X")
	id := extractID(out, "sty_")
	if id == "" {
		t.Fatalf("no story id in create output:\n%s", out)
	}
	mustRun(t, testBin, repo, "story", "estimate", id, "--time", "10m") // the coded estimate gate enforces OOTB
	mustRun(t, testBin, repo, "story", "set", id, "--status", "in_progress")

	// No mirror file written by the transition.
	if _, err := os.Stat(filepath.Join(repo, ".satelle", "stories", id+".md")); !os.IsNotExist(err) {
		t.Errorf("a story mirror file exists at .satelle/stories/%s.md; the DB must be the sole story store", id)
	}
	// The story is still the DB's, at the new status.
	got := mustRun(t, testBin, repo, "story", "get", id)
	if !strings.Contains(got, `"status": "in_progress"`) {
		t.Errorf("story should be in the DB at in_progress:\n%s", got)
	}
}
