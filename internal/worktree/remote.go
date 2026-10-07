package worktree

import (
	"fmt"
	"os/exec"
	"strings"
)

// Read-only probes of a working tree's relation to the git remote. They run git
// and decide nothing: what an operator does with "dirty" or "not on a remote" is
// the caller's configuration. None of them touches the network — the remote is
// whatever the remote-tracking refs say as of the last fetch or push.

// Dirty reports whether dir's working tree has uncommitted changes, untracked
// files included.
func Dirty(dir string) (bool, error) {
	out, err := gitOutput(dir, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) != "", nil
}

// Head returns the commit dir's working tree is on.
func Head(dir string) (string, error) {
	out, err := gitOutput(dir, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// OnRemote reports whether any remote-tracking ref contains sha, that is,
// whether the commit can be checked out from a remote.
func OnRemote(dir, sha string) (bool, error) {
	out, err := gitOutput(dir, "for-each-ref", "--count=1", "--contains", sha, "--format=%(refname)", "refs/remotes")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) != "", nil
}

// HasCommit reports whether sha names a commit present in dir's repository, so a
// recorded head can be told apart from one that never reached this machine.
func HasCommit(dir, sha string) (bool, error) {
	if _, err := gitOutput(dir, "rev-parse", "--git-dir"); err != nil {
		return false, err
	}
	cmd := exec.Command("git", "-C", dir, "cat-file", "-e", sha+"^{commit}")
	if err := cmd.Run(); err != nil {
		if _, ok := err.(*exec.ExitError); ok {
			return false, nil
		}
		return false, fmt.Errorf("git cat-file: %w", err)
	}
	return true, nil
}

func gitOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		msg := err.Error()
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			msg = strings.TrimSpace(string(ee.Stderr))
		}
		return "", fmt.Errorf("git %s: %s", args[0], msg)
	}
	return string(out), nil
}
