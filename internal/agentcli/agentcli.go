// Package agentcli abstracts the headless agent CLI the quality-management spine
// shells out to for isolated reviews and summaries. satellites hardcoded claude's
// flag surface (claude -p --append-system-prompt …); satelle routes every
// subprocess through a Runner so the operator picks their agent (claude or codex)
// at install time and no reviewer/summariser code names a binary directly.
//
// A Runner takes a Request (system prompt + stdin payload + tool grant + optional
// model, in a working dir) and returns the agent's stdout. The reviewer (A3) and
// summariser (A5) build on this; this package ships the abstraction, the claude
// implementation, a clearly-erroring codex stub, and PATH detection.
package agentcli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Supported agent CLI identifiers.
const (
	CLIClaude = "claude"
	CLICodex  = "codex"
)

// Request is one headless agent invocation.
type Request struct {
	SystemPrompt string // appended as the system prompt (the gate/skill body)
	Payload      string // delivered on stdin (the review/summary input)
	AllowedTools string // comma-separated tool grant
	Model        string // optional model override; "" inherits the harness default
	Dir          string // working directory for the subprocess
}

// Runner invokes an agent CLI headlessly and returns its stdout.
type Runner interface {
	// Name reports the agent CLI identifier (claude | codex).
	Name() string
	// Run executes the agent over req and returns its raw stdout.
	Run(ctx context.Context, req Request) ([]byte, error)
}

// NewRunner returns the Runner for the named CLI. An empty name defaults to
// claude; an unknown name is an error.
func NewRunner(name string) (Runner, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", CLIClaude:
		return claudeRunner{binary: CLIClaude}, nil
	case CLICodex:
		return codexRunner{binary: CLICodex}, nil
	default:
		return nil, fmt.Errorf("agentcli: unknown agent cli %q (want %q or %q)", name, CLIClaude, CLICodex)
	}
}

// RunnerFromHarness resolves an actors-layer harness binding to a Runner by its
// leading CLI name — e.g. "claude -p", "codex -p", or a bare "claude". An empty or
// "in-loop" harness returns (nil, nil): no agent-CLI runner, so the caller keeps its
// configured default. An unknown CLI name is an error (from NewRunner).
func RunnerFromHarness(harness string) (Runner, error) {
	fields := strings.Fields(harness)
	if len(fields) == 0 {
		return nil, nil
	}
	if cli := strings.ToLower(fields[0]); cli != "in-loop" {
		return NewRunner(cli)
	}
	return nil, nil
}

// Detect returns the first supported agent CLI found on PATH (claude preferred),
// or "" when none is installed. Used by the install-time selection.
func Detect() string {
	for _, c := range []string{CLIClaude, CLICodex} {
		if _, err := exec.LookPath(c); err == nil {
			return c
		}
	}
	return ""
}

// Available reports whether the named CLI's binary is on PATH.
func Available(name string) bool {
	r, err := NewRunner(name)
	if err != nil {
		return false
	}
	_, lerr := exec.LookPath(r.Name())
	return lerr == nil
}

// claudeRunner invokes `claude -p --allowedTools <tools> --append-system-prompt
// <body> [--model m]` with the payload on stdin — the satellites gate argv,
// reproduced behind the Runner seam.
type claudeRunner struct{ binary string }

func (c claudeRunner) Name() string { return CLIClaude }

func (c claudeRunner) Run(ctx context.Context, req Request) ([]byte, error) {
	args := []string{"-p", "--allowedTools", req.AllowedTools, "--append-system-prompt", req.SystemPrompt}
	if m := strings.TrimSpace(req.Model); m != "" {
		args = append(args, "--model", m)
	}
	return runProcess(ctx, c.binary, args, req)
}

// codexRunner is a placeholder: codex's headless surface differs from claude's
// (no --append-system-prompt), so a faithful argv mapping is follow-up work. It
// is selectable so the seam is exercised, but Run errors clearly until mapped.
type codexRunner struct{ binary string }

func (c codexRunner) Name() string { return CLICodex }

func (c codexRunner) Run(ctx context.Context, req Request) ([]byte, error) {
	return nil, fmt.Errorf("agentcli: the codex runner is not yet implemented — install claude and set [agent] cli = %q, or contribute the codex argv mapping", CLIClaude)
}

// runProcess runs binary with args, feeding req.Payload on stdin in req.Dir, and
// returns stdout. A non-zero exit surfaces stderr in the error.
func runProcess(ctx context.Context, binary string, args []string, req Request) ([]byte, error) {
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Stdin = strings.NewReader(req.Payload)
	if req.Dir != "" {
		cmd.Dir = req.Dir
	}
	cmd.Env = os.Environ()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, fmt.Errorf("agentcli: %s: %w: %s", binary, err, msg)
		}
		return nil, fmt.Errorf("agentcli: %s: %w", binary, err)
	}
	return out, nil
}
