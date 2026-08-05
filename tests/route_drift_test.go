//go:build integration

package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRouteDriftRefusalNamesBothLanes drives the real binary through the hazard
// this guard exists for (sty_6e4f7fd8 AC1/AC2): a category table is added BETWEEN
// two transitions, which re-lanes a story already in flight, and the next
// transition must say so instead of blaming the workflow.
//
// The story walks backlog → plan under a `["*"]` lane; then a `[chore]` table
// arrives whose lane has no `plan` state at all. From `plan` there is now no
// legal edge, and the refusal must name route-drift, both lanes, and the remedy
// that is actually legal from where the story sits.
func TestRouteDriftRefusalNamesBothLanes(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	stubReviewerDispatch(t, repo)

	wfDir := filepath.Join(repo, ".satelle", "workflows")
	donePath := filepath.Join(wfDir, "done.toml")
	stepPath := filepath.Join(wfDir, "step.toml")

	// A lane with a plan step, and a step catalogue carrying every step both
	// lanes below need.
	writeFile(t, donePath, `[meta]
name = "done"
type = "workflow"
scope = "project"
description = "Fixture declaration of done for the route-drift guard."

["*"]
obligations = ["raised", "planned", "coded", "closed"]
cancel = { state = "cancelled" }
`)
	writeFile(t, stepPath, `[meta]
name = "step"
type = "workflow"
scope = "project"
description = "Fixture step catalogue for the route-drift guard."

[raised]
status = "backlog"
start = true

[planned]
status = "plan"
agent = "executor"
requires = ["raised"]

[coded]
status = "in_progress"
agent = "executor"
requires = ["planned"]

[authored]
status = "in_progress"
agent = "executor"
requires = ["raised"]

[closed]
status = "done"
terminal = true
requires = ["coded"]

[docs-verified]
status = "done"
terminal = true
requires = ["authored"]
`)
	mustRun(t, testBin, repo, "reindex")

	out := mustRun(t, testBin, repo, "story", "create",
		"--title", "A story whose lane changes under it",
		"--body", "It walks the wildcard lane, then a category table arrives and re-lanes it.",
		"--acceptance", "1. the refusal names route drift",
		"--category", "chore")
	id := extractID(out, "sty_")
	if id == "" {
		t.Fatalf("no story id:\n%s", out)
	}
	mustRun(t, testBin, repo, "story", "set", id, "--status", "plan")

	// THE HAZARD: a [chore] table arrives mid-flight. Its lane never had `plan`.
	body, err := os.ReadFile(donePath)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, donePath, string(body)+`
[chore]
obligations = ["raised", "authored", "docs-verified"]
cancel = { state = "cancelled" }
`)
	mustRun(t, testBin, repo, "reindex")

	rej, err := run(t, testBin, repo, "story", "set", id, "--status", "in_progress")
	if err == nil {
		t.Fatalf("a story sitting on a state its lane no longer declares must not advance:\n%s", rej)
	}
	if !strings.Contains(rej, "route-drift") {
		t.Errorf("refusal must name the route-drift rule, not the generic undeclared-edge text:\n%s", rej)
	}
	// Both lanes, so the reader can see what changed.
	if !strings.Contains(rej, "plan") || !strings.Contains(rej, "chore") {
		t.Errorf("refusal must name the status and the category:\n%s", rej)
	}
	// Past the entry state, category is frozen: the remedy must be re-raise, and
	// must NOT suggest --category, which the definition freeze would refuse.
	if !strings.Contains(rej, "supersedes:"+id) {
		t.Errorf("past the entry state the remedy is cancel-and-re-raise:\n%s", rej)
	}
	if strings.Contains(rej, "--category") {
		t.Errorf("remedy must not suggest --category for a story past its entry state:\n%s", rej)
	}
}

// TestOrdinaryIllegalMoveKeepsUndeclaredEdge: the new rule must not swallow the
// old one. A story sitting on a state its lane DOES declare, asked for a move
// that lane does not declare, is still an undeclared edge (sty_6e4f7fd8 AC2).
func TestOrdinaryIllegalMoveKeepsUndeclaredEdge(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	stubReviewerDispatch(t, repo)

	out := mustRun(t, testBin, repo, "story", "create",
		"--title", "A story asked to skip its lane",
		"--body", "It sits on a declared state and is asked for an edge the lane does not declare.",
		"--acceptance", "1. the refusal is the ordinary one",
		"--category", "chore")
	id := extractID(out, "sty_")
	if id == "" {
		t.Fatalf("no story id:\n%s", out)
	}
	// backlog → done skips the shipped lane's coded step: illegal, but no drift.
	rej, err := run(t, testBin, repo, "story", "set", id, "--status", "done")
	if err == nil {
		t.Fatalf("skipping a step must be refused:\n%s", rej)
	}
	if strings.Contains(rej, "route-drift") {
		t.Errorf("an ordinary illegal move is not drift:\n%s", rej)
	}
}

// TestNoDriftCostsNoDispatch: the common path must stay free (sty_6e4f7fd8 AC4).
// A story whose lane never changed walks to done with the drift check never
// dispatched — it ships UNWIRED, so nothing runs it unless a repo names it — and
// the transitions themselves are unaffected.
func TestNoDriftCostsNoDispatch(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	logPath := stubReviewerDispatch(t, repo)

	out := mustRun(t, testBin, repo, "story", "create",
		"--title", "An ordinary story on an unchanged lane",
		"--body", "Its category's lane does not change while it runs, so drift never arises.",
		"--acceptance", "1. it reaches done with no drift gate dispatched",
		"--category", "chore")
	id := extractID(out, "sty_")
	if id == "" {
		t.Fatalf("no story id:\n%s", out)
	}
	mustRun(t, testBin, repo, "story", "estimate", id, "--time", "10m", "--tokens", "1000")
	mustRun(t, testBin, repo, "story", "set", id, "--status", "in_progress")
	mustRun(t, testBin, repo, "story", "actual", id, "--time", "9m", "--tokens", "900")
	mustRun(t, testBin, repo, "story", "set", id, "--status", "done")

	if got := mustRun(t, testBin, repo, "story", "get", id); !strings.Contains(got, `"status": "done"`) {
		t.Fatalf("the ordinary path must be unaffected:\n%s", got)
	}
	gateLog, _ := os.ReadFile(logPath)
	if strings.Contains(string(gateLog), "satelle-route-drift-check") {
		t.Errorf("the drift check ships unwired and must not be dispatched on the common path; gate log:\n%s", gateLog)
	}
}
