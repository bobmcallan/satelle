//go:build integration

package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestInitReportsUneditedConstitution: fresh init WARNs on scaffold constitution
// but exits 0 (advisory, sty_c73f8905 AC3).
func TestInitReportsUneditedConstitution(t *testing.T) {
	repo := t.TempDir()
	out := mustRun(t, testBin, repo, "init")
	if !strings.Contains(out, "un-authored order-zero") &&
		!(strings.Contains(out, "WARN") && strings.Contains(out, "constitution.md")) {
		t.Fatalf("want constitution WARN on fresh init:\n%s", out)
	}
}

// TestInitFlagsInvalidPrincipleResidency: principles:global + kind:epic → non-zero
// exit naming the file (AC2).
func TestInitFlagsInvalidPrincipleResidency(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	// Author constitution so only the principle is fatal.
	_ = os.WriteFile(filepath.Join(repo, ".satelle", "constitution.md"), []byte("# Authored\n"), 0o644)
	princ := filepath.Join(repo, ".satelle", "principles", "bad-axis.md")
	// Valid structure so analysis (not OKF structure) is what fails closed.
	body := `---
name: bad-axis
type: principle
tags: [type:principle, kind:epic, principles:global]
description: Intentionally bad residency/tag axes to prove init fails closed.
---

# Bad-axis principle

This principle intentionally carries invented markers so init substrate analysis
must fail closed naming the file and the bad tags.
`
	if err := os.WriteFile(princ, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := run(t, testBin, repo, "init")
	if err == nil {
		t.Fatalf("init should fail on illegal residency/tag axis:\n%s", out)
	}
	if !strings.Contains(out, "bad-axis") {
		t.Errorf("report must name the file:\n%s", out)
	}
	if !strings.Contains(out, "principles:global") && !strings.Contains(out, "kind") {
		t.Errorf("report must name the bad tags:\n%s", out)
	}
}

// TestInitReconcilesConfigBlock: toml missing edit_exempt_paths → WARN with exact
// block, exit 0 (AC6 advisory).
func TestInitReconcilesConfigBlock(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	// Strip edit_exempt_paths from satelle.toml while keeping [gate].
	tomlPath := filepath.Join(repo, ".satelle", "satelle.toml")
	raw, err := os.ReadFile(tomlPath)
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, ln := range strings.Split(string(raw), "\n") {
		if strings.Contains(ln, "edit_exempt_paths") {
			continue
		}
		lines = append(lines, ln)
	}
	if err := os.WriteFile(tomlPath, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}
	// Author constitution so only config WARN fires (no fatals).
	_ = os.WriteFile(filepath.Join(repo, ".satelle", "constitution.md"), []byte("# Authored\n"), 0o644)
	out, err := run(t, testBin, repo, "init")
	if err != nil {
		t.Fatalf("init should stay green on advisory config:\n%s\nerr=%v", out, err)
	}
	if !strings.Contains(out, "edit_exempt_paths") {
		t.Fatalf("want edit_exempt_paths WARN:\n%s", out)
	}
	if !strings.Contains(out, `[".satelle/", ".gitignore"]`) && !strings.Contains(out, `[".satelle/",".gitignore"]`) {
		t.Fatalf("want exact block with .gitignore in fix:\n%s", out)
	}
}

// TestInitLandsNewlyEmbeddedCanon: deleted embedded principle is re-seeded;
// authored principle body is never clobbered (AC1).
func TestInitLandsNewlyEmbeddedCanon(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	princDir := filepath.Join(repo, ".satelle", "principles")
	entries, _ := os.ReadDir(princDir)
	var deleted string
	for _, e := range entries {
		if e.IsDir() || e.Name() == "README.md" {
			continue
		}
		deleted = filepath.Join(princDir, e.Name())
		break
	}
	if deleted != "" {
		if err := os.Remove(deleted); err != nil {
			t.Fatal(err)
		}
	}
	custom := filepath.Join(princDir, "my-authored.md")
	// Valid OKF principle structure so re-init's structure check stays green.
	customBody := `---
name: my-authored
type: principle
tags: [type:principle]
description: Authored principle used to prove init never clobbers operator content.
---

# Custom authored principle

State the guidance: never clobber operator-authored principle bodies on re-init.
This body is the proof the heal path respects authored substrate.
`
	if err := os.WriteFile(custom, []byte(customBody), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(repo, ".satelle", "constitution.md"), []byte("# Authored\n"), 0o644)

	out := mustRun(t, testBin, repo, "init")
	if deleted != "" {
		if _, err := os.Stat(deleted); err != nil {
			t.Errorf("deleted canon principle not re-seeded: %v\nout=%s", err, out)
		}
	}
	got, err := os.ReadFile(custom)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != customBody {
		t.Errorf("authored principle clobbered:\n got %q\nwant %q", got, customBody)
	}
}
