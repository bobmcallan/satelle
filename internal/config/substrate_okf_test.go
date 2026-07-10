package config

import (
	"io/fs"
	"path"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/structure"
)

// TestEmbeddedSubstrateTagsAreOKF asserts the embedded canonical substrate (the
// defaults that ship in the binary) carries NO legacy `kind:` tag prefix —
// classification lives in the OKF `type:` scalar and `type:` tags (sty_bf2e6ee6).
func TestEmbeddedSubstrateTagsAreOKF(t *testing.T) {
	err := fs.WalkDir(substrateFS, "substrate", func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() || !strings.HasSuffix(p, ".md") {
			return walkErr
		}
		b, rerr := substrateFS.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		for _, ln := range strings.Split(string(b), "\n") {
			trimmed := strings.TrimSpace(ln)
			if strings.HasPrefix(trimmed, "tags:") && strings.Contains(ln, "kind:") {
				t.Errorf("%s: tags carry a legacy kind: prefix — use type: (OKF): %s", p, trimmed)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestEmbeddedSubstrateStructure runs the deterministic structure check on every
// embedded substrate doc — the canonical defaults that ship in the binary must be
// OKF/structure-conformant (sty_31069c05). This also covers the embedded layer
// that the file-based per-noun validators (which walk .satelle/) does not see.
func TestEmbeddedSubstrateStructure(t *testing.T) {
	err := fs.WalkDir(substrateFS, "substrate", func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() || !strings.HasSuffix(p, ".md") {
			return walkErr
		}
		base := path.Base(p)
		if base == "README.md" || base == "index.md" || base == "log.md" {
			return nil // reserved keep-files are exempt
		}
		kind := path.Base(path.Dir(p)) // substrate/<kind>/<file>.md
		if !structure.Checked(kind) {
			return nil
		}
		b, rerr := substrateFS.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		name := strings.TrimSuffix(base, ".md")
		for _, prob := range structure.Doc(kind, name, string(b), nil) {
			t.Errorf("embedded %s/%s: %s", kind, name, prob)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestEmbeddedSubstrateAuditTask asserts the embedded default tasks kind carries the
// repo-agnostic substrate-audit task (sty_d4360e90): EmbeddedDefaults surfaces it, it
// passes structure.CheckTask, and its body references only the repo's own .satelle/
// paths (no internal/config/substrate — dev-repo-only — and no foreign ids).
func TestEmbeddedSubstrateAuditTask(t *testing.T) {
	var got *EmbeddedDefault
	for _, d := range EmbeddedDefaults() {
		if d.Kind == "tasks" && d.Name == "tsk_substrate-audit" {
			dd := d
			got = &dd
			break
		}
	}
	if got == nil {
		t.Fatal("EmbeddedDefaults does not surface tasks/tsk_substrate-audit")
	}
	if problems := structure.CheckTask(got.Body); len(problems) > 0 {
		t.Errorf("embedded audit task fails CheckTask: %v", problems)
	}
	for _, bad := range []string{"internal/config/substrate"} {
		if strings.Contains(got.Body, bad) {
			t.Errorf("embedded audit task is not repo-agnostic (references %q)", bad)
		}
	}
}

// TestEmbeddedReviewerObjectiveAuditTask asserts the embedded default tasks kind
// carries the repo-agnostic reviewer-objective-audit task: EmbeddedDefaults surfaces
// it, it passes structure.CheckTask, body stays repo-agnostic, and the paired skill
// is embedded under skills/.
func TestEmbeddedReviewerObjectiveAuditTask(t *testing.T) {
	var got *EmbeddedDefault
	for _, d := range EmbeddedDefaults() {
		if d.Kind == "tasks" && d.Name == "tsk_reviewer-objective-audit" {
			dd := d
			got = &dd
			break
		}
	}
	if got == nil {
		t.Fatal("EmbeddedDefaults does not surface tasks/tsk_reviewer-objective-audit")
	}
	if problems := structure.CheckTask(got.Body); len(problems) > 0 {
		t.Errorf("embedded reviewer-objective-audit task fails CheckTask: %v", problems)
	}
	for _, bad := range []string{"internal/config/substrate"} {
		if strings.Contains(got.Body, bad) {
			t.Errorf("embedded reviewer-objective-audit task is not repo-agnostic (references %q)", bad)
		}
	}
	var skill *EmbeddedDefault
	for _, d := range EmbeddedDefaults() {
		if d.Kind == "skills" && d.Name == "satelle-reviewer-objective-audit" {
			dd := d
			skill = &dd
			break
		}
	}
	if skill == nil {
		t.Fatal("EmbeddedDefaults does not surface skills/satelle-reviewer-objective-audit")
	}
	if !strings.Contains(skill.Body, "Given what was presented") {
		t.Error("embedded skill missing primary-objective phrasing")
	}
}

