package agentcli

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

func TestNewRunnerMapping(t *testing.T) {
	cases := []struct {
		name    string
		want    string
		wantErr bool
	}{
		{"", CLIClaude, false}, // empty defaults to claude
		{"claude", CLIClaude, false},
		{"CLAUDE", CLIClaude, false}, // case-insensitive
		{"codex", CLICodex, false},
		{"gpt", "", true},
	}
	for _, c := range cases {
		r, err := NewRunner(c.name)
		if c.wantErr {
			if err == nil {
				t.Errorf("NewRunner(%q): expected error", c.name)
			}
			continue
		}
		if err != nil {
			t.Errorf("NewRunner(%q): %v", c.name, err)
			continue
		}
		if r.Name() != c.want {
			t.Errorf("NewRunner(%q).Name() = %q, want %q", c.name, r.Name(), c.want)
		}
	}
}

func TestCodexStubErrorsClearly(t *testing.T) {
	r, err := NewRunner(CLICodex)
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.Run(context.Background(), Request{SystemPrompt: "x", Payload: "y"})
	if err == nil {
		t.Fatal("codex Run should error until mapped")
	}
	if !strings.Contains(err.Error(), "not yet mapped") {
		t.Errorf("codex error should be explicit, got: %v", err)
	}
}

// The claude preset must deny every work-tree MUTATOR so the grant is a CEILING
// (deny wins over allow) — INCLUDING Bash (sty_892517e7): a headless reviewer
// inherits the user's ~/.claude permission allows, so without Bash on the deny
// ceiling a "read-only" reviewer can write files via a shell redirect — observed
// live (the old push-review wrote summaries). Every reviewer rubric states "you
// cannot run commands"; the denylist now makes that true. An agent that MUST
// mutate is a named agent with an explicit full-command harness (no preset
// denylist), e.g. commit-agent.
func TestDefaultClaudeHarnessHasDenylistCeiling(t *testing.T) {
	for _, deny := range []string{"--disallowedTools", "Write", "Edit", "NotebookEdit", "Bash"} {
		if !strings.Contains(DefaultClaudeHarness, deny) {
			t.Errorf("DefaultClaudeHarness must include %q (mutator ceiling): %q", deny, DefaultClaudeHarness)
		}
	}
	rest := DefaultClaudeHarness[strings.Index(DefaultClaudeHarness, "--disallowedTools ")+len("--disallowedTools "):]
	if end := strings.Index(rest, " "); end >= 0 {
		rest = rest[:end]
	}
	if !strings.Contains(rest, "Bash") {
		t.Errorf("Bash must be on the deny ceiling (reviewers judge, never run commands): %q", rest)
	}
}

// A multi-line {system} value must survive as a SINGLE argv token, and {tools}
// must substitute in place.
func TestBuildArgsSubstitutesTokens(t *testing.T) {
	tmpl := strings.Fields("-p --append-system-prompt {system} --allowedTools {tools}")
	sys := "first line\nsecond line with spaces\tand a tab"
	args := buildArgs(tmpl, Request{SystemPrompt: sys, AllowedTools: "Read,Grep,Glob"})
	if !contains(args, sys) {
		t.Errorf("multi-line {system} was not preserved as one arg: %#v", args)
	}
	if !contains(args, "Read,Grep,Glob") {
		t.Errorf("{tools} not substituted: %#v", args)
	}
	if !contains(args, "--append-system-prompt") || !contains(args, "-p") {
		t.Errorf("literal flags missing: %#v", args)
	}
}

// An empty model drops both the {model} placeholder and its preceding flag.
func TestBuildArgsDropsEmptyModelFlag(t *testing.T) {
	tmpl := strings.Fields("--allowedTools {tools} --model {model}")
	args := buildArgs(tmpl, Request{AllowedTools: "Read", Model: ""})
	for _, a := range args {
		if a == "{model}" || a == "--model" || a == "" {
			t.Errorf("empty model should drop --model {model}, got %#v", args)
		}
	}
	if !contains(args, "Read") {
		t.Errorf("non-model args should remain: %#v", args)
	}
}

// A set model substitutes in place, keeping its flag.
func TestBuildArgsKeepsSetModel(t *testing.T) {
	tmpl := strings.Fields("--model {model}")
	args := buildArgs(tmpl, Request{Model: "claude-opus-4-8"})
	if len(args) != 2 || args[0] != "--model" || args[1] != "claude-opus-4-8" {
		t.Errorf("set model should yield [--model claude-opus-4-8], got %#v", args)
	}
}

// An unrecognised placeholder is passed through verbatim, not dropped or expanded.
func TestBuildArgsLeavesUnknownTokens(t *testing.T) {
	args := buildArgs(strings.Fields("--flag {unknown} value"), Request{})
	if !contains(args, "{unknown}") || !contains(args, "--flag") || !contains(args, "value") {
		t.Errorf("unknown tokens should pass through verbatim: %#v", args)
	}
}

// The payload is delivered on the subprocess's stdin (verified via `cat`, which
// echoes stdin to stdout). Exercises the full templateRunner.Run path.
func TestRunDeliversPayloadOnStdin(t *testing.T) {
	if _, err := exec.LookPath("cat"); err != nil {
		t.Skip("cat not on PATH")
	}
	r := templateRunner{binary: "cat"}
	out, err := r.Run(context.Background(), Request{Payload: "hello from stdin\n"})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if string(out) != "hello from stdin\n" {
		t.Errorf("stdin not delivered: got %q", string(out))
	}
}
