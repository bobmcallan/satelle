//go:build integration

package tests

import (
	"path/filepath"
	"strings"
	"testing"
)

func wildcardWF(name string) string {
	return "---\nname: " + name + "\ntype: workflow\napplies_to: [\"*\"]\ndescription: a test wildcard workflow\n---\n" +
		"```dot\n" + `digraph w {
  backlog [shape=Mdiamond]
  in_progress [agent=executor]
  done [shape=Msquare, agent=reviewer]
  cancelled [agent=reviewer]
  backlog -> in_progress
  in_progress -> done
  backlog -> cancelled
}` + "\n```\n"
}

// TestValidateFlagsWorkflowAmbiguity proves the inconsistency-advice slice
// (sty_4c0c7246): two REPO wildcard workflows are flagged by `satelle workflow validate`
// with an actionable message, and `workflow list` surfaces both candidates for
// the agent to choose between.
func TestValidateFlagsWorkflowAmbiguity(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "wf-a.md"), wildcardWF("wf-a"))
	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "wf-b.md"), wildcardWF("wf-b"))
	mustRun(t, testBin, repo, "reindex")

	out, err := run(t, testBin, repo, "workflow", "validate")
	if err == nil {
		t.Fatalf("validate should fail on two repo workflows tying for the wildcard:\n%s", out)
	}
	if !strings.Contains(out, "same precedence") || !strings.Contains(out, "wf-a") || !strings.Contains(out, "wf-b") {
		t.Errorf("validate should name the ambiguous workflows actionably:\n%s", out)
	}

	// Candidate surfacing: workflow list shows both for the agent to choose.
	list := mustRun(t, testBin, repo, "workflow", "list", "--category", "feature")
	if !strings.Contains(list, "wf-a") || !strings.Contains(list, "wf-b") {
		t.Errorf("workflow list should surface both candidates:\n%s", list)
	}
}
