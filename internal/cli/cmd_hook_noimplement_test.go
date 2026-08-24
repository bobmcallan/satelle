package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
)

func TestEvaluateNoImplement_OffWhenUnset(t *testing.T) {
	d, _, _, reason := evaluateNoImplement(callerID{Model: "claude-fable-5"}, "/repo/internal/x.go", "/repo", config.Config{})
	if d != "skip" || !strings.Contains(reason, "model check skipped") {
		t.Fatalf("got %s %q", d, reason)
	}
}

func TestEvaluateNoImplement_DenyMessageByteForByte(t *testing.T) {
	msg := "sessions of this model may not implement"
	cfg := config.Config{Gate: config.GateConfig{
		NoImplementModels:  []string{"claude-fable*"},
		NoImplementMessage: msg,
	}}
	d, glob, _, reason := evaluateNoImplement(callerID{Model: "claude-fable-5"}, "/repo/internal/x.go", "/repo", cfg)
	if d != "deny" || glob != "claude-fable*" || reason != msg {
		t.Fatalf("got decision=%s glob=%q reason=%q", d, glob, reason)
	}
}

func TestEvaluateNoImplement_NonMatchFallsThrough(t *testing.T) {
	cfg := config.Config{Gate: config.GateConfig{NoImplementModels: []string{"claude-fable*"}}}
	d, _, _, _ := evaluateNoImplement(callerID{Model: "claude-opus-4-6"}, "/repo/internal/x.go", "/repo", cfg)
	if d != "allow" {
		t.Fatalf("got %s, want allow", d)
	}
}

func TestEvaluateNoImplement_ExemptPathsDoNotPunchEngagement(t *testing.T) {
	cfg := config.Config{Gate: config.GateConfig{
		NoImplementModels:      []string{"claude-fable*"},
		NoImplementMessage:     "nope",
		NoImplementExemptPaths: []string{"docs/"},
	}}
	d, _, src, reason := evaluateNoImplement(callerID{Model: "claude-fable-5"}, "/repo/docs/a.md", "/repo", cfg)
	if d != "skip" || src != "no_implement_exempt_paths" {
		t.Fatalf("got %s src=%q reason=%q", d, src, reason)
	}
}

func TestEvaluateNoImplement_ToolUseIDDecisionStableAcrossMtimes(t *testing.T) {
	main := "/sess.jsonl"
	sub := "/sess/subagents/agent-1.jsonl"
	payload := []byte(`{"tool_use_id":"toolu_edit1","transcript_path":"/sess.jsonl"}`)
	fs := memFS{
		files: map[string][]byte{
			main: assistantLine("claude-fable-5", ""),
			sub:  assistantLine("claude-opus-4-6", "toolu_edit1"),
		},
		mtime: map[string]int64{main: 200, sub: 100},
	}
	cfg := config.Config{Gate: config.GateConfig{
		NoImplementModels:  []string{"claude-fable*"},
		NoImplementMessage: "configured deny text",
	}}
	dec := func() string {
		c := resolveCaller(payload, fs)
		d, _, _, reason := evaluateNoImplement(c, "/repo/internal/x.go", "/repo", cfg)
		return d + "\n" + reason
	}
	a := dec()
	fs.mtime[main], fs.mtime[sub] = 10, 999
	b := dec()
	if a != b {
		t.Fatalf("decision changed with mtime:\n%s\nvs\n%s", a, b)
	}
	if !strings.HasPrefix(a, "allow\n") {
		t.Fatalf("opus sub-agent must allow, got %q", a)
	}
	if strings.Contains(a, "mtime heuristic") {
		t.Fatalf("tool_use_id path must not mention mtime heuristic: %q", a)
	}
}

