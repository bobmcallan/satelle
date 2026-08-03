//go:build integration

package tests

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestWorkflowStampedAtCreate proves the stamp slice (sty_3800ac23): creating a
// story under a governing workflow records that workflow on the story — a
// workflow:<name> tag AND a workflow_stamped ledger event.
func TestWorkflowStampedAtCreate(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	seedRouteSource(t, repo)
	mustRun(t, testBin, repo, "reindex")

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
		if tg == "workflow:default" {
			stamped = true
		}
	}
	if !stamped {
		t.Errorf("story not stamped with the governing workflow tag; tags=%v", story.Tags)
	}

	// (b) a workflow_stamped ledger event records the choice.
	led := mustRun(t, testBin, repo, "ledger", "list", "--story", story.ID)
	if !strings.Contains(led, "workflow_stamped") || !strings.Contains(led, "default") {
		t.Errorf("no workflow_stamped ledger event for the choice:\n%s", led)
	}
}

// TestStampedWorkflowGovernsGating proves the AC2 property that survives the
// cutover: the LANE a story resolves to governs its gating, so a lifecycle that
// never declares an engage edge cannot be engaged (sty_3800ac23).
//
// The original test discriminated by STAMP — two workflow files, two stories both
// re-categorised to chore, differing only by the workflow:<name> they carried.
// A derived route has one name (`default`) for every category, so the
// stamp can no longer name a second lifecycle to override category resolution
// with; that leg retires with the DOT front end (sty_d953c5d8), and the stamp
// keeps its remaining job — recording what governs — which
// TestWorkflowStampedAtCreate above pins. What is still discriminating, and is
// what this test now drives, is the SECTION: `feature` declares
// backlog → in_progress → done, `chore` closes straight out of backlog.
func TestStampedWorkflowGovernsGating(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	writeRouteFixture(t, repo,
		`[feature]
obligations = ["raised", "coded", "closed"]
cancel = { state = "cancelled", gate = "satelle-story-cancel-review" }

[chore]
obligations = ["raised", "chore-closed"]
cancel = { state = "cancelled", gate = "satelle-story-cancel-review" }
`,
		`[raised]
status = "backlog"
start = true

[coded]
status = "in_progress"
agent = "executor"
requires = ["raised"]

[closed]
status = "done"
terminal = true
requires = ["coded"]

[chore-closed]
status = "done"
terminal = true
requires = ["raised"]
`)
	mustRun(t, testBin, repo, "reindex")

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

	// The feature lane declares the engage edge.
	idX := create("feature")
	mustRun(t, testBin, repo, "story", "estimate", idX, "--tokens", "1000", "--time", "10m")
	if out, err := run(t, testBin, repo, "story", "set", idX, "--status", "in_progress"); err != nil {
		t.Fatalf("a feature story must engage (its lane declares backlog->in_progress): %v\n%s", err, out)
	}
	// Free the single-story seat so the next engage is judged by the lane, not the
	// one-performing-story process rule (sty_c7149f8a).
	mustRun(t, testBin, repo, "story", "set", idX, "--status", "done")

	// The chore lane closes straight out of backlog — there is no engage edge.
	idY := create("chore")
	mustRun(t, testBin, repo, "story", "estimate", idY, "--tokens", "1000", "--time", "10m")
	if out, err := run(t, testBin, repo, "story", "set", idY, "--status", "in_progress"); err == nil {
		t.Fatalf("a chore story must NOT engage — its lane declares no in_progress step\n%s", out)
	}
}
