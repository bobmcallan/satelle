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
	if !strings.Contains(DefaultCodexExecCommand, "{effort}") {
		t.Error("DefaultCodexExecCommand must include {effort} (sty_aa726901)")
	}
	if !strings.Contains(DefaultCodexExecCommand, "model_reasoning_effort=") {
		t.Error("DefaultCodexExecCommand must use model_reasoning_effort (sty_aa726901)")
	}
}

// TestBuildArgsFusedEffort (sty_aa726901): fused {effort}/{model}/{settings}
// expand in place; empty drops the token and preceding flag; fused
// {system}/{payload} are not substituted.
func TestBuildArgsFusedEffort(t *testing.T) {
	tmpl := strings.Fields(`-c model_reasoning_effort="{effort}"`)
	args := buildArgs(tmpl, Request{Effort: "high"})
	if len(args) != 2 || args[0] != "-c" || args[1] != `model_reasoning_effort="high"` {
		t.Fatalf("fused effort set: got %v", args)
	}
	argsEmpty := buildArgs(tmpl, Request{})
	if len(argsEmpty) != 0 {
		t.Fatalf("empty effort should drop -c and fused token: %v", argsEmpty)
	}

	// Fused model.
	args = buildArgs(strings.Fields("--foo model={model}"), Request{Model: "m1"})
	if !contains(args, "model=m1") || !contains(args, "--foo") {
		t.Fatalf("fused model: %v", args)
	}
	argsEmpty = buildArgs(strings.Fields("--foo model={model}"), Request{})
	if contains(argsEmpty, "--foo") || contains(argsEmpty, "model=") {
		t.Fatalf("empty fused model should drop flag: %v", argsEmpty)
	}

	// Fused {system} / {payload} must NOT be substituted (exact-token only).
	raw := buildArgs([]string{"prefix={system}"}, Request{SystemPrompt: "rubric"})
	if len(raw) != 1 || raw[0] != "prefix={system}" {
		t.Fatalf("fused {system} must stay literal: %v", raw)
	}
	raw = buildArgs([]string{"body={payload}"}, Request{Payload: `{}`})
	if len(raw) != 1 || raw[0] != "body={payload}" {
		t.Fatalf("fused {payload} must stay literal: %v", raw)
	}
}
