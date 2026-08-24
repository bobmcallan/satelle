package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
)

func TestNoImplementHandoverNotice(t *testing.T) {
	settings := []byte(`{
  "hooks": {
    "PreToolUse": [
      {"matcher": "Edit|Write", "hooks": [{"type": "command", "command": "/home/me/.claude/hooks/operator-script.sh"}]}
    ]
  }
}`)
	on := noImplementHandoverNotice(config.GateConfig{NoImplementModels: []string{"claude-fable*"}}, settings)
	if !strings.Contains(on, "satelle-owned") {
		t.Fatalf("want satelle-owned notice, got %q", on)
	}
	if !strings.Contains(on, "operator-script.sh") {
		t.Fatalf("notice must interpolate the found command: %q", on)
	}
	if off := noImplementHandoverNotice(config.GateConfig{}, settings); off != "" {
		t.Fatalf("without keys notice must be empty, got %q", off)
	}
}

func TestInitPrintsNoImplementHandover(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, ".satelle"), 0o755); err != nil {
		t.Fatal(err)
	}
	toml := "[review]\ngate_create = false\n[gate]\nno_implement_models = [\"claude-fable*\"]\n"
	if err := os.WriteFile(filepath.Join(repo, ".satelle", "satelle.toml"), []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := runInitTest(t, &out, repo); err != nil {
		t.Fatalf("init: %v\n%s", err, out.String())
	}
	home := os.Getenv("HOME")
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	settings := []byte(`{"hooks":{"PreToolUse":[{"matcher":"Edit|Write","hooks":[{"type":"command","command":"/x/operator-script.sh"}]}]}}`)
	if err := os.WriteFile(filepath.Join(home, ".claude", "settings.json"), settings, 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := runInitTest(t, &out, repo); err != nil {
		t.Fatalf("re-init: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "satelle-owned") {
		t.Fatalf("init stdout missing handover notice:\n%s", out.String())
	}

	repo2 := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo2, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := runInitTest(t, &out, repo2); err != nil {
		t.Fatalf("init without keys: %v\n%s", err, out.String())
	}
	if strings.Contains(out.String(), "satelle-owned") {
		t.Fatalf("without no_implement_models init must print no notice:\n%s", out.String())
	}
}
