package testutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TrunkRepos is one bare remote with two clones of its trunk, the shape every
// trunk-sync test needs: Pusher stands for another machine that publishes to
// the remote, Subject for the machine under test (sty_9f3e51d1). Both start
// level with the remote. Every path is a local directory; nothing touches the
// network.
type TrunkRepos struct {
	Remote  string
	Pusher  string
	Subject string
}

// NewTrunkRepos builds the fixture on a trunk named "main". It pins git to an
// empty global configuration for the test, so a developer's own settings (signing,
// hooks, default branch) cannot change what the fixture does.
func NewTrunkRepos(t testing.TB) TrunkRepos {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	root := t.TempDir()
	r := TrunkRepos{
		Remote:  filepath.Join(root, "remote.git"),
		Pusher:  filepath.Join(root, "pusher"),
		Subject: filepath.Join(root, "subject"),
	}
	r.Git(t, root, "init", "--bare", "--quiet", "-b", "main", r.Remote)
	// A force push that rewrites published history is refused by the remote
	// itself, so a mechanism that forced would fail its test, not merely be
	// caught by a log.
	r.Git(t, r.Remote, "config", "receive.denyNonFastForwards", "true")
	r.Git(t, root, "init", "--quiet", "-b", "main", r.Pusher)
	r.identify(t, r.Pusher)
	r.Git(t, r.Pusher, "remote", "add", "origin", r.Remote)
	r.Commit(t, r.Pusher, "seed.txt", "seed\n")
	r.Git(t, r.Pusher, "push", "--quiet", "-u", "origin", "main")
	r.Git(t, root, "clone", "--quiet", r.Remote, r.Subject)
	r.identify(t, r.Subject)
	return r
}

