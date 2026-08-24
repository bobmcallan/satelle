//go:build integration

package tests

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHookNoImplementE2E_InstalledWrapper(t *testing.T) {
	if testBin == "" {
		t.Skip("test binary not built")
	}
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "internal"), 0o755); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	cmd := exec.Command(testBin, "init")
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), "HOME="+home, "TMPDIR="+t.TempDir(), "SATELLE_HOME="+home, "SATELLE_SERVER_ENDPOINT=none")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	tomlPath := filepath.Join(repo, ".satelle", "satelle.toml")
	b, err := os.ReadFile(tomlPath)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, "\n[gate]\n") {
		t.Fatal("seeded satelle.toml missing [gate] table")
	}
	s = strings.Replace(s, "\n[gate]\n", "\n[gate]\nno_implement_models = [\"claude-fable*\"]\nno_implement_message = \"configured deny text\"\n", 1)
	if err := os.WriteFile(tomlPath, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(repo, ".satelle", "hooks", "satelle-hook.sh")
	if _, err := os.Stat(script); err != nil {
		t.Fatalf("missing installed hook: %v", err)
	}
	localBin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(localBin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(testBin, filepath.Join(localBin, "satelle")); err != nil {
		t.Fatal(err)
	}

	line := func(model, toolID string) []byte {
		if toolID == "" {
			return []byte(`{"message":{"model":"` + model + `","role":"assistant","content":[{"type":"text","text":"ok"}]}}` + "\n")
		}
		return []byte(`{"message":{"model":"` + model + `","role":"assistant","content":[{"type":"tool_use","id":"` + toolID + `","name":"Edit","input":{"file_path":"internal/foo.go"}}]}}` + "\n")
	}

	type pair struct {
		name      string
		leadModel string
		subModel  string
		wantDeny  bool
	}
	pairs := []pair{
		{name: "opus-sub", leadModel: "claude-fable-5", subModel: "claude-opus-4-6", wantDeny: false},
		{name: "fable-sub", leadModel: "claude-opus-4-6", subModel: "claude-fable-5", wantDeny: true},
	}
	mtimeOrders := []struct {
		name      string
		mainNewer bool
	}{
		{"main-newer", true},
		{"sub-newer", false},
	}

	env := append(os.Environ(),
		"HOME="+home,
		"SATELLE_HOME="+home,
		"SATELLE_CONFIG="+tomlPath,
		"SATELLE_SERVER_ENDPOINT=none",
		"PATH="+localBin+string(os.PathListSeparator)+os.Getenv("PATH"),
	)

	for _, p := range pairs {
		for _, mt := range mtimeOrders {
			t.Run(p.name+"/"+mt.name, func(t *testing.T) {
				sess := t.TempDir()
				main := filepath.Join(sess, "session.jsonl")
				subDir := filepath.Join(sess, "session", "subagents")
				if err := os.MkdirAll(subDir, 0o755); err != nil {
					t.Fatal(err)
				}
				sub := filepath.Join(subDir, "agent-1.jsonl")
				if err := os.WriteFile(main, line(p.leadModel, ""), 0o644); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(sub, line(p.subModel, "toolu_edit1"), 0o644); err != nil {
					t.Fatal(err)
				}
				mainT, subT := time.Unix(5, 0), time.Unix(1, 0)
				if !mt.mainNewer {
					mainT, subT = time.Unix(1, 0), time.Unix(5, 0)
				}
				if err := os.Chtimes(main, mainT, mainT); err != nil {
					t.Fatal(err)
				}
				if err := os.Chtimes(sub, subT, subT); err != nil {
					t.Fatal(err)
				}
				payload := `{"tool_use_id":"toolu_edit1","transcript_path":` + jsonQuote(main) + `,"tool_input":{"file_path":"internal/foo.go"}}`

				c := exec.Command("sh", script, "gate", "claude")
				c.Dir = repo
				c.Env = env
				c.Stdin = strings.NewReader(payload)
				out, _ := c.CombinedOutput()
				s := string(out)
				denied := strings.Contains(s, `"permissionDecision":"deny"`) && strings.Contains(s, "configured deny text")
				if p.wantDeny && !denied {
					t.Fatalf("want deny with configured message:\n%s", s)
				}
				if !p.wantDeny && denied {
					t.Fatalf("want allow, got model deny:\n%s", s)
				}
				if strings.Contains(s, "mtime heuristic") {
					t.Fatalf("tool_use_id decision must not depend on mtime:\n%s", s)
				}

				pp := filepath.Join(t.TempDir(), "p.json")
				if err := os.WriteFile(pp, []byte(payload), 0o644); err != nil {
					t.Fatal(err)
				}
				ex := exec.Command(testBin, "hook", "explain", "--payload", pp)
				ex.Dir = repo
				ex.Env = env
				exOut, err := ex.CombinedOutput()
				if err != nil {
					t.Fatalf("explain: %v\n%s", err, exOut)
				}
				got := strings.ReplaceAll(string(exOut), sess, "$SESS")
				golden := filepath.Join("testdata", "hookexplain", "e2e-"+p.name+".golden")
				wantb, err := os.ReadFile(golden)
				if err != nil {
					t.Fatalf("golden %s: %v", golden, err)
				}
				if got != string(wantb) {
					t.Errorf("explain golden mismatch\ngot:\n%s\nwant:\n%s", got, wantb)
				}
			})
		}
	}
}

func jsonQuote(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(b)
}
