package cli

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/testutil"
)

// The id4me sty_d9904629 shape (sty_659c4133): a substrate story engaged from a main
// tree that fast-forwards over another story's merge, whose own work is only the
// gitignored .satelle/. The embedded satelle-substrate-only-check runs as the gate
// runs it — a script that shells out to `satelle story diff` — against a satelle
// binary built from this tree, so the change set it reads is the one under test.
func TestSubstrateOnlyCheckAfterFastForwardOverForeignCommit(t *testing.T) {
	script := embeddedCheckScript(t, "satelle-substrate-only-check")
	bin := buildSatelle(t)

	foreignPaths := []string{
		".github/workflows/ci.yml", "Makefile", "README.md",
		"scripts/ci-proven-tree.sh", "scripts/ci-proven-tree_test.sh", ".version",
	}
	// setup engages a story, lands a commit touching foreignPaths under the subject
	// the case gives it, fast-forwards the tree over it, and has the story edit only
	// the gitignored .satelle/.
	setup := func(t *testing.T, foreignSubject func(id string) string) (repo, id string) {
		repo, r, id := trunkEngageRepo(t, "")
		if out, err := runRoot(t, "story", "set", id, "--status", "in_progress"); err != nil {
			t.Fatalf("engage: %v\n%s", err, out)
		}
		r.Git(t, r.Pusher, "pull", "--quiet", "--ff-only")
		for _, p := range foreignPaths {
			abs := filepath.Join(r.Pusher, p)
			if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(abs, []byte(p+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		r.Git(t, r.Pusher, "add", "-A")
		r.Git(t, r.Pusher, "commit", "--quiet", "-m", foreignSubject(id))
		r.Git(t, r.Pusher, "push", "--quiet", "origin", "main")
		r.Git(t, repo, "pull", "--quiet", "--ff-only")

		skill := filepath.Join(repo, ".satelle", "skills", "x.md")
		if err := os.MkdirAll(filepath.Dir(skill), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(skill, []byte("a lane path\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return repo, id
	}

	t.Run("another story's commit is not this story's change", func(t *testing.T) {
		repo, id := setup(t, func(string) string { return "feat: proven tree (sty_other)" })
		out, code := runCheck(t, script, bin, repo, id)
		if code != 0 {
			t.Fatalf("a substrate story whose only own change is .satelle/ was refused (exit %d):\n%s", code, out)
		}
		if !strings.Contains(out, ".satelle/skills/x.md") {
			t.Errorf("the check must have seen the story's own lane path:\n%s", out)
		}
	})

	t.Run("a commit naming the story is its change", func(t *testing.T) {
		repo, id := setup(t, func(id string) string { return "feat: proven tree (" + id + ")" })
		out, code := runCheck(t, script, bin, repo, id)
		if code == 0 || !strings.Contains(out, "Makefile") {
			t.Fatalf("a non-lane commit naming the story must be refused naming Makefile (exit %d):\n%s", code, out)
		}
	})

	t.Run("the story's own non-lane commit is its change", func(t *testing.T) {
		repo, id := setup(t, func(string) string { return "feat: proven tree (sty_other)" })
		r := testutil.TrunkRepos{}
		r.Git(t, repo, "commit", "--quiet", "--allow-empty", "-m", "noop")
		if err := os.WriteFile(filepath.Join(repo, "mine.go"), []byte("package x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		r.Git(t, repo, "add", "mine.go")
		r.Git(t, repo, "commit", "--quiet", "-m", "fix: mine ("+id+")")
		out, code := runCheck(t, script, bin, repo, id)
		if code == 0 || !strings.Contains(out, "mine.go") || strings.Contains(out, "Makefile") {
			t.Fatalf("only the story's own non-lane file may be named (exit %d):\n%s", code, out)
		}
	})
}

// embeddedCheckScript is the ```check block of an embedded skill.
func embeddedCheckScript(t *testing.T, name string) string {
	t.Helper()
	for _, d := range config.EmbeddedDefaults() {
		if d.Kind == "skills" && d.Name == name {
			m := regexp.MustCompile("(?s)```check\n(.*?)\n```").FindStringSubmatch(d.Body)
			if m == nil {
				t.Fatalf("%s has no check block", name)
			}
			return m[1]
		}
	}
	t.Fatalf("embedded %s not found", name)
	return ""
}

// buildSatelle builds the satelle binary of this tree into a temp dir and returns
// that dir, to be put first on the check's PATH.
func buildSatelle(t *testing.T) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(file), "..", "..")
	bin := t.TempDir()
	cmd := exec.Command("go", "build", "-o", filepath.Join(bin, "satelle"), "./cmd/satelle")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build ./cmd/satelle: %v\n%s", err, out)
	}
	return bin
}

// runCheck runs script in repo with the built binary first on PATH and the
// transition payload on stdin; it returns the combined output and the exit code.
func runCheck(t *testing.T, script, binDir, repo, id string) (string, int) {
	t.Helper()
	cmd := exec.Command("bash", "-c", script)
	cmd.Dir = repo
	cmd.Stdin = strings.NewReader(`{"id":"` + id + `"}`)
	cmd.Env = append(os.Environ(), "PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	out, err := cmd.CombinedOutput()
	var ee *exec.ExitError
	switch {
	case err == nil:
		return string(out), 0
	case errors.As(err, &ee):
		return string(out), ee.ExitCode()
	default:
		t.Fatalf("run check: %v", err)
		return "", -1
	}
}