// Adopt turns an existing directory into the subject clone — for a test that
// needs the checkout under test to carry other files already, such as a repo's
// .satelle. It returns the fixture with Subject set to dir. The directory's
// .satelle is excluded from git, so it never makes the tree dirty.
func (r TrunkRepos) Adopt(t testing.TB, dir string) TrunkRepos {
	t.Helper()
	r.Git(t, dir, "init", "--quiet", "-b", "main")
	r.identify(t, dir)
	r.Git(t, dir, "remote", "add", "origin", r.Remote)
	r.Git(t, dir, "fetch", "--quiet", "origin")
	r.Git(t, dir, "checkout", "--quiet", "-B", "main", "--track", "origin/main")
	r.Git(t, dir, "remote", "set-head", "origin", "main")
	exclude := filepath.Join(dir, ".git", "info", "exclude")
	if err := os.WriteFile(exclude, []byte("/.satelle/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r.Subject = dir
	return r
}

// Git runs git in dir and returns its trimmed stdout, failing the test on error.
func (TrunkRepos) Git(t testing.TB, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s (in %s): %v\n%s", strings.Join(args, " "), dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

// Commit writes content to file in dir and commits it on the checked-out branch.
func (r TrunkRepos) Commit(t testing.TB, dir, file, content string) {
	t.Helper()
	path := filepath.Join(dir, file)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	r.Git(t, dir, "add", file)
	r.identify(t, dir)
	r.Git(t, dir, "commit", "--quiet", "-m", "add "+file)
}

// LinkedWorktree adds a linked worktree of from on a new branch and returns its
// path, symlink-resolved like the paths satelle records. The branch is not
// trunk, so trunk stays checked out in from.
func (r TrunkRepos) LinkedWorktree(t testing.TB, from, branch string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), branch)
	r.Git(t, from, "worktree", "add", "--quiet", "-b", branch, path)
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	return path
}

// PublishFromPusher commits file on the pusher's trunk and pushes it, the way
// another machine lands work on the remote.
func (r TrunkRepos) PublishFromPusher(t testing.TB, file string) {
	t.Helper()
	r.Commit(t, r.Pusher, file, file+"\n")
	r.Git(t, r.Pusher, "push", "--quiet", "origin", "main")
}

// Head is dir's HEAD sha.
func (r TrunkRepos) Head(t testing.TB, dir string) string {
	t.Helper()
	return r.Git(t, dir, "rev-parse", "HEAD")
}

// RemoteHead is the sha the bare remote's trunk points at.
func (r TrunkRepos) RemoteHead(t testing.TB) string {
	t.Helper()
	return r.Git(t, r.Remote, "rev-parse", "refs/heads/main")
}

// GitErr runs git in dir and returns its combined output and error, for a test
// that expects git to refuse.
func (TrunkRepos) GitErr(dir string, args ...string) (string, error) {
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// LinearSlice commits one release commit on the subject's trunk, unpushed: a
// single story's release. It returns the commit's sha.
func (r TrunkRepos) LinearSlice(t testing.TB) []string {
	t.Helper()
	r.Commit(t, r.Subject, "story.txt", "story\n")
	return []string{r.Head(t, r.Subject)}
}

// EpicSlice builds an epic container's release on the subject's trunk, unpushed:
// two child branches, each with a commit, merged into trunk with --no-ff. It
// returns the two merge commits and the two child tips.
func (r TrunkRepos) EpicSlice(t testing.TB) (merges, tips []string) {
	t.Helper()
	for _, child := range []string{"child-a", "child-b"} {
		r.Git(t, r.Subject, "checkout", "--quiet", "-b", "epic/"+child)
		r.Commit(t, r.Subject, child+".txt", child+"\n")
		tips = append(tips, r.Head(t, r.Subject))
		r.Git(t, r.Subject, "checkout", "--quiet", "main")
		r.Git(t, r.Subject, "merge", "--quiet", "--no-ff", "-m", "merge epic/"+child, "epic/"+child)
		merges = append(merges, r.Head(t, r.Subject))
	}
	return merges, tips
}

// PushCounter installs a post-receive hook on the bare remote and returns a
// function that reports how many pushes it has accepted since.
func (r TrunkRepos) PushCounter(t testing.TB) func() int {
	t.Helper()
	log := filepath.Join(t.TempDir(), "pushes")
	hook := filepath.Join(r.Remote, "hooks", "post-receive")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\necho push >> '"+log+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return func() int {
		b, _ := os.ReadFile(log)
		return strings.Count(string(b), "push")
	}
}

// GitShim puts a git wrapper first on PATH for the test: it records each call's
// argv and then execs the real git. Everything the test spawns, including the
// code under test and shell commands it runs, goes through it.
func GitShim(t testing.TB) *ShimLog {
	t.Helper()
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	script := "#!/bin/sh\n{ printf '%s\\037' \"$@\"; printf '\\036'; } >> '" + log + "'\nexec '" + realGit + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return &ShimLog{path: log}
}

// ShimLog is what GitShim recorded.
type ShimLog struct{ path string }

// Calls is every recorded git argv, in order.
func (l *ShimLog) Calls() [][]string {
	b, _ := os.ReadFile(l.path)
	var out [][]string
	for _, rec := range strings.Split(string(b), "\x1e") {
		if rec == "" {
			continue
		}
		out = append(out, strings.Split(strings.TrimSuffix(rec, "\x1f"), "\x1f"))
	}
	return out
}

// Pushes is the recorded calls whose git subcommand is push.
func (l *ShimLog) Pushes() [][]string {
	var out [][]string
	for _, argv := range l.Calls() {
		if gitSubcommand(argv) == "push" {
			out = append(out, argv)
		}
	}
	return out
}

// AssertNoForce fails the test unless at least one push was recorded and none
// of them carries a force option or a forced (+) refspec.
func (l *ShimLog) AssertNoForce(t testing.TB) {
	t.Helper()
	pushes := l.Pushes()
	if len(pushes) == 0 {
		t.Fatal("no git push was recorded, so the no-force check proved nothing")
	}
	for _, argv := range pushes {
		for _, a := range argv {
			forced := strings.HasPrefix(a, "+") || strings.HasPrefix(a, "--force") ||
				(strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.Contains(a, "f"))
			if forced {
				t.Errorf("a push carries a force option %q: git %s", a, strings.Join(argv, " "))
			}
		}
	}
}

// gitSubcommand is argv's subcommand, skipping git's own leading options.
func gitSubcommand(argv []string) string {
	for i := 0; i < len(argv); i++ {
		switch {
		case argv[i] == "-C" || argv[i] == "-c":
			i++
		case strings.HasPrefix(argv[i], "-"):
		default:
			return argv[i]
		}
	}
	return ""
}

func (r TrunkRepos) identify(t testing.TB, dir string) {
	t.Helper()
	r.Git(t, dir, "config", "user.name", "Trunk Test")
	r.Git(t, dir, "config", "user.email", "trunk@example.invalid")
	r.Git(t, dir, "config", "commit.gpgsign", "false")
}
