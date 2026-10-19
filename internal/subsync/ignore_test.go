package subsync

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
)

// config/.env is never written by a restore, with no .satelleignore at all.
func TestRestoreNeverWritesTheDefaultEnvExclusion(t *testing.T) {
	dir := t.TempDir()
	res, err := Restore(dir, []File{
		{Path: "config/.env", Content: []byte("TOKEN=hosted")},
		{Path: "skills/a.md", Content: []byte("a")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Skipped) != 1 || res.Skipped[0] != "config/.env" || res.Written != 1 {
		t.Errorf("Skipped = %v, Written = %d; want config/.env skipped and one file written", res.Skipped, res.Written)
	}
	if _, err := os.Stat(filepath.Join(dir, "config", ".env")); !os.IsNotExist(err) {
		t.Errorf("config/.env was written: %v", err)
	}
	if !ExcludedLocal("config/.env") {
		t.Error("ExcludedLocal(config/.env) = false")
	}
}

// A path the repo's .satelleignore lists is skipped by Restore and left alone by
// Remove, exactly as a .local path is.
func TestRestoreAndRemoveHonourSatelleignore(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, config.IgnoreFileName), []byte("secret.md\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := Restore(dir, []File{
		{Path: "skills/secret.md", Content: []byte("hosted")},
		{Path: "skills/keep.md", Content: []byte("k")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Skipped) != 1 || res.Skipped[0] != "skills/secret.md" || res.Written != 1 {
		t.Errorf("Skipped = %v, Written = %d", res.Skipped, res.Written)
	}
	if _, err := os.Stat(filepath.Join(dir, "skills", "secret.md")); !os.IsNotExist(err) {
		t.Errorf("an ignored path was written: %v", err)
	}

	local := filepath.Join(dir, "skills", "secret.md")
	if err := os.WriteFile(local, []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	removed, err := Remove(dir, []string{"skills/secret.md", "skills/keep.md"})
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || removed[0] != "skills/keep.md" {
		t.Errorf("removed = %v, want only skills/keep.md", removed)
	}
	if _, err := os.Stat(local); err != nil {
		t.Errorf("an ignored local file was removed: %v", err)
	}
}

func TestExcludedLocalByUsesTheLoadedIgnore(t *testing.T) {
	ign := config.ParseIgnore("secret.md\n")
	if !ExcludedLocalBy("skills/secret.md", ign) || ExcludedLocal("skills/secret.md") {
		t.Error("ExcludedLocalBy must apply the loaded ignore and ExcludedLocal must not")
	}
}
