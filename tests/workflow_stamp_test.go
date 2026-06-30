//go:build integration

package tests

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWorkflowStampedAtCreate proves the stamp slice (sty_3800ac23): creating a
// story under a governing workflow records that workflow on the story — a
// workflow:<name> tag AND a workflow_stamped ledger event.
func TestWorkflowStampedAtCreate(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	wf, err := os.ReadFile(filepath.Join(repoRootForTest(), ".satelle", "workflows", "satelle-project-workflow.md"))
	if err != nil {
		t.Fatal(err)
	}
	wfBody := strings.Replace(string(wf), `applies_to: ["*"]`, `applies_to: ["feature"]`, 1)
	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "satelle-project-workflow.md"), wfBody)
	mustRun(t, testBin, repo, "index")

	out := mustRun(t, testBin, repo, "story", "create", "--category", "feature",
		"--title", "Stamp me", "--body", "Record the governing workflow on creation", "--acceptance", "1. it is stamped")
	var story struct {
		ID   string   `json:"id"`
		Tags []string `json:"tags"`
	}
	if err := json.Unmarshal([]byte(out), &story); err != nil {
		t.Fatalf("parse created story: %v\n%s", err, out)
	}

	// (a) the workflow:<name> tag is stamped on the story.
	var stamped bool
	for _, tg := range story.Tags {
		if tg == "workflow:satelle-project-workflow" {
			stamped = true
		}
	}
	if !stamped {
		t.Errorf("story not stamped with the governing workflow tag; tags=%v", story.Tags)
	}

	// (b) a workflow_stamped ledger event records the choice.
	led := mustRun(t, testBin, repo, "ledger", "list", "--story", story.ID)
	if !strings.Contains(led, "workflow_stamped") || !strings.Contains(led, "satelle-project-workflow") {
		t.Errorf("no workflow_stamped ledger event for the choice:\n%s", led)
	}
}

const wfXFeature = "---\nname: wf-x\ntype: workflow\napplies_to: [\"feature\"]\n---\n# X declares the engage edge\n" +
	"```dot\n" + `digraph x {
  backlog [shape=Mdiamond]
  in_progress [agent=executor]
  done [shape=Msquare, agent=reviewer]
  cancelled [agent=reviewer]
  backlog -> in_progress
  in_progress -> done
  backlog -> cancelled
}` + "\n```\n"

const wfYChore = "---\nname: wf-y\ntype: workflow\napplies_to: [\"chore\"]\n---\n# Y has NO in_progress (engage edge undeclared)\n" +
	"```dot\n" + `digraph y {
  backlog [shape=Mdiamond]
  done [shape=Msquare, agent=reviewer]
  cancelled [agent=reviewer]
  backlog -> done
  backlog -> cancelled
}` + "\n```\n"

// TestStampedWorkflowGovernsGating proves AC2 end-to-end: a story's STAMPED
// workflow governs its gating, overriding category resolution. wf-x (feature)
// declares backlog->in_progress; wf-y (chore) does NOT. Two stories both end up
// category=chore — they differ ONLY by their stamp. The one stamped wf-x can
// engage (its workflow declares the edge); the one stamped wf-y cannot.
func TestStampedWorkflowGovernsGating(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "wf-x.md"), wfXFeature)
	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "wf-y.md"), wfYChore)
	mustRun(t, testBin, repo, "index")

	create := func(category string) string {
		out := mustRun(t, testBin, repo, "story", "create", "--category", category,
			"--title", "Engage me "+category, "--body", "Drive this story through its workflow", "--acceptance", "1. it engages")
		var s struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal([]byte(out), &s); err != nil {
			t.Fatalf("parse story: %v\n%s", err, out)
		}
		return s.ID
	}

	// Stamped wf-x (created under feature), then moved to category chore.
	idX := create("feature")
	mustRun(t, testBin, repo, "story", "set", idX, "--category", "chore")
	mustRun(t, testBin, repo, "story", "estimate", idX, "--tokens", "1000", "--time", "10m")
	if out, err := run(t, testBin, repo, "story", "set", idX, "--status", "in_progress"); err != nil {
		t.Fatalf("a story stamped wf-x must engage (its workflow declares backlog->in_progress): %v\n%s", err, out)
	}

	// Stamped wf-y (created under chore): wf-y does not declare the engage edge.
	idY := create("chore")
	mustRun(t, testBin, repo, "story", "estimate", idY, "--tokens", "1000", "--time", "10m")
	if out, err := run(t, testBin, repo, "story", "set", idY, "--status", "in_progress"); err == nil {
		t.Fatalf("a story stamped wf-y must NOT engage (undeclared edge); category alone would differ — stamp must govern\n%s", out)
	}
}
