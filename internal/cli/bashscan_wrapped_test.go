package cli

import (
	"reflect"
	"strings"
	"testing"
)

// sty_ca667036 AC4: a command and the same command wrapped in `bash -lc '…'` get
// the same lock candidates, edit-gate verdict and foreign-tree targets: the lock
// judges what a command writes, not whether it was wrapped.
func TestWrappedCommandSameVerdict(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SATELLE_PROJECT_DIR", root)
	cfg := lockConfig{root: root, lockPaths: []string{".satelle/"}}
	const other = "/elsewhere/other-repo"
	for _, c := range []string{
		"echo x > " + other + "/f",
		"cat " + other + "/f",
		`satelle story create --title t --body "note .satelle/satelle.toml"`,
		`satelle story set sty_x --body "note .satelle/satelle.toml"`,
		`satelle story message sty_x --from a --to b --body "note .satelle/satelle.toml"`,
		"git status 2>&1",
		"ls </dev/null",
		"echo hi >&2",
		"satelle story list 2>&1-",
		"echo x > .satelle/satelle.toml",
		"sed -i s/a/b/ .satelle/workflows/x.toml",
	} {
		if strings.ContainsAny(c, "'`") || strings.Contains(c, "$(") {
			t.Fatalf("%q: a parity row must wrap into one single-quoted token", c)
		}
		wrapped := "bash -lc '" + c + "'"
		if _, ok := shellProgram(lastSegment(wrapped, root)); !ok {
			t.Errorf("%q: shellProgram must read the wrapped program, or the row tests the fallback", wrapped)
			continue
		}
		if bare, w := bashLockCandidates(c, cfg), bashLockCandidates(wrapped, cfg); !reflect.DeepEqual(bare, w) {
			t.Errorf("lock candidates differ:\n%q: %v\n%q: %v", c, bare, wrapped, w)
		}
		if bare, w := bashMutatesTreeWith(c, root, nil), bashMutatesTreeWith(wrapped, root, nil); bare != w {
			t.Errorf("edit-gate verdict differs: %q=%v, %q=%v", c, bare, wrapped, w)
		}
		if bare, w := mutationTargets(c, root), mutationTargets(wrapped, root); !reflect.DeepEqual(bare, w) {
			t.Errorf("foreign targets differ:\n%q: %v\n%q: %v", c, bare, wrapped, w)
		}
	}
}
