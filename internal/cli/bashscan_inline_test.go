package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// AC6: only the edit-gate classifier sees inline programs; the fence and the
// substrate lock candidates are unchanged (sty_5fac62b9).
func TestInlineInterpreterIsInvisibleToFenceAndLock(t *testing.T) {
	c := `cd /elsewhere && python3 -c 'print(1)'`
	if got := mutationTargets(c, inlineAnchor); len(got) != 0 {
		t.Errorf("mutationTargets(%q) = %v, want none", c, got)
	}
	if home, foreign := bashMutationTargets(`python3 -c "open('a.go','w')"`, inlineAnchor); len(home)+len(foreign) != 0 {
		t.Errorf("bashMutationTargets saw an inline program: %v %v", home, foreign)
	}
}

// AC6, hook level: the cross-repo fence neither sees nor refuses an inline program
// run from another repository's directory.
func TestFenceDoesNotRefuseInlineInterpreterInOtherRepo(t *testing.T) {
	fenceTempRootsElsewhere(t)
	repo := tempRepo(t)
	t.Chdir(repo)
	other := t.TempDir()
	if err := os.MkdirAll(filepath.Join(other, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	c := "cd " + other + " && python3 -c 'print(1)'"
	if got := mutationTargets(c, repo); len(got) != 0 {
		t.Errorf("mutationTargets(%q) = %v, want none", c, got)
	}
	for _, handler := range []string{"gate", "commitgate"} {
		if out, err := runRootIn(t, bashEvent(c), "hook", handler); err != nil {
			t.Errorf("%s refused %q: %v\n%s", handler, c, err, out)
		}
	}
}

// AC6: the substrate lock's candidates for an interpreter command are what they were.
func TestBashLockCandidatesInterpreterUnchanged(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SATELLE_PROJECT_DIR", root)
	cfg := lockConfig{root: root, lockPaths: []string{".satelle/"}}
	want := filepath.Join(root, ".satelle", "x")
	for _, tc := range []struct {
		command string
		want    []string
	}{
		{`python3 -c "open('.satelle/x','w')"`, []string{want}},
		{"python3 - <<'PY'\nopen('.satelle/x','w')\nPY", []string{want}},
		{`bash -c "echo hi > .satelle/x"`, []string{want}},
		{`python3 -c 'print(1)'`, nil},
		{"python3 tool.py", nil},
	} {
		got := bashLockCandidates(tc.command, cfg)
		if strings.Join(got, " ") != strings.Join(tc.want, " ") {
			t.Errorf("bashLockCandidates(%q) = %v, want %v", tc.command, got, tc.want)
		}
	}
}
