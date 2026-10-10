package verb_test

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/testutil"
	"github.com/bobmcallan/satelle/internal/verb"
)

// Cutting a worktree from the trunk brings the trunk level with the remote first
// (sty_92337a13). Every case drives two clones of one bare remote: the pusher is
// another machine, the subject is the main tree the worktree is cut from.

type cutEnv struct {
	r   testutil.TrunkRepos
	out *bytes.Buffer
}

func wireCut(t *testing.T, cfg config.TrunkConfig) cutEnv {
	t.Helper()
	r := testutil.NewTrunkRepos(t)
	declare(t, r.Subject, config.WorktreeConfig{Branch: "work/{id}", Path: "../trees/{id}"})
	out := new(bytes.Buffer)
	verb.SetTrunkOutput(out)
	verb.SetTrunkConfig(cfg, r.Subject)
	return cutEnv{r: r, out: out}
}

func (e cutEnv) cut(t *testing.T, id, base string, extra ...string) (verb.WorktreeResult, error) {
	t.Helper()
	req := map[string]any{"id": id, "base": base}
	if len(extra) == 2 {
		req[extra[0]] = extra[1]
	}
	return openWT(t, req)
}

func (e cutEnv) publish(t *testing.T, file string) string {
	t.Helper()
	e.r.PublishFromPusher(t, file)
	return e.r.Git(t, e.r.Pusher, "rev-parse", "HEAD")
}

func (e cutEnv) mainSha(t *testing.T) string {
	t.Helper()
	return e.r.Git(t, e.r.Subject, "rev-parse", "refs/heads/main")
}

func (e cutEnv) treePath(id string) string {
	return filepath.Join(filepath.Dir(e.r.Subject), "trees", id)
}

// assertNothingCut: no worktree directory, no branch, and main where it was.
func (e cutEnv) assertNothingCut(t *testing.T, id, mainBefore string) {
	t.Helper()
	if wtExists(e.treePath(id)) {
		t.Errorf("a stopped cut created %s", e.treePath(id))
	}
	if out, err := execGit(e.r.Subject, "show-ref", "--verify", "--quiet", "refs/heads/work/"+id); err == nil {
		t.Errorf("a stopped cut created branch work/%s: %s", id, out)
	}
	if got := e.mainSha(t); got != mainBefore {
		t.Errorf("a stopped cut moved main: %s -> %s", mainBefore, got)
	}
}

