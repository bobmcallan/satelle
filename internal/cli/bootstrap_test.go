package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
)

// runRoot executes a fresh root command with args, returning combined output.
// Always drains UI push and closes the store (mirrors Execute's error-path
// cleanup — Cobra skips PersistentPostRunE when RunE fails).
func runRoot(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs(args)
	c, err := root.ExecuteC()
	if c != nil {
		closeAppForCmd(c)
	}
	return buf.String(), err
}

// tempRepo creates a repo with .satelle/satelle.toml and points SATELLE_CONFIG
// at it, so config resolution lands there without a process-global chdir.
// Isolates SATELLE_HOME so the home-keyed runtime plane (sty_4660bbe1) does not
// write into the developer's real ~/.satelle during tests.
func tempRepo(t *testing.T) string {
	t.Helper()
	t.Setenv("SATELLE_HOME", t.TempDir())
	repo := t.TempDir()
	satelleDir := filepath.Join(repo, ".satelle")
	if err := os.MkdirAll(satelleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(satelleDir, "satelle.toml")
	if err := os.WriteFile(cfgPath, []byte("web_port = 8181\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// An initialized repo must carry its agents layer — store-backed commands
	// refuse without it (requireAgents, sty_d0d6bb67). init always seeds it; the
	// hand-scaffolded test repo does the same.
	if err := os.WriteFile(filepath.Join(satelleDir, "agents.toml"), []byte("[executor]\nharness = \"in-loop\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SATELLE_CONFIG", cfgPath)
	return repo
}

// runtimeDBPath returns the resolved satelle.db path for the current SATELLE_CONFIG
// / SATELLE_HOME env (home-keyed by default).
func runtimeDBPath(t *testing.T) string {
	t.Helper()
	cfg, cfgPath, err := config.Load("")
	if err != nil && err != config.ErrNotFound {
		t.Fatal(err)
	}
	repoRoot := "."
	if cfgPath != "" {
		repoRoot = config.RepoRootFromConfigPath(cfgPath)
	}
	return cfg.ResolveDB(repoRoot)
}

func TestVersionDoesNotCreateDB(t *testing.T) {
	repo := tempRepo(t)
	out, err := runRoot(t, "version")
	if err != nil {
		t.Fatalf("version: %v", err)
	}
	if !strings.HasPrefix(out, "satelle ") {
		t.Errorf("version output = %q", out)
	}
	if _, statErr := os.Stat(filepath.Join(repo, ".satelle", "satelle.db")); statErr == nil {
		t.Error("version created a database — bootstrap should not open the store")
	}
}

func TestReindexThenStatus(t *testing.T) {
	repo := tempRepo(t)
	docs := filepath.Join(repo, ".satelle", "documents")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(docs, "intro.md"), []byte("# Intro\n\nhi"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := runRoot(t, "reindex")
	if err != nil {
		t.Fatalf("index: %v\n%s", err, out)
	}
	if !strings.Contains(out, `"indexed": 1`) {
		t.Errorf("index output = %q, want indexed:1", out)
	}

	out, err = runRoot(t, "status")
	if err != nil {
		t.Fatalf("status: %v\n%s", err, out)
	}
	for _, want := range []string{repo, "indexed documents", "web port", "8181", "stories"} {
		if !strings.Contains(out, want) {
			t.Errorf("status output missing %q:\n%s", want, out)
		}
	}
}
