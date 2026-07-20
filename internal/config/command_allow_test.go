package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadCommandAllow(t *testing.T) {
	dir := t.TempDir()
	data := filepath.Join(dir, "satelle.toml")
	body := `
[gate]
allow_parallel = false

[gate.command_allow]
push = ["release"]
commit = ["in_progress", "release"]
`
	if err := os.WriteFile(data, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	// Load expects .satelle/satelle.toml under a repo — write nested layout
	root := t.TempDir()
	sat := filepath.Join(root, ".satelle")
	if err := os.MkdirAll(sat, 0o755); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(sat, "satelle.toml")
	if err := os.WriteFile(cfgPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Gate.CommandAllow["push"]; len(got) != 1 || got[0] != "release" {
		t.Fatalf("push allow = %v", cfg.Gate.CommandAllow["push"])
	}
	if got := cfg.Gate.CommandAllow["commit"]; len(got) != 2 {
		t.Fatalf("commit allow = %v", cfg.Gate.CommandAllow["commit"])
	}
}
