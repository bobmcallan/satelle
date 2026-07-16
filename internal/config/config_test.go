package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestZeroConfigDefaults(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SATELLE_HOME", home)
	var c Config
	repo := "/repo"
	if got := c.ResolveDataDir(repo); got != "/repo/.satelle" {
		t.Errorf("ResolveDataDir = %q", got)
	}
	// Default DB is home-keyed under ~/.satelle/<repo-key>/ (sty_4660bbe1).
	wantDB := filepath.Join(home, RepoKey(repo), DefaultDBName)
	if got := c.ResolveDB(repo); got != wantDB {
		t.Errorf("ResolveDB = %q, want %q", got, wantDB)
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

func TestResolveEditExemptPaths(t *testing.T) {
	// Unset → nil (default binary exempts only the data dir).
	var zero Config
	if got := zero.ResolveEditExemptPaths("/repo"); got != nil {
		t.Errorf("unconfigured ResolveEditExemptPaths = %v, want nil", got)
	}
	// Blanks/whitespace dropped; relative resolved under repo; absolute passes
	// through. filepath.Join strips the trailing slash of ".claude/".
	c := Config{Gate: GateConfig{EditExemptPaths: []string{".claude/", "", "  ", "/opt/authoring"}}}
	got := c.ResolveEditExemptPaths("/repo")
	want := []string{"/repo/.claude", "/opt/authoring"}
	if len(got) != len(want) {
		t.Fatalf("ResolveEditExemptPaths = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ResolveEditExemptPaths[%d] = %q, want %q", i, got[i], want[i])
		}
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

// TestLoadVarsOverlay pins the per-key merge the env substitution relies on: a
// committed [vars] provides non-secret defaults, and the gitignored local overlay
// adds/overrides keys WITHOUT dropping committed-only keys (sty_001558ce).
func TestLoadVarsOverlay(t *testing.T) {
	repo := t.TempDir()
	satelleDir := filepath.Join(repo, ".satelle")
	if err := os.MkdirAll(satelleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	committed := "[vars]\nA = \"1\"\nB = \"2\"\n"
	if err := os.WriteFile(filepath.Join(satelleDir, ConfigName), []byte(committed), 0o644); err != nil {
		t.Fatal(err)
	}
	// Overlay overrides B and adds C; A (committed only) must survive.
	local := "[vars]\nB = \"9\"\nC = \"3\"\n"
	if err := os.WriteFile(filepath.Join(satelleDir, LocalConfigName), []byte(local), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := Load(filepath.Join(satelleDir, ConfigName))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"A": "1", "B": "9", "C": "3"}
	for k, v := range want {
		if cfg.Vars[k] != v {
			t.Errorf("vars[%q] = %q, want %q (full: %v)", k, cfg.Vars[k], v, cfg.Vars)
		}
	}
}

// TestLoadSyncOverlay pins the same per-key merge for [sync]: a committed table
// sets area scopes for the team, and the gitignored local overlay overrides a
// single area WITHOUT dropping the committed-only areas (sty_a2d2e057, plan
// addendum #1).
func TestLoadSyncOverlay(t *testing.T) {
	repo := t.TempDir()
	satelleDir := filepath.Join(repo, ".satelle")
	if err := os.MkdirAll(satelleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	committed := "[sync]\nskills = \"shared\"\ndocuments = \"personal\"\n"
	if err := os.WriteFile(filepath.Join(satelleDir, ConfigName), []byte(committed), 0o644); err != nil {
		t.Fatal(err)
	}
	// Overlay overrides documents; skills (committed only) must survive.
	local := "[sync]\ndocuments = \"local\"\n"
	if err := os.WriteFile(filepath.Join(satelleDir, LocalConfigName), []byte(local), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := Load(filepath.Join(satelleDir, ConfigName))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"skills": "shared", "documents": "local"}
	for k, v := range want {
		if cfg.Sync[k] != v {
			t.Errorf("sync[%q] = %q, want %q (full: %v)", k, cfg.Sync[k], v, cfg.Sync)
		}
	}
}
