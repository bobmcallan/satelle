package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestResolveActiveWorkspaceDefault covers the zero-config default: an unset
// (or blank) [hosted] workspace resolves to the developer's own personal
// workspace, needing no binding (AC2).
func TestResolveActiveWorkspaceDefault(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
	}{
		{"unset", Config{}},
		{"blank", Config{Hosted: HostedConfig{Workspace: "   "}}},
		{"personal sentinel", Config{Hosted: HostedConfig{Workspace: PersonalWorkspace}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.cfg.ResolveActiveWorkspace()
			if !got.IsPersonal() {
				t.Fatalf("%s: expected personal, got %+v", tc.name, got)
			}
			if got.Name != PersonalWorkspace || got.Kind != WorkspaceKindPersonal {
				t.Fatalf("%s: got %+v", tc.name, got)
			}
		})
	}
}

// TestResolveActiveWorkspaceTeam covers an elected team workspace: an explicit
// non-personal value resolves to a team workspace carrying that name handle.
func TestResolveActiveWorkspaceTeam(t *testing.T) {
	got := (Config{Hosted: HostedConfig{Workspace: "Acme Team"}}).ResolveActiveWorkspace()
	if got.IsPersonal() {
		t.Fatalf("expected team, got personal")
	}
	if got.Name != "Acme Team" || got.Kind != WorkspaceKindTeam {
		t.Fatalf("got %+v", got)
	}
}

// TestResolveActiveWorkspaceOverlayWins proves the satelle.local.toml overlay
// overrides a committed team default per-key (AC1's "resolved through the
// satelle.local.toml overlay"): a developer's personal choice wins over the
// committed team value.
func TestResolveActiveWorkspaceOverlayWins(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, DefaultDataDir, ConfigName)
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	// Committed team default.
	if err := os.WriteFile(cfgPath, []byte("[hosted]\nworkspace = \"Acme Team\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Per-developer overlay overrides it with the personal default.
	localPath := filepath.Join(dir, DefaultDataDir, LocalConfigName)
	if err := os.WriteFile(localPath, []byte("[hosted]\nworkspace = \"personal\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, _, err := Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	got := cfg.ResolveActiveWorkspace()
	if !got.IsPersonal() {
		t.Fatalf("overlay should win → personal, got %+v", got)
	}

	// And the reverse: a committed personal default overridden by a team overlay.
	if err := os.WriteFile(cfgPath, []byte("[hosted]\nworkspace = \"personal\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(localPath, []byte("[hosted]\nworkspace = \"Beta Squad\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, _, err = Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	got = cfg.ResolveActiveWorkspace()
	if got.Kind != WorkspaceKindTeam || got.Name != "Beta Squad" {
		t.Fatalf("overlay should win → team Beta Squad, got %+v", got)
	}
}