func execGit(dir string, args ...string) (string, error) {
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// unresolve makes the trunk impossible to name: no remote-tracking HEAD, and a
// remote whose own HEAD points at nothing, so set-head --auto cannot restore it.
func (e cutEnv) unresolve(t *testing.T) {
	t.Helper()
	e.r.Git(t, e.r.Subject, "remote", "set-head", "origin", "--delete")
	e.r.Git(t, e.r.Remote, "symbolic-ref", "HEAD", "refs/heads/gone")
}

func TestWorktreeTrunkBaseIsBroughtLevelBeforeTheCut(t *testing.T) {
	e := wireCut(t, config.TrunkConfig{})
	for i, base := range []string{"main", "origin/main"} {
		from := e.mainSha(t)
		tip := e.publish(t, fmt.Sprintf("n%d.txt", i))
		id := fmt.Sprintf("sty_cut%d", i)
		res, err := e.cut(t, id, base)
		if err != nil {
			t.Fatalf("--base %s: %v", base, err)
		}
		if got := e.mainSha(t); got != tip {
			t.Errorf("--base %s: local main = %s, want the pusher's tip %s", base, got, tip)
		}
		if got := e.r.Head(t, res.Path); got != tip {
			t.Errorf("--base %s: worktree HEAD = %s, want %s", base, got, tip)
		}
		want := fmt.Sprintf("satelle: trunk fast-forwarded main by 1 commit(s) %s..%s", from[:8], tip[:8])
		if !strings.Contains(e.out.String(), want) {
			t.Errorf("--base %s: output %q lacks %q", base, e.out.String(), want)
		}
		if res.Trunk == nil || !res.Trunk.FastForwarded || res.Trunk.Behind != 1 {
			t.Errorf("--base %s: result carries trunk = %+v, want a fast-forward of 1", base, res.Trunk)
		}
		e.out.Reset()
	}
}

func TestWorktreeTrunkBaseSpellingsAllNameTheTrunk(t *testing.T) {
	e := wireCut(t, config.TrunkConfig{})
	for i, base := range []string{"main", "refs/heads/main", "origin/main", "refs/remotes/origin/main"} {
		tip := e.publish(t, fmt.Sprintf("s%d.txt", i))
		res, err := e.cut(t, fmt.Sprintf("sty_sp%d", i), base)
		if err != nil {
			t.Fatalf("--base %s: %v", base, err)
		}
		if got := e.r.Head(t, res.Path); got != tip {
			t.Errorf("--base %s: worktree HEAD = %s, want %s", base, got, tip)
		}
	}
}

// Each stopping state, for a trunk base, refuses before anything is created.
func TestWorktreeTrunkStopStatesCreateNothing(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, e cutEnv)
		want  []string
	}{
		{"diverged", func(t *testing.T, e cutEnv) {
			e.r.Commit(t, e.r.Subject, "mine.txt", "mine\n")
			e.publish(t, "theirs.txt")
		}, []string{"diverged: 1 ahead, 1 behind", "git pull --rebase origin main"}},
		{"ahead", func(t *testing.T, e cutEnv) {
			e.r.Commit(t, e.r.Subject, "mine.txt", "mine\n")
		}, []string{"1 unpushed", "push"}},
		{"dirty", func(t *testing.T, e cutEnv) {
			if err := os.WriteFile(filepath.Join(e.r.Subject, "seed.txt"), []byte("changed\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			e.publish(t, "theirs.txt")
		}, []string{"dirty tree on main", "stash"}},
		{"not moved", func(t *testing.T, e cutEnv) {
			e.r.Git(t, e.r.Subject, "checkout", "--quiet", "-b", "elsewhere")
			other := filepath.Join(filepath.Dir(e.r.Subject), "holder")
			e.r.Git(t, e.r.Subject, "worktree", "add", "--quiet", other, "main")
			e.publish(t, "theirs.txt")
		}, []string{"behind origin/main by 1, not moved", "holder"}},
		{"offline", func(t *testing.T, e cutEnv) {
			e.r.Git(t, e.r.Subject, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "missing.git"))
		}, []string{"fetch from origin failed"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := wireCut(t, config.TrunkConfig{})
			c.setup(t, e)
			before := e.mainSha(t)
			_, err := e.cut(t, "sty_stop", "main")
			if err == nil {
				t.Fatal("cut succeeded, want a stop")
			}
			for _, w := range c.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error %q lacks %q", err, w)
				}
			}
			e.assertNothingCut(t, "sty_stop", before)
		})
	}
}

func TestWorktreeTrunkSetHeadAutoRestoresTheTrunk(t *testing.T) {
	e := wireCut(t, config.TrunkConfig{})
	e.r.Git(t, e.r.Subject, "remote", "set-head", "origin", "--delete")
	tip := e.publish(t, "a.txt")
	res, err := e.cut(t, "sty_auto", "main")
	if err != nil {
		t.Fatal(err)
	}
	if got := e.r.Head(t, res.Path); got != tip || e.mainSha(t) != tip {
		t.Errorf("worktree HEAD = %s, main = %s, want both %s", got, e.mainSha(t), tip)
	}
	if got := e.r.Git(t, e.r.Subject, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); got != "origin/main" {
		t.Errorf("origin/HEAD = %q, want set-head --auto to have restored origin/main", got)
	}
}

