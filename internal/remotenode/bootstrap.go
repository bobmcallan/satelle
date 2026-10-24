package remotenode

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"time"
)

// BootstrapError is a [host] bootstrap command that failed. Index is its place in
// the list, counted from 1, and Reason a fixed classification. Neither holds the
// command's argv, its output or the environment it ran in, so the error is safe to
// report to satelle.dev.
type BootstrapError struct {
	Index  int
	Reason string
}

func (e *BootstrapError) Error() string {
	return fmt.Sprintf("bootstrap command %d: %s", e.Index, e.Reason)
}

// RunBootstrap runs each argv in order in dir, with no stdin, each bounded by
// timeout, and stops at the first that fails. The commands' output goes to out
// (the node's own log) and nowhere else. The environment is the node's own.
func RunBootstrap(ctx context.Context, dir string, argvs [][]string, timeout time.Duration, out io.Writer) error {
	for i, argv := range argvs {
		if reason := runBootstrapCommand(ctx, dir, argv, timeout, out); reason != "" {
			return &BootstrapError{Index: i + 1, Reason: reason}
		}
	}
	return nil
}

// runBootstrapCommand runs one argv and returns why it failed, "" when it did not.
// The reason is a fixed classification, never the argv or the raw exec error.
func runBootstrapCommand(ctx context.Context, dir string, argv []string, timeout time.Duration, out io.Writer) string {
	if len(argv) == 0 || argv[0] == "" {
		return "empty command"
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Stdin = nil
	if out != nil {
		cmd.Stdout, cmd.Stderr = out, out
	}
	// A command that ignores the kill and holds the pipes open must not outlive the bound.
	cmd.WaitDelay = time.Second
	err := cmd.Run()
	if err == nil {
		return ""
	}
	if cctx.Err() != nil {
		if ctx.Err() != nil {
			return "interrupted"
		}
		return fmt.Sprintf("timed out after %s", timeout)
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return fmt.Sprintf("exit status %d", exit.ExitCode())
	}
	return "could not start"
}
