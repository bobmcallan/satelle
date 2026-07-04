package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSaveHostedServerAppends(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "satelle.toml")
	original := "# comment\n[review]\ngate_create = true\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SaveHostedServer(path, "https://h/", "proj"); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	s := string(got)
	// Surrounding content preserved.
	if !strings.Contains(s, "# comment") || !strings.Contains(s, "gate_create = true") {
		t.Fatalf("surrounding content lost:\n%s", s)
	}
	if !strings.Contains(s, "[hosted]") || !strings.Contains(s, `server = "https://h"`) || !strings.Contains(s, `project = "proj"`) {
		t.Fatalf("hosted section missing:\n%s", s)
	}

	// Round-trip through Load.
	cfg, _, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Hosted.Server != "https://h" || cfg.Hosted.Project != "proj" {
		t.Fatalf("loaded hosted = %+v", cfg.Hosted)
	}
	if !cfg.Review.GateCreate {
		t.Fatal("unrelated [review] key clobbered")
	}
}

func TestSaveHostedServerReplacesInPlace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "satelle.toml")
	original := "[hosted]\nserver = \"https://old\"\nproject = \"old\"\n\n[review]\ngate_create = true\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SaveHostedServer(path, "https://new", ""); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	s := string(got)
	if strings.Contains(s, "https://old") {
		t.Fatalf("old server not replaced:\n%s", s)
	}
	if !strings.Contains(s, `server = "https://new"`) {
		t.Fatalf("new server missing:\n%s", s)
	}
	// An empty project omits the key.
	if strings.Contains(s, "project =") {
		t.Fatalf("project should be omitted:\n%s", s)
	}
	// The following table must survive.
	if !strings.Contains(s, "[review]") || !strings.Contains(s, "gate_create = true") {
		t.Fatalf("following [review] table lost:\n%s", s)
	}
}

func TestSaveHostedServerCreatesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".satelle", "satelle.toml")
	if err := SaveHostedServer(path, "https://h", "p"); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Hosted.Server != "https://h" {
		t.Fatalf("hosted = %+v", cfg.Hosted)
	}
}

// Tokens must never be written to the committed config by this writer.
func TestSaveHostedServerNoTokens(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "satelle.toml")
	if err := SaveHostedServer(path, "https://h", "p"); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if strings.Contains(string(got), "token") {
		t.Fatalf("committed config must not contain any token:\n%s", got)
	}
}
