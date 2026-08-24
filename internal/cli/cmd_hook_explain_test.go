package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHookExplain_AllowDenyFallbackExempt(t *testing.T) {
	pkgDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	repo := tempRepo(t)
	t.Chdir(repo)
	cfgPath := filepath.Join(repo, ".satelle", "satelle.toml")
	body, _ := os.ReadFile(cfgPath)
	body = append(body, []byte(`
[gate]
no_implement_models = ["claude-fable*"]
no_implement_message = "configured deny text"
no_implement_exempt_paths = ["docs/"]
`)...)
	if err := os.WriteFile(cfgPath, body, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SATELLE_CONFIG", cfgPath)

	dir := t.TempDir()
	write := func(name, content string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}

	cases := []struct {
		name    string
		payload string
		want    []string
	}{
		{
			name:    "allow",
			payload: `{"model":"claude-opus-4-6","tool_input":{"file_path":"internal/foo.go"}}`,
			want:    []string{"key:        payload_model", "model:      claude-opus-4-6", "decision:   allow", "exempt:     (none)"},
		},
		{
			name:    "deny",
			payload: `{"model":"claude-fable-5","tool_input":{"file_path":"internal/foo.go"}}`,
			want:    []string{"decision:   deny", "matched:    claude-fable*", "configured deny text"},
		},
		{
			name:    "fallback",
			payload: `{"transcript_path":"/no-such.jsonl"}`,
			want:    []string{"key:        (unresolved)", "decision:   skip", "model check skipped"},
		},
		{
			name:    "exempt",
			payload: `{"model":"claude-fable-5","tool_input":{"file_path":"docs/a.md"}}`,
			want:    []string{"decision:   skip", "exempt:     no_implement_exempt_paths"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := write(tc.name+".json", tc.payload)
			out, err := runRootIn(t, "", "hook", "explain", "--payload", p)
			if err != nil {
				t.Fatalf("explain: %v\n%s", err, out)
			}
			for _, w := range tc.want {
				if !strings.Contains(out, w) {
					t.Errorf("explain output missing %q:\n%s", w, out)
				}
			}
			golden := filepath.Join(pkgDir, "testdata", "hookexplain", tc.name+".golden")
			wantb, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("golden %s: %v", golden, err)
			}
			if out != string(wantb) {
				t.Errorf("golden mismatch for %s\ngot:\n%s\nwant:\n%s", tc.name, out, wantb)
			}
		})
	}
}

func TestHookExplain_MtimeHeuristicFallback(t *testing.T) {
	pkgDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	repo := tempRepo(t)
	t.Chdir(repo)
	cfgPath := filepath.Join(repo, ".satelle", "satelle.toml")
	body, _ := os.ReadFile(cfgPath)
	body = append(body, []byte(`
[gate]
no_implement_models = ["claude-fable*"]
no_implement_message = "configured deny text"
`)...)
	if err := os.WriteFile(cfgPath, body, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SATELLE_CONFIG", cfgPath)

	sess := t.TempDir()
	main := filepath.Join(sess, "session.jsonl")
	subDir := filepath.Join(sess, "session", "subagents")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(subDir, "agent-1.jsonl")
	if err := os.WriteFile(main, assistantLine("claude-fable-5", ""), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sub, assistantLine("claude-opus-4-6", ""), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(main, time.Unix(1, 0), time.Unix(1, 0)); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(sub, time.Unix(9, 0), time.Unix(9, 0)); err != nil {
		t.Fatal(err)
	}
	payloadPath := filepath.Join(sess, "payload.json")
	payload := fmt.Sprintf(`{"transcript_path":%q,"tool_input":{"file_path":"internal/foo.go"}}`, main)
	if err := os.WriteFile(payloadPath, []byte(payload), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runRootIn(t, "", "hook", "explain", "--payload", payloadPath)
	if err != nil {
		t.Fatalf("explain: %v\n%s", err, out)
	}
	if !strings.Contains(out, "key:        mtime heuristic") {
		t.Fatalf("want mtime heuristic key:\n%s", out)
	}
	if !strings.Contains(out, "mtime heuristic") {
		t.Fatalf("reason must name heuristic:\n%s", out)
	}
	normalized := strings.ReplaceAll(out, sess, "$SESS")
	golden := filepath.Join(pkgDir, "testdata", "hookexplain", "mtime.golden")
	wantb, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("golden %s: %v", golden, err)
	}
	if normalized != string(wantb) {
		t.Errorf("golden mismatch\ngot:\n%s\nwant:\n%s", normalized, wantb)
	}
}
