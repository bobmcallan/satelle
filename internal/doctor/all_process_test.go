package doctor

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/health"
)

// skillFile is the smallest skill the authored-skill contract accepts.
func skillFile(name string) string {
	return "---\nname: " + name + "\nscope: project\ntype: skill\ntags: [type:skill]\ndescription: Fixture skill " + name + " for the doctor --all isolation test.\n---\n\nThis fixture skill exists only in one repository and has a plain prose rubric body.\n"
}

// gitInitCommit makes root a git repo with one commit, so a linked worktree can
// branch from it. The fixture's .satelle is committed with it.
func gitInitCommit(t *testing.T, root string) {
	t.Helper()
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "t@example.com"},
		{"config", "user.name", "t"},
		{"add", "-A"},
		{"commit", "-q", "-m", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

// skillRepo is a git repo whose workflow hook names a skill that ships ONLY in
// that repo. Judged by any other repo's process it is a consistency failure.
func skillRepo(t *testing.T, skill string) string {
	t.Helper()
	root := newFixtureRepo(t, fixtureOpts{
		workflowAdd: "create_review = \"" + skill + "\"\n",
		extraFiles:  map[string]string{"skills/" + skill + ".md": skillFile(skill)},
	})
	gitInitCommit(t, root)
	return root
}

// insideOpts are the opts the CLI builds when `doctor` runs inside root: that
// repo's own process, as app.Open resolves it.
func insideOpts(t *testing.T, root string) Opts {
	t.Helper()
	process, processRoot, _, err := config.LoadRepoProcess(root)
	if err != nil {
		t.Fatal(err)
	}
	return Opts{
		RepoRoot:    root,
		DataDir:     config.ResolveProcessDataDir(process, processRoot),
		Process:     &process,
		ProcessRoot: processRoot,
	}
}

func findingKeys(r Report) string {
	out := make([]string, 0, len(r.Findings))
	for _, f := range r.Findings {
		out = append(out, f.ID+"|"+string(f.Severity)+"|"+f.Title+"|"+f.Detail)
	}
	return strings.Join(out, "\n")
}

// sty_2e86c0cc AC1: `doctor --all` reports for each registered repo exactly what
// `doctor` reports inside it, whichever repo the sweep runs from.
func TestCheckAllJudgesEachRepoByItsOwnProcess(t *testing.T) {
	a := skillRepo(t, "a-only-check")
	b := skillRepo(t, "b-only-check")
	registerRepos(t, a, b)

	for _, r := range []string{a, b} {
		inside := Check(context.Background(), insideOpts(t, r))
		if bad := inside.Findings.Details(health.SeverityError); len(bad) != 0 {
			t.Fatalf("fixture %s must be healthy on its own, got %v", r, bad)
		}
	}

	for _, from := range []string{a, b} {
		reports := CheckAll(context.Background(), insideOpts(t, from))
		if len(reports) != 2 {
			t.Fatalf("from %s: want 2 reports, got %d", from, len(reports))
		}
		for i, root := range []string{a, b} {
			want := findingKeys(Check(context.Background(), insideOpts(t, root)))
			got := findingKeys(reports[i])
			if got != want {
				t.Errorf("--all from %s, repo %s:\n got  %s\n want %s", from, root, got, want)
			}
			if ids(reports[i])[health.IDWorkflowConsistency] || ids(reports[i])[health.IDProcessDivergent] {
				t.Errorf("--all from %s judged %s by another repo's process: %s", from, root, got)
			}
		}
	}
}

// sty_2e86c0cc AC2: a separate repository is never a worktree of the invoker.
func TestCheckAllSeparateRepoNotDivergent(t *testing.T) {
	a := skillRepo(t, "a-only-check")
	b := skillRepo(t, "b-only-check")
	registerRepos(t, a, b)

	reports := CheckAll(context.Background(), insideOpts(t, a))
	if ids(reports[1])[health.IDProcessDivergent] {
		t.Errorf("a separate repo must not be reported as a divergent worktree: %s", findingKeys(reports[1]))
	}
}

// sty_2e86c0cc AC2: the warning still fires for a REAL linked worktree of the
// same repository, run from an unrelated repo.
func TestCheckAllLinkedWorktreeStillDivergent(t *testing.T) {
	a := skillRepo(t, "a-only-check")
	b := skillRepo(t, "b-only-check")
	wt := filepath.Join(t.TempDir(), "wt")
	cmd := exec.Command("git", "worktree", "add", "-q", "-b", "wt-branch", wt, "HEAD")
	cmd.Dir = a
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git worktree add: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(wt, ".satelle", "skills", "a-only-check.md"),
		[]byte(skillFile("a-only-check")+"edited in the worktree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	registerRepos(t, a, wt, b)

	reports := CheckAll(context.Background(), insideOpts(t, b))
	if !ids(reports[1])[health.IDProcessDivergent] {
		t.Fatalf("a real linked worktree must still report divergence: %s", findingKeys(reports[1]))
	}
	for _, i := range []int{0, 2} {
		if ids(reports[i])[health.IDProcessDivergent] {
			t.Errorf("%s is the main tree of its own repo, not a divergent worktree: %s", reports[i].Repo, findingKeys(reports[i]))
		}
	}
}
