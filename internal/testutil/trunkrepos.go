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

func (r TrunkRepos) identify(t testing.TB, dir string) {
	t.Helper()
	r.Git(t, dir, "config", "user.name", "Trunk Test")
	r.Git(t, dir, "config", "user.email", "trunk@example.invalid")
	r.Git(t, dir, "config", "commit.gpgsign", "false")
}