func TestEvaluateNoImplement_HeuristicDenyMarksReason(t *testing.T) {
	cfg := config.Config{Gate: config.GateConfig{
		NoImplementModels:  []string{"claude-fable*"},
		NoImplementMessage: "configured deny text",
	}}
	d, _, _, reason := evaluateNoImplement(callerID{Key: "mtime heuristic", Model: "claude-fable-5"}, "/repo/internal/x.go", "/repo", cfg)
	if d != "deny" {
		t.Fatalf("got %s", d)
	}
	if !strings.Contains(reason, "mtime heuristic") {
		t.Fatalf("heuristic deny reason must name the heuristic: %q", reason)
	}
	if !strings.Contains(reason, "configured deny text") {
		t.Fatalf("heuristic deny must still carry the configured message: %q", reason)
	}
}

func TestEvaluateNoImplement_UnresolvedSkip(t *testing.T) {
	cfg := config.Config{Gate: config.GateConfig{NoImplementModels: []string{"claude-fable*"}}}
	d, _, _, reason := evaluateNoImplement(callerID{Reason: "no caller model resolvable from this payload"}, "/repo/internal/x.go", "/repo", cfg)
	if d != "skip" || !strings.Contains(reason, "model check skipped") {
		t.Fatalf("got %s %q", d, reason)
	}
}

func TestHookGate_NoImplementDenyAndGrokSkip(t *testing.T) {
	repo, _ := liveSeatRepo(t)
	cfgPath := filepath.Join(repo, ".satelle", "satelle.toml")
	body, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	msg := "configured deny text"
	body = append(body, []byte("\n[gate]\nno_implement_models = [\"claude-fable*\"]\nno_implement_message = \""+msg+"\"\n")...)
	if err := os.WriteFile(cfgPath, body, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SATELLE_CONFIG", cfgPath)

	out, err := runRootIn(t, `{"model":"claude-fable-5","tool_input":{"file_path":"internal/foo.go"}}`, "hook", "gate")
	if err == nil {
		t.Fatalf("fable edit must deny, stdout=%s", out)
	}
	if !strings.Contains(out, msg) {
		t.Fatalf("deny must use configured message, got %q", out)
	}

	// Grok-shaped payload: no resolvable model → no model deny. Seat is live so
	// engagement allows.
	out, err = runRootIn(t, `{"toolInput":{"filePath":"internal/foo.go"}}`, "hook", "gate")
	if err != nil {
		t.Fatalf("grok payload with live seat must not model-deny: %v\n%s", err, out)
	}
}

func TestHookGate_NoImplementSkipStillRequiresEngagement(t *testing.T) {
	repo := tempRepo(t)
	t.Chdir(repo)
	if err := os.MkdirAll(filepath.Join(repo, "internal"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(repo, ".satelle", "satelle.toml")
	body, _ := os.ReadFile(cfgPath)
	body = append(body, []byte("\n[gate]\nno_implement_models = [\"claude-fable*\"]\nno_implement_exempt_paths = [\"docs/\"]\n")...)
	if err := os.WriteFile(cfgPath, body, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SATELLE_CONFIG", cfgPath)
	if err := os.MkdirAll(filepath.Join(repo, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "docs", "a.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runRootIn(t, `{"model":"claude-fable-5","tool_input":{"file_path":"docs/a.md"}}`, "hook", "gate")
	if err == nil {
		t.Fatalf("exempt from model rule must still require engagement, stdout=%s", out)
	}
	if strings.Contains(out, "matches [gate] no_implement_models") {
		t.Fatalf("must not be a model deny: %s", out)
	}
}

func TestHookGate_CodexShapedSkip(t *testing.T) {
	repo, _ := liveSeatRepo(t)
	cfgPath := filepath.Join(repo, ".satelle", "satelle.toml")
	body, _ := os.ReadFile(cfgPath)
	body = append(body, []byte("\n[gate]\nno_implement_models = [\"claude-fable*\"]\nno_implement_message = \"nope\"\n")...)
	if err := os.WriteFile(cfgPath, body, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SATELLE_CONFIG", cfgPath)
	out, err := runRootIn(t, `{"tool_input":{"file_path":"internal/foo.go"}}`, "hook", "gate", "--harness", "codex")
	if err != nil {
		t.Fatalf("codex payload with no model must skip model rule: %v\n%s", err, out)
	}
}
