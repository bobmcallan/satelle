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

// TestRuntimeListClassifies (sty_c36c211f AC2): marker → linked, registry → linked,
// bare key dir → unknown; --orphans hides linked.
func TestRuntimeListClassifies(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SATELLE_HOME", home)

	// Linked via marker.
	repoMarked := t.TempDir()
	keyMarked := config.RepoKey(repoMarked)
	dirMarked := filepath.Join(home, keyMarked)
	if err := os.MkdirAll(dirMarked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := config.WriteRepoPathMarker(dirMarked, repoMarked); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dirMarked, config.DefaultDBName), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Linked via workspace registry (no marker).
	repoReg := t.TempDir()
	keyReg := config.RepoKey(repoReg)
	dirReg := filepath.Join(home, keyReg)
	if err := os.MkdirAll(dirReg, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := config.SaveGlobal(config.GlobalConfig{
		Workspace: config.WorkspaceConfig{Repos: []string{repoReg}},
	}); err != nil {
		t.Fatal(err)
	}

	// Orphan unknown (no marker, no registry).
	orphanKey := "002-deadbeef"
	dirOrphan := filepath.Join(home, orphanKey)
	if err := os.MkdirAll(dirOrphan, 0o755); err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	if err := runRuntimeList(&out, false); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if !strings.Contains(s, keyMarked) || !strings.Contains(s, "linked") {
		t.Errorf("marked dir should be linked:\n%s", s)
	}
	if !strings.Contains(s, keyReg) {
		t.Errorf("registry dir missing:\n%s", s)
	}
	if !strings.Contains(s, orphanKey) || !strings.Contains(s, "unknown") {
		t.Errorf("orphan should be unknown:\n%s", s)
	}
	if !strings.Contains(s, "rm -rf "+dirOrphan) {
		t.Errorf("orphan should get rm suggestion:\n%s", s)
	}

	var orphans strings.Builder
	if err := runRuntimeList(&orphans, true); err != nil {
		t.Fatal(err)
	}
	o := orphans.String()
	if strings.Contains(o, keyMarked) {
		t.Errorf("--orphans must hide linked marked:\n%s", o)
	}
	if !strings.Contains(o, orphanKey) {
		t.Errorf("--orphans must show unknown:\n%s", o)
	}
}
