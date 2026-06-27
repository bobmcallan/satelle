package config

import (
	"path/filepath"
	"testing"
)

func TestServiceDefaults(t *testing.T) {
	var s ServiceConfig
	if s.ResolvePort() != DefaultWebPort {
		t.Errorf("port default = %d", s.ResolvePort())
	}
	if s.ResolveAddr() != DefaultServiceAddr {
		t.Errorf("addr default = %q, want %q", s.ResolveAddr(), DefaultServiceAddr)
	}
}

func TestSaveLoadGlobalRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SATELLE_HOME", home)

	if GlobalDir() != home {
		t.Fatalf("GlobalDir = %q, want %q", GlobalDir(), home)
	}
	if GlobalConfigPath() != filepath.Join(home, GlobalConfigName) {
		t.Fatalf("GlobalConfigPath = %q", GlobalConfigPath())
	}

	// Absent file → defaults, no error.
	gc, err := LoadGlobal()
	if err != nil {
		t.Fatalf("LoadGlobal (absent): %v", err)
	}
	if gc.Service.ResolvePort() != DefaultWebPort {
		t.Errorf("absent port = %d", gc.Service.ResolvePort())
	}

	// Save then reload.
	gc.Service.Port = 9090
	gc.Service.Addr = "127.0.0.1"
	gc.Service.Repo = "/home/u/repo"
	if err := SaveGlobal(gc); err != nil {
		t.Fatalf("SaveGlobal: %v", err)
	}
	got, err := LoadGlobal()
	if err != nil {
		t.Fatalf("LoadGlobal: %v", err)
	}
	if got.Service.Port != 9090 || got.Service.Addr != "127.0.0.1" || got.Service.Repo != "/home/u/repo" {
		t.Errorf("round-trip mismatch: %+v", got.Service)
	}
}

func TestAgentCLIRoundTripAndDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SATELLE_HOME", home)

	// Absent → default claude.
	gc, err := LoadGlobal()
	if err != nil {
		t.Fatalf("LoadGlobal (absent): %v", err)
	}
	if gc.Agent.ResolveCLI() != DefaultAgentCLI {
		t.Errorf("absent agent cli = %q, want %q", gc.Agent.ResolveCLI(), DefaultAgentCLI)
	}

	// Persisted value survives a round-trip alongside the service block.
	gc.Agent.CLI = "codex"
	if err := SaveGlobal(gc); err != nil {
		t.Fatalf("SaveGlobal: %v", err)
	}
	got, err := LoadGlobal()
	if err != nil {
		t.Fatalf("LoadGlobal: %v", err)
	}
	if got.Agent.CLI != "codex" || got.Agent.ResolveCLI() != "codex" {
		t.Errorf("agent cli round-trip = %+v", got.Agent)
	}
}
