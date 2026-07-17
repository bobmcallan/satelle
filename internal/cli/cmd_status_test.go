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

// TestStatusWarnsOnInRepoStoriesResidue (sty_58fa970e AC4): when runtime is
// home-keyed and dataDir/stories exists, status names the residue + cleanup;
// absent dir stays silent.
func TestStatusWarnsOnInRepoStoriesResidue(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SATELLE_HOME", home)
	repo := t.TempDir()
	dataDir := filepath.Join(repo, config.DefaultDataDir)
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Home-keyed DB so layout is not legacy.
	rt := filepath.Join(home, config.RepoKey(repo))
	if err := os.MkdirAll(rt, 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(filepath.Join(rt, config.DefaultDBName))
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	a := &app.App{
		Config:     config.Config{},
		RepoRoot:   repo,
		DataDir:    dataDir,
		RuntimeDir: rt,
		DBPath:     filepath.Join(rt, config.DefaultDBName),
	}
	if note := a.Config.LegacyRuntimeNote(a.RepoRoot); note != "" {
		t.Fatalf("want home-keyed layout, got: %s", note)
	}

	// With residue.
	if err := os.MkdirAll(filepath.Join(dataDir, "stories", "sty_x"), 0o755); err != nil {
		t.Fatal(err)
	}
	warn := runtimeResidueLine(a)
	if !strings.Contains(warn, "runtime residue") || !strings.Contains(warn, "rm -rf") {
		t.Fatalf("want residue warning with cleanup, got %q", warn)
	}
	if !strings.Contains(warn, filepath.Join(dataDir, "stories")) {
		t.Fatalf("want residue path named, got %q", warn)
	}

	// Absent residue is silent.
	_ = os.RemoveAll(filepath.Join(dataDir, "stories"))
	if got := runtimeResidueLine(a); got != "" {
		t.Fatalf("absent stories dir must be silent, got %q", got)
	}
}
