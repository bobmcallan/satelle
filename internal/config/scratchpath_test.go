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
