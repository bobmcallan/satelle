//go:build integration

package tests

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

type wfListRow struct {
	Name     string `json:"name"`
	Active   bool   `json:"active"`
	Embedded bool   `json:"embedded"`
}

func wfList(t *testing.T, repo, category string) []wfListRow {
	t.Helper()
	out := mustRun(t, testBin, repo, "workflow", "list", "--category", category)
	var rows []wfListRow
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("parse workflow list: %v\n%s", err, out)
	}
	return rows
}

// TestEmbeddedBaselinePrecedence proves the baseline-scaffold fix (sty_3f9a6124)
// under the seeded default solution (sty_a7cbd6dd): a fresh repo has NO baseline
// repo file — the baseline stays embedded-only (listed as a fallback candidate,
// embedded=true) — while the SEEDED project workflow (a real, editable repo file)
// is the active default, and a repo's OWN wildcard workflow takes precedence.
func TestEmbeddedBaselinePrecedence(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	mustRun(t, testBin, repo, "reindex")

	// (a) The seeded project workflow (a repo file, not embedded) is the active
	// default of a fresh repo; the embedded baseline is still listed as the
	// order-zero fallback, never as a repo file.
	var sawBaseline bool
	for _, r := range wfList(t, repo, "feature") {
		switch r.Name {
		case "satelle-baseline-workflow":
			sawBaseline = true
			if !r.Embedded {
				t.Errorf("the baseline must be the EMBEDDED one (no repo file), got embedded=false")
			}
			if r.Active {
				t.Errorf("the baseline must not be active — the seeded project workflow governs a fresh repo")
			}
		case "satelle-project-workflow":
			if r.Embedded {
				t.Errorf("the seeded project workflow must be a real repo file, got embedded=true")
			}
			if !r.Active {
				t.Errorf("the seeded project workflow must be active for a fresh repo")
			}
		}
	}
	if !sawBaseline {
		t.Fatal("baseline workflow not listed for a fresh repo")
	}

	// (b) A repo's own wildcard workflow takes precedence over the embedded baseline.
	wf, err := os.ReadFile(filepath.Join(repoRootForTest(), ".satelle", "workflows", "satelle-project-workflow.md"))
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "satelle-project-workflow.md"), string(wf))
	mustRun(t, testBin, repo, "reindex")
	rows := wfList(t, repo, "feature")
	if len(rows) == 0 || rows[0].Name != "satelle-project-workflow" || !rows[0].Active {
		t.Errorf("a repo wildcard workflow must beat the embedded baseline, got %+v", rows)
	}
}
