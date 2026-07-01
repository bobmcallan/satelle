//go:build integration

package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestInitScaffoldsConstitutionAndInjects proves 'satelle init' scaffolds
// .satelle/constitution.md idempotently and the SessionStart injector rides it as
// order-zero context, frontmatter stripped (epic:session-context).
func TestInitScaffoldsConstitutionAndInjects(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	path := filepath.Join(repo, ".satelle", "constitution.md")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("init did not scaffold the constitution: %v", err)
	}

	// Idempotent: author it, re-init, content preserved + reported as present.
	custom := "---\ntype: constitution\n---\n\n# My repo constitution\n\nThis repo's order-zero rule."
	if err := os.WriteFile(path, []byte(custom), 0o644); err != nil {
		t.Fatal(err)
	}
	out := mustRun(t, testBin, repo, "init")
	if !strings.Contains(out, "constitution.md") {
		t.Errorf("re-init should report the constitution:\n%s", out)
	}
	if b, _ := os.ReadFile(path); !strings.Contains(string(b), "This repo's order-zero rule.") {
		t.Errorf("re-init clobbered the authored constitution")
	}

	// Injected at session start as order-zero context, frontmatter stripped.
	mustRun(t, testBin, repo, "index", "--validate=false")
	ctx := mustRun(t, testBin, repo, "hook", "context")
	if !strings.Contains(ctx, "This repo's order-zero rule.") {
		t.Errorf("constitution not injected into session context:\n%s", ctx)
	}
	if !strings.Contains(ctx, "# Project constitution") {
		t.Errorf("constitution order-zero header missing:\n%s", ctx)
	}
}
