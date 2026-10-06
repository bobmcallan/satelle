package worktree

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMessageBody(t *testing.T) {
	for name, c := range map[string]struct{ msg, want string }{
		"subject only":              {"subject\n", ""},
		"subject and trailer":       {"subject\n\nSatelle-Nonce: abc\n", ""},
		"body and trailer":          {"subject\n\n## Table\n\n| a | b |\n\nSatelle-Nonce: abc\n", "## Table\n\n| a | b |"},
		"body, two trailers":        {"s\n\nbody line\n\nCo-Authored-By: x <x@x>\nSatelle-Nonce: abc\n", "body line"},
		"body without trailer":      {"s\n\nsome prose: with a colon in the middle\n", "some prose: with a colon in the middle"},
		"last paragraph is prose":   {"s\n\nbody\n\nNote that this is prose, not a trailer\n", "body\n\nNote that this is prose, not a trailer"},
		"crlf":                      {"s\r\n\r\nbody\r\n\r\nSatelle-Nonce: abc\r\n", "body"},
		"trailer-looking body only": {"s\n\nKey: value\n", ""},
	} {
		if got := messageBody(c.msg); got != c.want {
			t.Errorf("%s: messageBody(%q) = %q, want %q", name, c.msg, got, c.want)
		}
	}
}

func gitFix(t *testing.T) (work, remote string) {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	for k, v := range map[string]string{"GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@t", "GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@t"} {
		t.Setenv(k, v)
	}
	root := t.TempDir()
	work, remote = filepath.Join(root, "work"), filepath.Join(root, "remote.git")
	run := func(dir string, args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	run(root, "init", "-q", "--bare", "-b", "main", remote)
	run(root, "clone", "-q", remote, work)
	if err := os.WriteFile(filepath.Join(work, "f"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(work, "add", ".")
	run(work, "commit", "-q", "-m", "init")
	run(work, "branch", "-M", "main")
	run(work, "push", "-q", "-u", "origin", "main")
	return work, remote
}

func TestPushedBranchAndUpstreamRemote(t *testing.T) {
	work, _ := gitFix(t)
	ctx := context.Background()
	if b, err := PushedBranch(ctx, work); err != nil || b != "main" {
		t.Fatalf("pushed branch = %q, %v", b, err)
	}
	if r, err := UpstreamRemote(ctx, work); err != nil || r != "origin" {
		t.Fatalf("remote = %q, %v", r, err)
	}
	if err := os.WriteFile(filepath.Join(work, "f"), []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", work, "commit", "-q", "-am", "local").CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if _, err := PushedBranch(ctx, work); err == nil || !strings.Contains(err.Error(), "git") || !strings.Contains(err.Error(), "push") {
		t.Fatalf("unpushed HEAD: err = %v, want a refusal naming git push", err)
	}
}

func TestWaitTrailerBranchTimesOut(t *testing.T) {
	work, _ := gitFix(t)
	start := time.Now()
	_, err := WaitTrailerBranch(context.Background(), work, "origin", "claude/never", "Satelle-Nonce", "abc", 10*time.Millisecond, 150*time.Millisecond)
	if !errors.Is(err, ErrTrailerTimeout) {
		t.Fatalf("err = %v, want ErrTrailerTimeout", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("wait overran its timeout")
	}
}

func TestWaitTrailerBranchHonoursContext(t *testing.T) {
	work, _ := gitFix(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := WaitTrailerBranch(ctx, work, "origin", "claude/never", "Satelle-Nonce", "abc", 10*time.Millisecond, time.Minute)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

// A failed fetch of the finished commit is retried on the next poll: the tip is
// only marked seen once it was read, so a transient failure is not a timeout.
func TestWaitTrailerBranchRetriesFailedFetch(t *testing.T) {
	work, _ := gitFix(t)
	run := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", work}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	run("checkout", "-q", "-b", "claude/done")
	run("commit", "-q", "--allow-empty", "-m", "work\n\nbody\n\nSatelle-Nonce: abc")
	run("push", "-q", "origin", "claude/done")
	// A directory where git writes FETCH_HEAD makes every fetch fail until it is removed.
	blocker := filepath.Join(work, ".git", "FETCH_HEAD")
	if err := os.Mkdir(blocker, 0o755); err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(300 * time.Millisecond)
		_ = os.Remove(blocker)
	}()
	tip, err := WaitTrailerBranch(context.Background(), work, "origin", "claude/done", "Satelle-Nonce", "abc", 50*time.Millisecond, 10*time.Second)
	if err != nil {
		t.Fatalf("err = %v, want the tip once the fetch recovers", err)
	}
	if tip.Body != "body" {
		t.Fatalf("body = %q", tip.Body)
	}
}