func TestWorktreeTrunkUnresolvedWithoutAHintStopsEveryCut(t *testing.T) {
	e := wireCut(t, config.TrunkConfig{})
	e.unresolve(t)
	e.r.Git(t, e.r.Subject, "branch", "dep/x")
	before := e.mainSha(t)
	for i, base := range []string{"main", "dep/x"} {
		id := fmt.Sprintf("sty_un%d", i)
		_, err := e.cut(t, id, base)
		want := fmt.Sprintf("satelle: trunk unresolved — cannot tell whether %s is the trunk; pass --trunk-branch <name> or run git remote set-head origin --auto", base)
		if err == nil || err.Error() != want {
			t.Fatalf("--base %s: error = %v, want %q", base, err, want)
		}
		if strings.Contains(err.Error(), " --branch") {
			t.Errorf("message tells the operator to pass --branch: %v", err)
		}
		e.assertNothingCut(t, id, before)
	}
}

func TestWorktreeTrunkUnresolvedWithAHintSyncsTheHintedBranch(t *testing.T) {
	for _, viaConfig := range []bool{false, true} {
		t.Run(fmt.Sprintf("config=%v", viaConfig), func(t *testing.T) {
			cfg := config.TrunkConfig{}
			if viaConfig {
				cfg.Branch = "main"
			}
			e := wireCut(t, cfg)
			e.unresolve(t)
			e.r.Git(t, e.r.Subject, "branch", "dep/x")
			for i, base := range []string{"main", "refs/heads/main", "origin/main", "refs/remotes/origin/main"} {
				tip := e.publish(t, fmt.Sprintf("h%d.txt", i))
				var extra []string
				if !viaConfig {
					extra = []string{"trunk_branch", "main"}
				}
				res, err := e.cut(t, fmt.Sprintf("sty_h%d", i), base, extra...)
				if err != nil {
					t.Fatalf("--base %s: %v", base, err)
				}
				if got := e.r.Head(t, res.Path); got != tip {
					t.Errorf("--base %s: worktree HEAD = %s, want %s", base, got, tip)
				}
			}
			e.out.Reset()
			var extra []string
			if !viaConfig {
				extra = []string{"trunk_branch", "main"}
			}
			res, err := e.cut(t, "sty_dep", "dep/x", extra...)
			if err != nil {
				t.Fatalf("dependency base: %v", err)
			}
			if res.Trunk != nil || e.out.Len() != 0 {
				t.Errorf("dependency base ran a trunk check: %+v / %q", res.Trunk, e.out.String())
			}
		})
	}
}

// A repo that lists a state out of base_refuse lets that cut proceed, with the
// line printed.
func TestWorktreeTrunkBaseRefuseOverrideLetsTheCutProceed(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, e cutEnv)
		line  string
	}{
		{"unresolved", func(t *testing.T, e cutEnv) { e.unresolve(t) }, "satelle: trunk unresolved:"},
		{"offline", func(t *testing.T, e cutEnv) {
			e.r.Git(t, e.r.Subject, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "missing.git"))
		}, "satelle: trunk fetch from origin failed"},
		{"ahead", func(t *testing.T, e cutEnv) { e.r.Commit(t, e.r.Subject, "mine.txt", "mine\n") }, "satelle: trunk 1 unpushed"},
		{"diverged", func(t *testing.T, e cutEnv) {
			e.r.Commit(t, e.r.Subject, "mine.txt", "mine\n")
			e.publish(t, "theirs.txt")
		}, "satelle: trunk diverged: 1 ahead, 1 behind"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := wireCut(t, config.TrunkConfig{BaseRefuse: &[]string{"dirty"}})
			c.setup(t, e)
			res, err := e.cut(t, "sty_ov", "main")
			if err != nil {
				t.Fatalf("overridden cut: %v", err)
			}
			if got := e.r.Head(t, res.Path); got != e.mainSha(t) {
				t.Errorf("worktree HEAD = %s, want local main %s", got, e.mainSha(t))
			}
			if !strings.Contains(e.out.String(), c.line) {
				t.Errorf("output %q lacks %q", e.out.String(), c.line)
			}
		})
	}
}

