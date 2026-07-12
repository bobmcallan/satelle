//go:build integration

package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPrincipleValidatePlacementWiring drives the real binary to prove the
// placement guard is anchored under `satelle principle validate` (whole-set):
// a scope: remnant on a principle yields FAIL principles (placement) and a
// non-zero exit; a clean init seed passes placement.
func TestPrincipleValidatePlacementWiring(t *testing.T) {
	// Clean seed: init materialises stamped embedded defaults — placement must pass.
	clean := t.TempDir()
	mustRun(t, testBin, clean, "init")
	cleanOut := mustRun(t, testBin, clean, "principle", "validate")
	if strings.Contains(cleanOut, "FAIL  principles (placement)") {
		t.Fatalf("clean init seed must pass placement:\n%s", cleanOut)
	}

	// Violating principle: illegal principles:global tag (structure-valid;
	// placement-only fail) → FAIL principles (placement).
	badRepo := t.TempDir()
	mustRun(t, testBin, badRepo, "init")
	bad := "---\nname: global-leak\ntype: principle\ntags: [type:principle, principles:global]\ndescription: d\napplies_to: [\"*\"]\n---\n\n# Global leak\n\nDead residency-ish tag must fail placement.\n"
	if err := os.WriteFile(filepath.Join(badRepo, ".satelle", "principles", "global-leak.md"), []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := run(t, testBin, badRepo, "principle", "validate")
	if err == nil {
		t.Fatalf("expected principle validate to fail with a placement violation:\n%s", out)
	}
	if !strings.Contains(out, "FAIL  principles (placement)") || !strings.Contains(out, "principles:global") {
		t.Errorf("want whole-set placement FAIL naming principles:global:\n%s", out)
	}
}
