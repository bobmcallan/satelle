package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type memFS struct {
	files map[string][]byte
	mtime map[string]int64
}

func (m memFS) ReadFile(name string) ([]byte, error) {
	b, ok := m.files[name]
	if !ok {
		return nil, os.ErrNotExist
	}
	return b, nil
}

func (m memFS) Glob(pattern string) ([]string, error) {
	var out []string
	for p := range m.files {
		ok, err := filepath.Match(pattern, p)
		if err == nil && ok {
			out = append(out, p)
		}
	}
	return out, nil
}

func (m memFS) ModTime(name string) (int64, error) {
	if t, ok := m.mtime[name]; ok {
		return t, nil
	}
	if _, ok := m.files[name]; ok {
		return 0, nil
	}
	return 0, os.ErrNotExist
}

func assistantLine(model, toolID string) []byte {
	if toolID == "" {
		return []byte(`{"message":{"model":"` + model + `","role":"assistant","content":[{"type":"text","text":"ok"}]}}`)
	}
	return []byte(`{"message":{"model":"` + model + `","role":"assistant","content":[{"type":"tool_use","id":"` + toolID + `","name":"Edit","input":{"file_path":"internal/foo.go"}}]}}`)
}

func TestResolveCaller_ToolUseIDIndependentOfMtime(t *testing.T) {
	main := "/sess.jsonl"
	sub := "/sess/subagents/agent-1.jsonl"
	payload := []byte(`{"tool_use_id":"toolu_edit1","transcript_path":"/sess.jsonl","tool_input":{"file_path":"internal/foo.go"}}`)
	fs := memFS{
		files: map[string][]byte{
			main: assistantLine("claude-fable-5", ""),
			sub:  assistantLine("claude-opus-4-6", "toolu_edit1"),
		},
		mtime: map[string]int64{main: 200, sub: 100},
	}
	a := resolveCaller(payload, fs)
	fs.mtime[main], fs.mtime[sub] = 100, 200
	b := resolveCaller(payload, fs)
	if a.Model != "claude-opus-4-6" || b.Model != "claude-opus-4-6" {
		t.Fatalf("want opus sub-agent in both mtime orders, got %q / %q", a.Model, b.Model)
	}
	if a.Key != "tool_use_id" || b.Key != "tool_use_id" {
		t.Fatalf("key = %q / %q, want tool_use_id", a.Key, b.Key)
	}
	if a != b {
		t.Fatalf("non-heuristic callerID must be byte-identical across mtimes:\n%+v\n%+v", a, b)
	}
}

func TestResolveCaller_ReverseRolesDenyFableSubagent(t *testing.T) {
	main := "/sess.jsonl"
	sub := "/sess/subagents/agent-1.jsonl"
	payload := []byte(`{"tool_use_id":"toolu_edit1","transcript_path":"/sess.jsonl"}`)
	fs := memFS{
		files: map[string][]byte{
			main: assistantLine("claude-opus-4-6", ""),
			sub:  assistantLine("claude-fable-5", "toolu_edit1"),
		},
		mtime: map[string]int64{main: 200, sub: 100},
	}
	a := resolveCaller(payload, fs)
	fs.mtime[main], fs.mtime[sub] = 50, 900
	b := resolveCaller(payload, fs)
	if a.Model != "claude-fable-5" || b.Model != "claude-fable-5" {
		t.Fatalf("want fable sub-agent in both mtime orders, got %q / %q", a.Model, b.Model)
	}
	if a != b {
		t.Fatalf("mismatch across mtimes: %+v vs %+v", a, b)
	}
}

func TestResolveCaller_MtimeHeuristicSelfLabels(t *testing.T) {
	main := "/sess.jsonl"
	sub := "/sess/subagents/agent-1.jsonl"
	payload := []byte(`{"transcript_path":"/sess.jsonl"}`)
	fs := memFS{
		files: map[string][]byte{
			main: assistantLine("claude-fable-5", ""),
			sub:  assistantLine("claude-opus-4-6", ""),
		},
		mtime: map[string]int64{main: 100, sub: 200},
	}
	got := resolveCaller(payload, fs)
	if got.Key != "mtime heuristic" || got.Model != "claude-opus-4-6" {
		t.Fatalf("got %+v, want mtime heuristic + opus", got)
	}
	if !strings.Contains(got.Reason, "mtime heuristic") {
		t.Fatalf("reason %q must contain mtime heuristic", got.Reason)
	}
	fs.mtime[main], fs.mtime[sub] = 300, 100
	got2 := resolveCaller(payload, fs)
	if got2.Model != "claude-fable-5" {
		t.Fatalf("older sub-agent must lose: %+v", got2)
	}
	if got2.Key != "mtime heuristic" {
		t.Fatalf("key %q", got2.Key)
	}
}

func TestResolveCaller_PayloadModelWins(t *testing.T) {
	got := resolveCaller([]byte(`{"model":"claude-fable-5","transcript_path":"/nope.jsonl"}`), memFS{files: map[string][]byte{}})
	if got.Key != "payload_model" || got.Model != "claude-fable-5" {
		t.Fatalf("got %+v", got)
	}
}

func TestResolveCaller_Unresolved(t *testing.T) {
	got := resolveCaller([]byte(`{"toolInput":{"filePath":"x.go"}}`), memFS{files: map[string][]byte{}})
	if got.Model != "" || got.Key != "" {
		t.Fatalf("want unresolved, got %+v", got)
	}
	if got.Reason == "" {
		t.Fatal("unresolved must name why")
	}
}
