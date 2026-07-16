package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/store"
)

func TestRuntimePathReportsLegacy(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SATELLE_HOME", home)
	repo := t.TempDir()
	dataDir := filepath.Join(repo, config.DefaultDataDir)
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Real sqlite so later migrate tests share the fixture shape.
	dbPath := filepath.Join(dataDir, config.DefaultDBName)
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	var out strings.Builder
	if err := runRuntimePath(&out, config.Config{}, repo); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if !strings.Contains(s, "layout       legacy") {
		t.Errorf("expected legacy layout:\n%s", s)
	}
	if !strings.Contains(s, dataDir) {
		t.Errorf("expected runtime_dir under data_dir:\n%s", s)
	}
}

func TestRuntimeMigrateCopiesAndLeavesLegacy(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SATELLE_HOME", home)
	repo := t.TempDir()
	dataDir := filepath.Join(repo, config.DefaultDataDir)
	if err := os.MkdirAll(filepath.Join(dataDir, "logs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "logs", "operations.log"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(dataDir, config.DefaultDBName)
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	a := &app.App{
		Config:   config.Config{},
		RepoRoot: repo,
		DataDir:  dataDir,
	}
	var out strings.Builder
	if err := runRuntimeMigrate(&out, a, false, false); err != nil {
		t.Fatalf("migrate: %v\n%s", err, out.String())
	}
	target := filepath.Join(home, config.RepoKey(repo), config.DefaultDBName)
	if !fileExists(target) {
		t.Fatalf("target db missing: %s\n%s", target, out.String())
	}
	// Legacy remains.
	if !fileExists(dbPath) {
		t.Fatal("legacy db was removed — migrate must leave it")
	}
	// Logs copied.
	if !fileExists(filepath.Join(home, config.RepoKey(repo), "logs", "operations.log")) {
		t.Fatal("logs/ not copied")
	}
	// Second migrate without --force refuses.
	if err := runRuntimeMigrate(&strings.Builder{}, a, false, false); err == nil {
		t.Fatal("expected refuse when target exists")
	}
	// After migrate, ResolveRuntimeDir prefers home.
	res := config.Config{}.ResolveRuntimeDir(repo)
	if res.Legacy {
		t.Fatal("after migrate, layout should not be legacy")
	}
	if note := (config.Config{}).LegacyRuntimeNote(repo); note != "" {
		t.Errorf("after migrate, no deprecation note expected, got %q", note)
	}
}

func TestRuntimeMigrateDryRun(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SATELLE_HOME", home)
	repo := t.TempDir()
	dataDir := filepath.Join(repo, config.DefaultDataDir)
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(filepath.Join(dataDir, config.DefaultDBName))
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	a := &app.App{Config: config.Config{}, RepoRoot: repo, DataDir: dataDir}
	var out strings.Builder
	if err := runRuntimeMigrate(&out, a, false, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "dry-run") {
		t.Errorf("expected dry-run report:\n%s", out.String())
	}
	target := filepath.Join(home, config.RepoKey(repo), config.DefaultDBName)
	if fileExists(target) {
		t.Fatal("dry-run must not create target db")
	}
}

