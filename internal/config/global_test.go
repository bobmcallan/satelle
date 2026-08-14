package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/testutil"
)

func TestServiceDefaults(t *testing.T) {
	testutil.IsolateHome(t)
	// IsolateHome disables UI push; this test asserts the derived default.
	t.Setenv(EnvServerEndpoint, "")
	_ = os.Unsetenv(EnvServerEndpoint)
	var s ServiceConfig
	if s.ResolvePort() != DefaultWebPort {
		t.Errorf("port default = %d", s.ResolvePort())
	}
	if s.ResolveAddr() != DefaultServiceAddr {
		t.Errorf("addr default = %q, want %q", s.ResolveAddr(), DefaultServiceAddr)
	}
	if s.ResolveEndpoint() != "http://127.0.0.1:8787" {
		t.Errorf("endpoint default = %q", s.ResolveEndpoint())
	}
}

// TestGlobalDirPanicsWithoutHomeUnderTest (sty_c36c211f): the seam refuses to
// resolve the real ~/.satelle when SATELLE_HOME is unset during go test.
func TestGlobalDirPanicsWithoutHomeUnderTest(t *testing.T) {
	t.Setenv("SATELLE_HOME", "")
	// Clear may not unset; force empty via Setenv to empty then check Lookup.
	_ = os.Unsetenv("SATELLE_HOME")
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("GlobalDir() must panic under test with no SATELLE_HOME")
		}
		msg, _ := r.(string)
		if !strings.Contains(msg, "SATELLE_HOME") || !strings.Contains(msg, "testutil.IsolateHome") {
			t.Errorf("panic message missing guidance: %v", r)
		}
	}()
	_ = GlobalDir()
}

func TestGlobalDirHonorsSATELLE_HOME(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SATELLE_HOME", home)
	if got := GlobalDir(); got != home {
		t.Fatalf("GlobalDir = %q, want %q", got, home)
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
	gc.Service.Endpoint = "http://127.0.0.1:9090"
	gc.Service.Repo = "/home/u/repo"
	if err := SaveGlobal(gc); err != nil {
		t.Fatalf("SaveGlobal: %v", err)
	}
	got, err := LoadGlobal()
	if err != nil {
		t.Fatalf("LoadGlobal: %v", err)
	}
	if got.Service.Port != 9090 || got.Service.Addr != "127.0.0.1" || got.Service.Endpoint != "http://127.0.0.1:9090" || got.Service.Repo != "/home/u/repo" {
		t.Errorf("round-trip mismatch: %+v", got.Service)
	}
}

func TestResolveEndpointPrecedence(t *testing.T) {
	testutil.IsolateHome(t)
	t.Setenv(EnvServerEndpoint, "")
	_ = os.Unsetenv(EnvServerEndpoint)
	s := ServiceConfig{Port: 9001, Endpoint: "http://explicit.example:9"}
	if got := s.ResolveEndpoint(); got != "http://explicit.example:9" {
		t.Errorf("config endpoint = %q", got)
	}
	t.Setenv(EnvServerEndpoint, "http://env.example:8")
	if got := s.ResolveEndpoint(); got != "http://env.example:8" {
		t.Errorf("env endpoint = %q", got)
	}
	t.Setenv(EnvServerEndpoint, "none")
	if got := s.ResolveEndpoint(); got != "" {
		t.Errorf("none must disable: %q", got)
	}
	t.Setenv(EnvServerEndpoint, "")
	_ = os.Unsetenv(EnvServerEndpoint)
	s.Endpoint = ""
	if got := s.ResolveEndpoint(); got != "http://127.0.0.1:9001" {
		t.Errorf("derived endpoint = %q", got)
	}
}

func TestGlobalConfigHasNoSyncField(t *testing.T) {
	rt := reflect.TypeOf(GlobalConfig{})
	if _, ok := rt.FieldByName("Sync"); ok {
		t.Fatal("GlobalConfig must not carry a sync field — [sync] stays per-repo")
	}
	for i := 0; i < rt.NumField(); i++ {
		if tag := rt.Field(i).Tag.Get("toml"); tag == "sync" {
			t.Fatalf("GlobalConfig field %s has toml:\"sync\"", rt.Field(i).Name)
		}
	}
}

func TestWorkspaceRegistryCRUDAndRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SATELLE_HOME", home)

	gc, err := LoadGlobal()
	if err != nil {
		t.Fatal(err)
	}
	if !gc.Workspace.AddRepo("/repo/a") || !gc.Workspace.AddRepo("/repo/b") {
		t.Fatal("AddRepo should report new additions")
	}
	if gc.Workspace.AddRepo("/repo/a") {
		t.Error("AddRepo should de-dup an already-registered repo")
	}
	if err := SaveGlobal(gc); err != nil {
		t.Fatal(err)
	}
	got, err := LoadGlobal()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Workspace.Repos) != 2 || got.Workspace.Repos[0] != "/repo/a" || got.Workspace.Repos[1] != "/repo/b" {
		t.Fatalf("workspace round-trip = %v", got.Workspace.Repos)
	}
	if !got.Workspace.RemoveRepo("/repo/a") || got.Workspace.RemoveRepo("/repo/a") {
		t.Error("RemoveRepo should report presence once")
	}
	if len(got.Workspace.Repos) != 1 || got.Workspace.Repos[0] != "/repo/b" {
		t.Fatalf("after remove = %v", got.Workspace.Repos)
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

func TestUIThemeRoundTrip(t *testing.T) {
	t.Setenv("SATELLE_HOME", t.TempDir())
	gc, err := LoadGlobal()
	if err != nil {
		t.Fatalf("LoadGlobal: %v", err)
	}
	if gc.UI.Theme != "" {
		t.Errorf("default UI.Theme = %q, want empty (light)", gc.UI.Theme)
	}
	gc.UI.Theme = "dark"
	if err := SaveGlobal(gc); err != nil {
		t.Fatalf("SaveGlobal: %v", err)
	}
	got, err := LoadGlobal()
	if err != nil {
		t.Fatalf("LoadGlobal (reload): %v", err)
	}
	if got.UI.Theme != "dark" {
		t.Errorf("reloaded UI.Theme = %q, want dark", got.UI.Theme)
	}
}