func TestWorktreeTrunkLevelCutPrintsNothing(t *testing.T) {
	e := wireCut(t, config.TrunkConfig{})
	res, err := e.cut(t, "sty_level", "main")
	if err != nil {
		t.Fatal(err)
	}
	if e.out.Len() != 0 {
		t.Errorf("a level cut printed %q", e.out.String())
	}
	if res.Trunk == nil || res.Trunk.State != "level" {
		t.Errorf("trunk = %+v, want the level report", res.Trunk)
	}
	if got := e.r.Head(t, res.Path); got != e.mainSha(t) {
		t.Errorf("worktree HEAD = %s, want main %s", got, e.mainSha(t))
	}
}

// A base that is not the trunk is cut as named: no fetch, no report, main
// untouched even though the remote has moved.
func TestWorktreeNonTrunkBaseIsCutAsNamed(t *testing.T) {
	e := wireCut(t, config.TrunkConfig{})
	e.r.Git(t, e.r.Subject, "branch", "epic/x")
	epic := e.r.Git(t, e.r.Subject, "rev-parse", "epic/x")
	e.publish(t, "moved.txt")
	before := e.mainSha(t)
	res, err := e.cut(t, "sty_epic", "epic/x")
	if err != nil {
		t.Fatal(err)
	}
	if res.Trunk != nil || e.out.Len() != 0 {
		t.Errorf("a non-trunk base ran a check: %+v / %q", res.Trunk, e.out.String())
	}
	if e.mainSha(t) != before {
		t.Error("a non-trunk cut moved main")
	}
	if got := e.r.Head(t, res.Path); got != epic {
		t.Errorf("worktree HEAD = %s, want the epic tip %s", got, epic)
	}
}

func TestWorktreeTrunkCheckOffSwitches(t *testing.T) {
	off := false
	t.Run("check = false", func(t *testing.T) {
		e := wireCut(t, config.TrunkConfig{Check: &off})
		before := e.mainSha(t)
		e.publish(t, "moved.txt")
		res, err := e.cut(t, "sty_off", "main")
		if err != nil {
			t.Fatal(err)
		}
		if res.Trunk != nil || e.out.Len() != 0 || e.mainSha(t) != before {
			t.Errorf("check = false still checked: %+v / %q", res.Trunk, e.out.String())
		}
		if got := e.r.Head(t, res.Path); got != before {
			t.Errorf("worktree HEAD = %s, want the stale main %s", got, before)
		}
	})
	t.Run("--existing", func(t *testing.T) {
		e := wireCut(t, config.TrunkConfig{})
		first, err := e.cut(t, "sty_ex", "main")
		if err != nil {
			t.Fatal(err)
		}
		e.out.Reset()
		e.publish(t, "moved.txt")
		before := e.mainSha(t)
		res, err := openWT(t, map[string]any{"id": "sty_ex", "existing": first.Path})
		if err != nil {
			t.Fatal(err)
		}
		if res.Trunk != nil || e.out.Len() != 0 || e.mainSha(t) != before {
			t.Errorf("--existing ran a check: %+v / %q", res.Trunk, e.out.String())
		}
	})
	t.Run("no remote", func(t *testing.T) {
		main := wtFixture(t)
		declare(t, main, config.WorktreeConfig{Branch: "work/{id}", Path: "../trees/{id}"})
		out := new(bytes.Buffer)
		verb.SetTrunkOutput(out)
		res, err := openWT(t, map[string]any{"id": "sty_nr", "base": "main"})
		if err != nil {
			t.Fatal(err)
		}
		if res.Trunk != nil || out.Len() != 0 {
			t.Errorf("a repo with no remote ran a check: %+v / %q", res.Trunk, out.String())
		}
	})
}
