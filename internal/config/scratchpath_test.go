package config

import (
	"path/filepath"
	"strings"
	"testing"
)

// A tidy run from inside a dispatch (TMPDIR == SATELLE_SCRATCH == the dispatch
// dir) must still land in the story-level area, not inside the dispatch dir
// that finishScratch removes (sty_d74e9b1b AC1).
func TestTidyDirFromInsideDispatchIsStoryLevel(t *testing.T) {
	base := t.TempDir()
	repo := "/some/repo"
	story := filepath.Join(base, "satelle", ScratchRepoKey(repo), "sty_x")
	dispatch := filepath.Join(story, "1234-abcd")

	t.Setenv(ScratchEnv, dispatch)
	t.Setenv("TMPDIR", dispatch) // what a dispatched agent sees

	got := TidyDir(repo, "sty_x")
	if want := filepath.Join(story, "tidy"); got != want {
		t.Errorf("TidyDir = %s, want %s", got, want)
	}
	if strings.HasPrefix(got, dispatch+string(filepath.Separator)) {
		t.Errorf("TidyDir %s is inside the dispatch dir %s", got, dispatch)
	}
}

// The parent walk holds for the short dispatch layout, story and adhoc legs.
func TestScratchBaseShortLayouts(t *testing.T) {
	base := t.TempDir()
	repo := "/some/repo"
	key := ScratchRepoKey(repo)
	for _, leg := range []string{"sty_0123abcd", "adhoc-0123abcd"} {
		dispatch := filepath.Join(base, "satelle", key, leg, "89abcdef")
		t.Setenv(ScratchEnv, dispatch)
		if got := scratchBase(); got != base {
			t.Errorf("%s: scratchBase = %s, want %s", leg, got, base)
		}
		if got, want := StoryScratchDir(repo, leg), filepath.Dir(dispatch); got != want {
			t.Errorf("StoryScratchDir = %s, want %s", got, want)
		}
		if got, want := TidyDir(repo, leg), filepath.Join(filepath.Dir(dispatch), "tidy"); got != want {
			t.Errorf("TidyDir = %s, want %s", got, want)
		}
	}
}

func TestStoryScratchDirWithoutDispatchUsesTempDir(t *testing.T) {
	t.Setenv(ScratchEnv, "")
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	want := filepath.Join(tmp, "satelle", ScratchRepoKey("/r"), "sty_y")
	if got := StoryScratchDir("/r", "sty_y"); got != want {
		t.Errorf("got %s want %s", got, want)
	}
}

// IsScratchPath matches the scratch root and what lies under it, never a
// sibling that merely shares its name as a prefix (sty_e3a1c1d5).
func TestIsScratchPath(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv(ScratchEnv, "")
	t.Setenv("TMPDIR", tmp)
	dispatch := filepath.Join(tmp, "satelle", "abcdef012345", "sty_x", "1234abcd")
	if got, want := ScratchRoot(), filepath.Join(tmp, "satelle"); got != want {
		t.Fatalf("ScratchRoot = %s, want %s", got, want)
	}
	for p, want := range map[string]bool{
		filepath.Join(tmp, "satelle"):            true,
		filepath.Join(dispatch, "repo"):          true,
		filepath.Join(tmp, "satelle-other", "r"): false,
		filepath.Join(tmp, "repo"):               false,
		"/elsewhere/repo":                        false,
	} {
		if got := IsScratchPath(p); got != want {
			t.Errorf("IsScratchPath(%s) = %v, want %v", p, got, want)
		}
	}

	// Inside a dispatch TMPDIR is the dispatch dir; the root is recovered from
	// SATELLE_SCRATCH, so the dispatch's own repo still counts as scratch.
	t.Setenv(ScratchEnv, dispatch)
	t.Setenv("TMPDIR", dispatch)
	if !IsScratchPath(filepath.Join(dispatch, "repo")) {
		t.Errorf("in-dispatch repo %s must be scratch", filepath.Join(dispatch, "repo"))
	}
}
