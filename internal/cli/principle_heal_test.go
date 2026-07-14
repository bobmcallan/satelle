package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
)

// TestRunInitHealsPrincipleFrontmatter (sty_cc8ce91c): init on a repo whose only
// fatals are inert scope: + principles:always heals them, reports migration,
// and validates green (constitution still WARNs if scaffold).
func TestRunInitHealsPrincipleFrontmatter(t *testing.T) {
	repo := t.TempDir()
	var out1 strings.Builder
	if err := runInitTest(t, &out1, repo); err != nil {
		t.Fatalf("first init: %v\n%s", err, out1.String())
	}
	// Author constitution so only principle drift can fail.
	if err := os.WriteFile(filepath.Join(repo, ".satelle", "constitution.md"), []byte("# Authored constitution\n\nProject rules.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Inject drifts on a custom principle.
	princ := filepath.Join(repo, ".satelle", "principles", "drift-me.md")
	body := `---
name: drift-me
scope: system
type: principle
tags: [type:principle, principles:always]
description: Drifted principle used to prove init heals frontmatter.
---

# Drift me

Authored body must survive the heal unchanged.
`
	if err := os.WriteFile(princ, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	// Remove deployed.version so re-init is a real heal path.
	_ = os.Remove(filepath.Join(repo, ".satelle", "deployed.version"))

	var out2 strings.Builder
	if err := runInitTest(t, &out2, repo); err != nil {
		t.Fatalf("heal init failed: %v\n%s", err, out2.String())
	}
	s := out2.String()
	if !strings.Contains(s, "migrated:") || !strings.Contains(s, "scope:") {
		t.Fatalf("want migration report for scope:\n%s", s)
	}
	if !strings.Contains(s, "principles:always") {
		t.Fatalf("want always→session migration report:\n%s", s)
	}
	got, err := os.ReadFile(princ)
	if err != nil {
		t.Fatal(err)
	}
	gs := string(got)
	if strings.Contains(gs, "scope:") || strings.Contains(gs, "principles:always") {
		t.Fatalf("file not healed:\n%s", gs)
	}
	if !strings.Contains(gs, "principles:session") || !strings.Contains(gs, "Authored body must survive") {
		t.Fatalf("heal lost content:\n%s", gs)
	}
}

// TestRunInitHealsEmbeddedPrincipleThenRestamps: stampless embedded principle
// whose only drifts are scope: + principles:always is healed then restamped in
// one init so validation goes green (AC5 restamp path).
func TestRunInitHealsEmbeddedPrincipleThenRestamps(t *testing.T) {
	repo := t.TempDir()
	var out1 strings.Builder
	if err := runInitTest(t, &out1, repo); err != nil {
		t.Fatalf("first init: %v\n%s", err, out1.String())
	}
	if err := os.WriteFile(filepath.Join(repo, ".satelle", "constitution.md"), []byte("# Authored\n\nok\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Pick an embedded session principle and rewrite it stampless with both drifts.
	var sample config.EmbeddedDefault
	for _, d := range config.EmbeddedDefaults() {
		if d.Kind != "principles" {
			continue
		}
		if !strings.Contains(d.Body, "principles:session") {
			continue
		}
		sample = d
		break
	}
	if sample.Name == "" {
		t.Fatal("need an embedded session principle")
	}
	// Build drifted body from the embedded default: strip stamp, inject scope:,
	// rewrite session → always.
	stripped, _, _ := stripEmbeddedStamp(sample.Body)
	// Inject scope: after name line if present.
	lines := strings.Split(stripped, "\n")
	var rebuilt []string
	for _, ln := range lines {
		rebuilt = append(rebuilt, ln)
		if strings.HasPrefix(strings.TrimSpace(ln), "name:") {
			rebuilt = append(rebuilt, "scope: system")
		}
	}
	drifted := strings.ReplaceAll(strings.Join(rebuilt, "\n"), "principles:session", "principles:always")
	path := filepath.Join(repo, ".satelle", "principles", sample.Name+".md")
	if err := os.WriteFile(path, []byte(drifted), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(filepath.Join(repo, ".satelle", "deployed.version"))

	var out2 strings.Builder
	if err := runInitTest(t, &out2, repo); err != nil {
		t.Fatalf("heal+restamp init failed: %v\n%s", err, out2.String())
	}
	s := out2.String()
	if !strings.Contains(s, "migrated:") {
		t.Fatalf("want migration report:\n%s", s)
	}
	if !strings.Contains(s, "restamped") {
		t.Fatalf("want restamp after heal:\n%s", s)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	gs := string(got)
	if strings.Contains(gs, "scope:") || strings.Contains(gs, "principles:always") {
		t.Fatalf("drifts remain:\n%s", gs)
	}
	if !strings.Contains(gs, "embedded_sha:") {
		t.Fatalf("missing stamp after heal+restamp:\n%s", gs)
	}
}

func TestMigratePrincipleFrontmatterRemovesScope(t *testing.T) {
	in := "---\nname: p\nscope: system\ntype: principle\ntags: [type:principle]\ndescription: keep me\n---\n\n# Body\n\nauthored prose\n"
	out, notes := migratePrincipleFrontmatter(in)
	if len(notes) != 1 || !strings.Contains(notes[0], "scope:") {
		t.Fatalf("notes = %v", notes)
	}
	if strings.Contains(out, "scope:") {
		t.Fatalf("scope still present:\n%s", out)
	}
	if !strings.Contains(out, "description: keep me") || !strings.Contains(out, "authored prose") {
		t.Fatalf("authored content lost:\n%s", out)
	}
}

func TestMigratePrincipleFrontmatterAlwaysToSession(t *testing.T) {
	in := "---\nname: p\ntype: principle\ntags: [type:principle, principles:always]\ndescription: d\n---\n\nbody\n"
	out, notes := migratePrincipleFrontmatter(in)
	if len(notes) != 1 || !strings.Contains(notes[0], "principles:always") {
		t.Fatalf("notes = %v", notes)
	}
	if strings.Contains(out, "principles:always") {
		t.Fatalf("always still present:\n%s", out)
	}
	if !strings.Contains(out, "principles:session") {
		t.Fatalf("session not written:\n%s", out)
	}
}

func TestMigratePrincipleFrontmatterBlockListAlways(t *testing.T) {
	in := "---\nname: p\ntype: principle\ntags:\n  - type:principle\n  - principles:always\ndescription: d\n---\n\nbody\n"
	out, notes := migratePrincipleFrontmatter(in)
	if len(notes) == 0 {
		t.Fatal("expected notes")
	}
	if strings.Contains(out, "principles:always") || !strings.Contains(out, "principles:session") {
		t.Fatalf("block list not migrated:\n%s", out)
	}
}

func TestMigratePrincipleFrontmatterAlwaysWithSessionDropsDup(t *testing.T) {
	in := "---\nname: p\ntype: principle\ntags: [type:principle, principles:session, principles:always]\ndescription: d\n---\n\nbody\n"
	out, notes := migratePrincipleFrontmatter(in)
	if len(notes) == 0 {
		t.Fatal("expected notes")
	}
	if strings.Contains(out, "principles:always") {
		t.Fatalf("always not dropped:\n%s", out)
	}
	// exactly one session
	if n := strings.Count(out, "principles:session"); n != 1 {
		t.Fatalf("session count = %d:\n%s", n, out)
	}
}

func TestMigratePrincipleFrontmatterNoop(t *testing.T) {
	in := "---\nname: p\ntype: principle\ntags: [type:principle, principles:session]\ndescription: d\n---\n\nbody\n"
	out, notes := migratePrincipleFrontmatter(in)
	if len(notes) != 0 || out != in {
		t.Fatalf("noop mutated: notes=%v out=%q", notes, out)
	}
}

func TestHealPrincipleFrontmatterInitPath(t *testing.T) {
	dataDir := t.TempDir()
	writePrinciple(t, dataDir, "scoped", "---\nname: scoped\nscope: project\ntype: principle\ntags: [type:principle, principles:always]\ndescription: d\n---\n\n# Body\n\nprose stays\n")
	// Author constitution so validate path is not the concern here.
	lines := healPrincipleFrontmatter(dataDir, BackupOpts{LocalOnly: true})
	if len(lines) != 1 || !strings.Contains(lines[0], "migrated:") {
		t.Fatalf("heal lines = %v", lines)
	}
	// Backup written
	bak := filepath.Join(dataDir, "backups", string(BackupKindPreMutation), "principles", "scoped.md")
	if _, err := os.Stat(bak); err != nil {
		t.Fatalf("backup missing: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dataDir, "principles", "scoped.md"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	if strings.Contains(s, "scope:") || strings.Contains(s, "principles:always") {
		t.Fatalf("heal incomplete:\n%s", s)
	}
	if !strings.Contains(s, "principles:session") || !strings.Contains(s, "prose stays") {
		t.Fatalf("heal lost content:\n%s", s)
	}
	// Placement clean for this file after heal
	probs := auditPlacement(dataDir, nil, "")
	for _, p := range probs {
		if strings.Contains(p, "scoped") {
			t.Fatalf("placement still flags healed principle: %v", probs)
		}
	}
}
