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

// TestEmbeddedBaselinePrecedence proves the baseline-scaffold fix (sty_3f9a6124):
// a fresh repo has NO baseline repo file, the EMBEDDED baseline is the active
// default (embedded=true), and a repo's OWN wildcard workflow then takes
// precedence over it.
func TestEmbeddedBaselinePrecedence(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	mustRun(t, testBin, repo, "reindex")

	// (a) Embedded baseline is the active default of an unconfigured repo.
	var sawBaseline bool
	for _, r := range wfList(t, repo, "feature") {
		if r.Name == "satelle-baseline-workflow" {
			sawBaseline = true
			if !r.Embedded {
				t.Errorf("the default baseline must be the EMBEDDED one (no repo file), got embedded=false")
			}
			if !r.Active {
				t.Errorf("the embedded baseline must be active for an unconfigured repo")
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
