//go:build integration

package tests

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// AC4 of sty_3795e7f6: a repo initialised with NO authored substrate resolves
// its lifecycle from the route the binary ships, drives a story to done through
// it, and validates green — proven by driving the real binary, not by reading
// the markdown.

// TestFreshRepoDrivesAStoryOnTheShippedRoute is the whole claim end to end.
func TestFreshRepoDrivesAStoryOnTheShippedRoute(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	// A fresh repo authors nothing: the route governs through the doc index's
	// read-time overlay, with no file on disk (virtual sparse defaults).
	entries, err := os.ReadDir(filepath.Join(repo, ".satelle", "workflows"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".md") && e.Name() != "README.md" {
			t.Fatalf("init seeded %s — defaults are virtual, and an on-disk copy would make "+
				"this test prove nothing about the shipped route", e.Name())
		}
	}

	logPath := stubReviewerDispatch(t, repo)
	// The suite turns the create gate off after init (most tests create partial
	// drafts); this one needs it ON, because `create_review:` on done.toml is how
	// the shipped route carries a create-time gate at all.
	writeFile(t, filepath.Join(repo, ".satelle", "satelle.local.toml"), "[review]\ngate_create = true\n")
	mustRun(t, testBin, repo, "reindex")

	// Every validator is green against the virtual route.
	mustRun(t, testBin, repo, "workflow", "validate")
	mustRun(t, testBin, repo, "skill", "validate")
	mustRun(t, testBin, repo, "validate")

	// Create → the create gate runs (done.toml carries create_review:) and the
	// story is stamped with what will actually gate it.
	out := mustRun(t, testBin, repo, "story", "create",
		"--title", "Drive the shipped route",
		"--body", "A fresh repo must be able to take a story to done with no authored substrate.",
		"--acceptance", "1. backlog → in_progress → done on the shipped route",
		"--category", "chore")
	id := extractID(out, "sty_")
	if id == "" {
		t.Fatalf("no story id:\n%s", out)
	}
	if !strings.Contains(out, `"workflow:default"`) {
		t.Errorf("create did not stamp the shipped route:\n%s", out)
	}
	if body, _ := os.ReadFile(logPath); !strings.Contains(string(body), "satelle-story-create-review") {
		t.Errorf("the create gate declared on done.toml did not run; gate log:\n%s", body)
	}

	// backlog → in_progress: the estimate gate is a coded check, so it rejects
	// before the estimate is recorded. That is the route's gate firing, not a
	// stub, which is what makes the accept below meaningful.
	if rej, err := run(t, testBin, repo, "story", "set", id, "--status", "in_progress"); err == nil {
		t.Fatalf("engage without an estimate should reject:\n%s", rej)
	}
	mustRun(t, testBin, repo, "story", "estimate", id, "--time", "10m", "--tokens", "1000")
	mustRun(t, testBin, repo, "story", "set", id, "--status", "in_progress")

	// in_progress → done, through the route's three-reviewer close.
	mustRun(t, testBin, repo, "story", "actual", id, "--time", "9m", "--tokens", "900")
	mustRun(t, testBin, repo, "story", "set", id, "--status", "done")
	if got := mustRun(t, testBin, repo, "story", "get", id); !strings.Contains(got, `"status": "done"`) {
		t.Fatalf("story did not reach done:\n%s", got)
	}

	gateLog, _ := os.ReadFile(logPath)
	for _, want := range []string{
		"satelle-story-intent-review",
		"satelle-story-done-review",
		"satelle-story-scope-review",
		"satelle-workflow-change-review",
	} {
		if !strings.Contains(string(gateLog), want) {
			t.Errorf("the shipped route did not run gate %s; gate log:\n%s", want, gateLog)
		}
	}

	// The route the story actually carries is the shipped one, reported honestly.
	route := mustRun(t, testBin, repo, "story", "route", id)
	if !strings.Contains(route, "default") {
		t.Errorf("story route does not name the shipped route:\n%s", route)
	}
}

// TestFreshRepoExecutionUsesTheTaskSection is AC3's "never falls through": a run
// resolves to the route's own task section — gated by the two task-validate
// reviewers — rather than to the wildcard lane.
func TestFreshRepoExecutionUsesTheTaskSection(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	stubReviewerAccept(t, repo)

	// An execution needs a task header to run under; init seeds no example one.
	authored := "---\nid: tsk_run\ntype: task\nstatus: backlog\n---\n\n# Runnable\n\n" +
		"ACTION: do the thing. VERIFICATION: it is done.\n"
	writeFile(t, filepath.Join(repo, ".satelle", "tasks", "tsk_run.md"), authored)
	mustRun(t, testBin, repo, "reindex")

	type wfRow struct {
		Name   string `json:"name"`
		Active bool   `json:"active"`
	}
	out := mustRun(t, testBin, repo, "workflow", "list", "--category", "execution")
	var rows []wfRow
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("parse workflow list: %v\n%s", err, out)
	}
	if len(rows) == 0 || rows[0].Name != "default" || !rows[0].Active {
		t.Fatalf("an execution must resolve to the shipped route, got %+v", rows)
	}

	out = mustRun(t, testBin, repo, "execution", "create", "--parent", "tsk_run", "--title", "run 1")
	eid := extractID(out, "exe_")
	if eid == "" {
		t.Fatalf("no execution id:\n%s", out)
	}

	// The run's route is the task section: the two validate gates bracket it, and
	// it owes no `coded` obligation — that would be the wildcard lane.
	route := mustRun(t, testBin, repo, "story", "route", eid)
	for _, want := range []string{"satelle-task-validate-before-review", "satelle-task-validate-after-review"} {
		if !strings.Contains(route, want) {
			t.Errorf("the run's route does not name %s:\n%s", want, route)
		}
	}
	if strings.Contains(route, "coded") {
		t.Errorf("the run fell through to the wildcard lane (its route owes `coded`):\n%s", route)
	}
}

// TestShippedRouteSurvivesReindex: the doc index normalises workflows at ingest
// (wfdot.ToDOT). The route halves carry no inline-YAML lifecycle, so they must
// pass through byte-for-byte — asserted rather than assumed, because a rewrite
// would silently corrupt the shipped grammar on the first reindex.
func TestShippedRouteSurvivesReindex(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	materializeDefault(t, repo, "workflows", "done")
	materializeDefault(t, repo, "workflows", "step")

	before := map[string][]byte{}
	for _, half := range []string{"done.toml", "step.toml"} {
		b, err := os.ReadFile(filepath.Join(repo, ".satelle", "workflows", half))
		if err != nil {
			t.Fatal(err)
		}
		before[half] = b
	}
	mustRun(t, testBin, repo, "reindex")
	mustRun(t, testBin, repo, "reindex")
	for half, want := range before {
		got, err := os.ReadFile(filepath.Join(repo, ".satelle", "workflows", half))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(want) {
			t.Errorf("reindex rewrote %s:\n--- before ---\n%s\n--- after ---\n%s", half, want, got)
		}
	}
}
