package agentcli

import (
	"strings"
	"testing"
)

func TestBuildArgsEffortPlaceholder(t *testing.T) {
	tmpl := strings.Fields("--model {model} --reasoning-effort {effort}")
	args := buildArgs(tmpl, Request{Model: "m1", Effort: "high"})
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "high") || !strings.Contains(joined, "m1") {
		t.Fatalf("got %v", args)
	}
	argsEmpty := buildArgs(tmpl, Request{})
	for _, a := range argsEmpty {
		if a == "--model" || a == "--reasoning-effort" || a == "{effort}" || a == "{model}" {
			t.Fatalf("empty effort/model should drop flags: %v", argsEmpty)
		}
	}
}

func TestDefaultTemplatesIncludeEffort(t *testing.T) {
	if !strings.Contains(DefaultClaudeCommand, "{effort}") {
		t.Error("DefaultClaudeCommand must include {effort}")
	}
	if !strings.Contains(DefaultGrokCommand, "{effort}") {
		t.Error("DefaultGrokCommand must include {effort}")
	}
}
