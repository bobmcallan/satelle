package worktree

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// Cloud collection (sty_82cffd60): a step performed in a provider's cloud session
// leaves its work on a pushed branch. These helpers prove the branch the session
// was based on is pushed, wait for the session's completion marker on the
// remote, and bring the branch into the story worktree. They know nothing of any
// provider or repo convention: the branch, the trailer and what the commit body
// holds are the caller's.

// PushedBranch returns the branch checked out at dir, after proving the cloud
// can see it: it must have an upstream, and that upstream must be HEAD. A cloud
// session is based on the pushed branch, so an unpushed or stale branch would
// start the session on something other than the tree the story worktree holds.
func PushedBranch(ctx context.Context, dir string) (string, error) {
	branch, err := git(ctx, dir, "symbolic-ref", "--short", "-q", "HEAD")
	if err != nil || branch == "" {
		return "", fmt.Errorf("worktree %s has no branch checked out (detached HEAD) — a cloud session is based on a pushed branch", dir)
	}
	upstream, err := git(ctx, dir, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}")
	if err != nil {
		return "", fmt.Errorf("branch %s has no upstream — push it first: git -C %s push -u origin %s", branch, dir, branch)
	}
	head, err := git(ctx, dir, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	pushed, err := git(ctx, dir, "rev-parse", "@{u}")
	if err != nil {
		return "", err
	}
	if head != pushed {
		return "", fmt.Errorf("branch %s is not pushed: HEAD %.8s differs from its upstream %s (%.8s) — push it first: git -C %s push", branch, head, upstream, pushed, dir)
	}
	return branch, nil
}

// UpstreamRemote is the remote the branch checked out at dir pushes to.
func UpstreamRemote(ctx context.Context, dir string) (string, error) {
	branch, err := git(ctx, dir, "symbolic-ref", "--short", "-q", "HEAD")
	if err != nil || branch == "" {
		return "", fmt.Errorf("worktree %s has no branch checked out", dir)
	}
	remote, err := git(ctx, dir, "config", "--get", "branch."+branch+".remote")
	if err != nil || remote == "" {
		return "", fmt.Errorf("branch %s has no configured remote", branch)
	}
	return remote, nil
}

// ErrTrailerTimeout is the wait deadline passing before the marker appeared.
var ErrTrailerTimeout = errors.New("worktree: the branch never carried the completion trailer before the deadline")

// Tip is a fetched branch tip.
type Tip struct {
	// Commit is the full sha of the tip.
	Commit string
	// Body is the tip commit message after its subject, with the trailer block
	// removed and the ends trimmed. Empty when the commit has only a subject.
	Body string
}

// WaitTrailerBranch polls remote until branch's tip commit message carries the
// trailer "<key>: <value>", then returns that tip. The trailer is the only
// marker: nothing else about the commit is inspected. A tip is fetched and read
// once per change, every interval. It returns ErrTrailerTimeout when timeout
// passes first, and the context's error if ctx ends.
func WaitTrailerBranch(ctx context.Context, dir, remote, branch, key, value string, interval, timeout time.Duration) (Tip, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ref := "refs/heads/" + branch
	var seen string
	for {
		out, err := git(ctx, dir, "ls-remote", "--heads", remote, ref)
		if err == nil {
			if sha, _, ok := strings.Cut(out, "\t"); ok && sha != seen {
				// A tip is marked seen only once it was read: a failed fetch of the
				// finished commit is retried on the next poll, not taken as unmarked.
				if tip, ok, ferr := readTip(ctx, dir, remote, ref, key, value); ferr == nil {
					seen = sha
					if ok {
						return tip, nil
					}
				}
			}
		}
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return Tip{}, ErrTrailerTimeout
			}
			return Tip{}, ctx.Err()
		case <-time.After(interval):
		}
	}
}

// readTip fetches ref and reports whether its tip message carries key: value.
func readTip(ctx context.Context, dir, remote, ref, key, value string) (Tip, bool, error) {
	if _, err := git(ctx, dir, "fetch", "--quiet", remote, ref); err != nil {
		return Tip{}, false, err
	}
	commit, err := git(ctx, dir, "rev-parse", "FETCH_HEAD")
	if err != nil {
		return Tip{}, false, err
	}
	msg, err := gitRaw(ctx, dir, "", "log", "-1", "--format=%B", commit)
	if err != nil {
		return Tip{}, false, err
	}
	trailers, err := gitRaw(ctx, dir, msg, "interpret-trailers", "--parse")
	if err != nil {
		return Tip{}, false, err
	}
	want := key + ": " + value
	for _, line := range strings.Split(trailers, "\n") {
		if strings.EqualFold(strings.TrimSpace(line), want) {
			return Tip{Commit: commit, Body: messageBody(msg)}, true, nil
		}
	}
	return Tip{}, false, nil
}

var trailerLine = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*:\s`)

// messageBody is msg after its subject paragraph, minus a trailing trailer
// block, trimmed.
func messageBody(msg string) string {
	paras := strings.Split(strings.TrimSpace(strings.ReplaceAll(msg, "\r\n", "\n")), "\n\n")
	if len(paras) > 1 && isTrailerBlock(paras[len(paras)-1]) {
		paras = paras[:len(paras)-1]
	}
	if len(paras) < 2 {
		return ""
	}
	return strings.TrimSpace(strings.Join(paras[1:], "\n\n"))
}

// isTrailerBlock reports whether every line of para is a "Key: value" trailer or
// a folded continuation of one.
func isTrailerBlock(para string) bool {
	for i, line := range strings.Split(para, "\n") {
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			if i == 0 {
				return false
			}
			continue
		}
		if !trailerLine.MatchString(line) {
			return false
		}
	}
	return true
}

// CollectBranch brings commit into the worktree at dir: a fast-forward when
// HEAD is its ancestor, otherwise a merge. A merge that is not clean is aborted,
// so the worktree is as it was, and the returned error says so.
func CollectBranch(ctx context.Context, dir, commit string) error {
	if _, err := git(ctx, dir, "merge", "--ff-only", "--quiet", commit); err == nil {
		return nil
	}
	if _, err := git(ctx, dir, "merge", "--no-edit", "--quiet", commit); err != nil {
		_, _ = git(ctx, dir, "merge", "--abort")
		return fmt.Errorf("collected commit %.8s does not merge cleanly into the story worktree — resolve by hand: %w", commit, err)
	}
	return nil
}

// gitRaw runs git with stdin and returns stdout unmodified.
func gitRaw(ctx context.Context, dir, stdin string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = cleanEnv()
	var stdout, stderr bytes.Buffer
	cmd.Stdin = strings.NewReader(stdin)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String(), fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}
