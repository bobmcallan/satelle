package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/store"
)

func TestSubstratePruneRemovesOnlyUneditedSeeds(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SATELLE_HOME", home)
	repo := t.TempDir()
	dataDir := filepath.Join(repo, config.DefaultDataDir)
	if err := os.MkdirAll(filepath.Join(dataDir, "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Unedited seed (stamped current default).
	body, ok := embeddedDefault("skills", "satelle-step-summary")
	if !ok {
		t.Fatal("embedded step-summary missing")
	}
	stamped := applyEmbeddedStamp(body, embeddedSHA(body))
	seedPath := filepath.Join(dataDir, "skills", "satelle-step-summary.md")
	if err := os.WriteFile(seedPath, []byte(stamped), 0o644); err != nil {
		t.Fatal(err)
	}
	// Edited override (different body).
	editedPath := filepath.Join(dataDir, "skills", "satelle-workflow-advisor.md")
	if err := os.WriteFile(editedPath, []byte("---\nname: satelle-workflow-advisor\ntype: skill\ndescription: operator edit\n---\n\n# Edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Authored-only skill.
	authoredPath := filepath.Join(dataDir, "skills", "my-own-skill.md")
	if err := os.WriteFile(authoredPath, []byte("---\nname: my-own-skill\ntype: skill\ndescription: d\n---\n\n# Own\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	if err := runSubstratePrune(&out, strings.NewReader(""), dataDir, BackupOpts{LocalOnly: true}, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "satelle-step-summary") || !strings.Contains(out.String(), "dry-run") {
		t.Fatalf("dry-run should list step-summary:\n%s", out.String())
	}
	if fileExists(seedPath) && !fileExists(editedPath) {
		t.Fatal("dry-run must not remove files")
	}

	out.Reset()
	if err := runSubstratePrune(&out, strings.NewReader(""), dataDir, BackupOpts{LocalOnly: true, BackupsDir: t.TempDir()}, true); err != nil {
		t.Fatal(err)
	}
	if fileExists(seedPath) {
		t.Error("prune --yes must remove unedited seed")
	}
	if !fileExists(editedPath) {
		t.Error("prune must never touch edited file")
	}
	if !fileExists(authoredPath) {
		t.Error("prune must never touch authored file")
	}
}

func TestSubstrateListEditedProvenance(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SATELLE_HOME", home)
	db, err := store.Open(filepath.Join(t.TempDir(), "satelle.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// Index an edited on-disk skill that shadows an embedded default.
	dataDir := t.TempDir()
	skillDir := filepath.Join(dataDir, "skills")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	edited := "---\nname: satelle-step-summary\ntype: skill\ndescription: operator override\n---\n\n# Operator rewrite\n\ncustom body\n"
	if err := os.WriteFile(filepath.Join(skillDir, "satelle-step-summary.md"), []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DocIndex.Sync(t.Context(), map[string]string{"skills": skillDir}, time.Now()); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := runSubstrateList(&out, db.DocIndex, true); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if !strings.Contains(s, `"name": "satelle-step-summary"`) {
		t.Fatalf("list missing skill:\n%s", s)
	}
	if !strings.Contains(s, `"provenance": "edited"`) {
		t.Fatalf("expected edited provenance:\n%s", s)
	}
}
