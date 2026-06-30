// Package agentcli abstracts the headless agent CLI the quality-management spine
// shells out to for isolated reviews and summaries. satellites hardcoded claude's
// flag surface (claude -p --append-system-prompt …); satelle routes every
// subprocess through a Runner driven by a CONFIG TEMPLATE, so the operator picks
// their agent and its exact argv in `.satelle/agents.toml` and no reviewer code
// names a binary or a flag directly.
//
// A harness string is a command template: the first token is the binary, the rest
// are argv tokens that may carry the placeholders {system}, {tools}, and {model}.
// At call time satelle substitutes each placeholder into its own argv token (so a
// multi-line system prompt stays a single argument) and pipes the payload on stdin.
// A bare CLI name (a single token, e.g. "claude") expands to that CLI's built-in
// PRESET template — claude's preset carries a read-only --disallowedTools denylist
// so the grant is a ceiling over the repo's settings, not just an allowlist floor.
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

// DefaultClaudeHarness is the claude preset template — the satellites gate argv
// reproduced behind the template seam, hardened with a --disallowedTools denylist
// so the reviewer's grant is a CEILING (deny wins over allow) over any
// permissions the repo's .claude/settings.json would otherwise inherit. {system}
// is the gate/skill body, {tools} the allow-grant, {model} the optional model
// (dropped, with its flag, when unset).
//
// The denylist keeps the work-tree MUTATORS off (Write, Edit, NotebookEdit) so a
// reviewer can never modify the repo it judges — the read-only invariant. The
// reviewer's default allow-grant ({tools}) is read-only (Read, Grep, Glob) and
// needs no shell: the substrate it reasons about is materialised as markdown
// under .satelle, so it reads it directly. A repo MAY widen the grant in
// .satelle/agents.toml (transparently), but the default is read-only.
const DefaultClaudeHarness = "claude -p --disallowedTools Write,Edit,NotebookEdit --append-system-prompt {system} --allowedTools {tools} --model {model}"

// Request is one headless agent invocation.
type Request struct {
	SystemPrompt string // {system}: appended as the system prompt (the gate/skill body)
	Payload      string // delivered on stdin (the review/summary input)
	AllowedTools string // {tools}: comma-separated tool grant
	Model        string // {model}: optional model override; "" drops the placeholder
	Dir          string // working directory for the subprocess
}

// Runner invokes an agent CLI headlessly and returns its stdout.
type Runner interface {
	// Name reports the agent CLI identifier (the template's binary).
	Name() string
	// Command reports the resolved command/harness template (binary + argv with the
	// {system}/{tools}/{model} placeholders intact) — a concise, payload-free
	// description of HOW the agent is invoked, recorded as invocation evidence
	// (sty_fb3e0873). It never includes the rubric body or the stdin payload.
	Command() string
	// Run executes the agent over req and returns its raw stdout.
	Run(ctx context.Context, req Request) ([]byte, error)
}

// NewRunner returns the Runner for a bare CLI NAME — the preset. An empty name
// defaults to claude; "codex" is the not-yet-mapped stub; an unknown name errors.
// Callers with a full harness template use RunnerFromHarness instead.
func NewRunner(name string) (Runner, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", CLIClaude:
		return templateFromHarness(DefaultClaudeHarness), nil
	case CLICodex:
		return codexRunner{binary: CLICodex}, nil
	default:
		return nil, fmt.Errorf("agentcli: unknown agent cli %q (want %q or %q, or a full harness template)", name, CLIClaude, CLICodex)
	}
}

// RunnerFromHarness resolves an agents-layer harness binding to a Runner. An empty
// or "in-loop" harness returns (nil, nil): no agent-CLI runner, so the caller keeps
// its configured default. A SINGLE-token harness is a preset CLI name, resolved via
// NewRunner (so "codex" still errors as a stub). A MULTI-token harness is a literal
// command template: the first token is the binary, the rest the argv template.
func RunnerFromHarness(harness string) (Runner, error) {
	fields := strings.Fields(harness)
	if len(fields) == 0 || strings.ToLower(fields[0]) == "in-loop" {
		return nil, nil
	}
	if len(fields) == 1 {
		return NewRunner(fields[0])
	}
	return templateRunner{binary: fields[0], argTemplate: fields[1:]}, nil
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

// templateRunner executes a command template: a binary plus an argv template whose
// tokens may carry {system}/{tools}/{model} placeholders. It is the single code
// path for every agent CLI — claude, codex, or any operator-supplied binary.
type templateRunner struct {
	binary      string
	argTemplate []string
}

// templateFromHarness parses a full harness string into a templateRunner.
func templateFromHarness(harness string) templateRunner {
	fields := strings.Fields(harness)
	return templateRunner{binary: fields[0], argTemplate: fields[1:]}
}

func (t templateRunner) Name() string { return t.binary }

// Command joins the binary and argv template (placeholders intact) into the
// payload-free harness string recorded as invocation evidence.
func (t templateRunner) Command() string {
	return strings.Join(append([]string{t.binary}, t.argTemplate...), " ")
}

func (t templateRunner) Run(ctx context.Context, req Request) ([]byte, error) {
	return runProcess(ctx, t.binary, buildArgs(t.argTemplate, req), req)
}

// buildArgs substitutes the placeholders in an argv template against req. Each of
// {system}/{tools}/{model} must be its own token, so a multi-word value (a
// multi-line system prompt) becomes exactly one argument. An empty {model} drops
// the placeholder AND a directly preceding flag token (e.g. "--model {model}"), so
// the default template carries the model flag without emitting an empty value.
func buildArgs(argTemplate []string, req Request) []string {
	args := make([]string, 0, len(argTemplate))
	for _, tok := range argTemplate {
		switch tok {
		case "{system}":
			args = append(args, req.SystemPrompt)
		case "{tools}":
			args = append(args, req.AllowedTools)
		case "{model}":
			if strings.TrimSpace(req.Model) == "" {
				if n := len(args); n > 0 && strings.HasPrefix(args[n-1], "-") {
					args = args[:n-1] // drop the now-valueless flag
				}
				continue
			}
			args = append(args, req.Model)
		default:
			args = append(args, tok)
		}
	}
	return args
}

// codexRunner is a placeholder: codex's headless surface differs from claude's
// (no --append-system-prompt), so a faithful preset is follow-up work. It is
// selectable so the seam is exercised, but Run errors clearly until mapped — a
// repo can still use codex today by setting a full [reviewer] harness template.
type codexRunner struct{ binary string }

func (c codexRunner) Name() string { return CLICodex }

// Command reports the codex binary; its full preset argv is not yet mapped.
func (c codexRunner) Command() string { return c.binary }

func (c codexRunner) Run(ctx context.Context, req Request) ([]byte, error) {
	return nil, fmt.Errorf("agentcli: the codex preset is not yet mapped — install claude, or set [reviewer] harness to a full codex command template in .satelle/agents.toml")
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
