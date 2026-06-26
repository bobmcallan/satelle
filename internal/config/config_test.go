package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestZeroConfigDefaults(t *testing.T) {
	var c Config
	repo := "/repo"
	if got := c.ResolveDataDir(repo); got != "/repo/.satelle" {
		t.Errorf("ResolveDataDir = %q", got)
	}
	if got := c.ResolveDB(repo); got != "/repo/.satelle/satelle.db" {
		t.Errorf("ResolveDB = %q", got)
	}
	if got := c.ResolveWebPort(); got != DefaultWebPort {
		t.Errorf("ResolveWebPort = %d", got)
	}
	if got := c.ResolveLogLevel(); got != "info" {
		t.Errorf("ResolveLogLevel = %q", got)
	}
}

func TestResolveDBOverride(t *testing.T) {
	c := Config{DB: "/abs/custom.db"}
	if got := c.ResolveDB("/repo"); got != "/abs/custom.db" {
		t.Errorf("absolute db override = %q", got)
	}
	c = Config{DB: "data/x.db"}
	if got := c.ResolveDB("/repo"); got != "/repo/data/x.db" {
		t.Errorf("relative db override = %q", got)
	}
}

func TestResolveAuthoredDirs(t *testing.T) {
	c := Config{SubstrateRoots: map[string]string{
		"skills": "/elsewhere", // absolute override → /elsewhere/skills
	}}
	dirs := c.ResolveAuthoredDirs("/repo")
	if got := dirs["documents"]; got != "/repo/.satelle/documents" {
		t.Errorf("default documents dir = %q", got)
	}
	if got := dirs["skills"]; got != "/elsewhere/skills" {
		t.Errorf("overridden skills dir = %q", got)
	}
	if len(dirs) != len(AuthoredKinds) {
		t.Errorf("got %d dirs, want %d", len(dirs), len(AuthoredKinds))
	}
}

func TestLoadWithLocalOverlay(t *testing.T) {
	repo := t.TempDir()
	satelleDir := filepath.Join(repo, ".satelle")
	if err := os.MkdirAll(satelleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	committed := "web_port = 9000\nlog_level = \"warn\"\n"
	if err := os.WriteFile(filepath.Join(satelleDir, ConfigName), []byte(committed), 0o644); err != nil {
		t.Fatal(err)
	}
	// Overlay overrides web_port but not log_level.
	local := "web_port = 9999\n"
	if err := os.WriteFile(filepath.Join(satelleDir, LocalConfigName), []byte(local), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, path, err := Load(filepath.Join(satelleDir, ConfigName))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ResolveWebPort() != 9999 {
		t.Errorf("overlay web_port = %d, want 9999", cfg.ResolveWebPort())
	}
	if cfg.ResolveLogLevel() != "warn" {
		t.Errorf("committed log_level lost: %q", cfg.ResolveLogLevel())
	}
	if RepoRootFromConfigPath(path) != repo {
		t.Errorf("repo root = %q, want %q", RepoRootFromConfigPath(path), repo)
	}
}
