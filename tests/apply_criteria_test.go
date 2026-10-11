//go:build integration

package tests

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// TestApplyCriteriaOnEntryThroughTheBinary (sty_4d9df9a0) drives the real binary
// over a route whose coded step declares apply_criteria: entering the step copies
// the plan's `## Acceptance criteria` section onto the story with no manual
// step, records the edit as applied from the plan, and a story whose plan
// authored no criteria keeps its own.
func TestApplyCriteriaOnEntryThroughTheBinary(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "done.toml"),
		"[meta]\nname = \"done\"\ntype = \"workflow\"\nscope = \"project\"\ndescription = \"apply criteria fixture\"\n\n"+
			"[\"*\"]\nobligations = [\"raised\", \"coded\", \"closed\"]\n")
	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "step.toml"),
		"[meta]\nname = \"step\"\ntype = \"workflow\"\nscope = \"project\"\ndescription = \"apply criteria fixture\"\n\n"+
			"[raised]\nstatus = \"backlog\"\nstart = true\n\n"+
			"[coded]\nstatus = \"in_progress\"\nagent = \"executor\"\nfreeze = true\nrequires = [\"raised\"]\n"+
			"apply_criteria = { doc = \"plan\", heading = \"Acceptance criteria\" }\n\n"+
			"[closed]\nstatus = \"done\"\nterminal = true\nrequires = [\"coded\"]\n")
	mustRun(t, testBin, repo, "reindex")

	create := func(title string) string {
		out := mustRun(t, testBin, repo, "story", "create", "--category", "feature", "--title", title,
			"--body", "Apply the plan's criteria", "--acceptance", "1. a draft criterion")
		return storyIDFrom(t, out)
	}
	criteriaOf := func(id string) string {
		var it struct {
			AcceptanceCriteria string `json:"acceptance_criteria"`
		}
		out := mustRun(t, testBin, repo, "story", "get", id, "--json")
		if err := json.Unmarshal([]byte(out[strings.Index(out, "{"):]), &it); err != nil {
			t.Fatalf("decode story: %v\n%s", err, out)
		}
		return it.AcceptanceCriteria
	}

	const planned = "1. the widget renders\n2. the widget is blue"
	withCriteria := create("Plan authors criteria")
	mustRun(t, testBin, repo, "story", "attach", withCriteria, "--name", "plan", "--type", "plan",
		"--body", "# Plan\n\n## Acceptance criteria\n\n"+planned+"\n\n## Risks\n\nNone.\n")
	mustRun(t, testBin, repo, "story", "estimate", withCriteria, "--time", "10", "--tokens", "1000")
	mustRun(t, testBin, repo, "story", "set", withCriteria, "--status", "in_progress")
	if got := criteriaOf(withCriteria); got != planned {
		t.Fatalf("criteria = %q, want the plan's section verbatim %q", got, planned)
	}
	// One performing story holds the seat; free it so the second story can engage.
	mustRun(t, testBin, repo, "story", "seat", "release", withCriteria)
	edits := mustRun(t, testBin, repo, "story", "definition-edits", withCriteria, "--json")
	for _, want := range []string{`"field": "acceptance_criteria"`, `"actor": "accepted-plan"`, `"source": "plan#Acceptance criteria"`} {
		if !strings.Contains(strings.ReplaceAll(edits, `":"`, `": "`), want) {
			t.Errorf("definition-edits missing %s:\n%s", want, edits)
		}
	}

	without := create("Plan authors none")
	mustRun(t, testBin, repo, "story", "attach", without, "--name", "plan", "--type", "plan",
		"--body", "# Plan\n\n## Premise\n\nIt holds.\n")
	mustRun(t, testBin, repo, "story", "estimate", without, "--time", "10", "--tokens", "1000")
	mustRun(t, testBin, repo, "story", "set", without, "--status", "in_progress")
	if got := criteriaOf(without); got != "1. a draft criterion" {
		t.Fatalf("criteria = %q, want the story's own unchanged", got)
	}
}
