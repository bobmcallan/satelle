package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
)

// pickEmbeddedSkill returns one embedded default from a restorable kind.
func pickEmbeddedSkill(t *testing.T) config.EmbeddedDefault {
	t.Helper()
	for _, d := range config.EmbeddedDefaults() {
		if d.Kind == "skills" {
			return d
		}
	}
	t.Fatal("no embedded skill defaults found")
	return config.EmbeddedDefault{}
}

// TestRunRestoreOverwritesDrift proves restore is the inverse of init's
// never-clobber (sty_9e2426b3): a drifted embedded default is restored to the
// canonical bytes when confirmed.
func TestRunRestoreOverwritesDrift(t *testing.T) {
	dataDir := t.TempDir()
	d := pickEmbeddedSkill(t)
	p := filepath.Join(dataDir, "skills", d.Name+".md")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("drifted content"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	if err := runRestore(&out, strings.NewReader(""), dataDir, true); err != nil {
		t.Fatalf("restore --yes: %v", err)
	}
	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != d.Body {
		t.Errorf("drifted file was not restored to the embedded default")
	}
	if !strings.Contains(out.String(), "re-materialised") {
		t.Errorf("restore should report what it did:\n%s", out.String())
	}
	// The baseline workflow must NOT be materialised (embedded-only by design).
	if _, err := os.Stat(filepath.Join(dataDir, "workflows", "satelle-baseline-workflow.md")); err == nil {
		t.Error("restore must not write the baseline workflow to disk")
	}
}

// TestRunRestoreAbortsWithoutConfirmation proves the destructive path requires
// an explicit yes: on refusal nothing is written (sty_9e2426b3).
func TestRunRestoreAbortsWithoutConfirmation(t *testing.T) {
	dataDir := t.TempDir()
	d := pickEmbeddedSkill(t)
	p := filepath.Join(dataDir, "skills", d.Name+".md")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("drifted content"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	// "no" on stdin — anything but an explicit yes aborts.
	if err := runRestore(&out, strings.NewReader("no\n"), dataDir, false); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if !strings.Contains(out.String(), "aborted") {
		t.Errorf("refusal should report the abort:\n%s", out.String())
	}
	got, _ := os.ReadFile(p)
	if string(got) != "drifted content" {
		t.Error("nothing must be written without confirmation")
	}
}

// TestRunRestoreNoopWhenClean: with every default in place, restore reports
// nothing to do (and needs no confirmation).
func TestRunRestoreNoopWhenClean(t *testing.T) {
	dataDir := t.TempDir()
	for _, d := range config.EmbeddedDefaults() {
		if d.Kind == "workflows" {
			continue
		}
		p := filepath.Join(dataDir, d.Kind, d.Name+".md")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(d.Body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var out strings.Builder
	if err := runRestore(&out, strings.NewReader(""), dataDir, false); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if !strings.Contains(out.String(), "nothing to do") {
		t.Errorf("clean repo should be a no-op:\n%s", out.String())
	}
}
