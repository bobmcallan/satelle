//go:build integration

package tests

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubAmendVerdict writes a deterministic reviewer harness into repo and returns
// a setter that flips its verdict. Hermetic by construction: no model is called.
func stubAmendVerdict(t *testing.T, repo string) func(decision, notes string) {
	t.Helper()
	verdict := filepath.Join(repo, "verdict.sh")
	set := func(decision, notes string) {
		writeFile(t, verdict, fmt.Sprintf("#!/bin/sh\necho '{\"decision\":\"%s\",\"notes\":\"%s\"}'\n", decision, notes))
		_ = os.Chmod(verdict, 0o755)
	}
	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "agents.toml"),
		fmt.Sprintf("[reviewer]\ncommand = \"%s {system} {tools} {model}\"\n", verdict))
	set("accept", "ok")
	return set
}

// engageForAmend creates a story and moves it out of the entry state, so the
// definition freeze is live and `story set` can no longer touch the ACs.
func engageForAmend(t *testing.T, repo, title string) string {
	t.Helper()
	out := mustRun(t, testBin, repo, "story", "create", "--category", "feature",
		"--title", title, "--body", "Render a widget on the dashboard",
		"--acceptance", "1. the widget renders\n2. the widget is blue")
	id := storyIDFrom(t, out)
	// The shipped route fences entry to in_progress on a recorded estimate.
	mustRun(t, testBin, repo, "story", "estimate", id, "--time", "10", "--tokens", "1000")
	mustRun(t, testBin, repo, "story", "set", id, "--status", "in_progress")
	return id
}

// storyIDFrom pulls the sty_ id out of a JSON story payload without decoding it.
func storyIDFrom(t *testing.T, out string) string {
	t.Helper()
	i := strings.Index(out, "sty_")
	if i < 0 {
		t.Fatalf("no story id in output:\n%s", out)
	}
	id := out[i:]
	if j := strings.IndexAny(id, "\"\n ,"); j > 0 {
		id = id[:j]
	}
	return id
}

// TestAmendGateOnAFreshlyInitialisedRepo (sty_5c768dd3 AC2/AC3) is the whole
// point of promoting the rubric: a repo that has authored NO substrate at all
// inherits the amend gate from the shipped default and can correct a wrong
// definition mid-flight. It drives the real binary end to end — reject leaves
// the story untouched, accept updates it and records the before/after.
func TestAmendGateOnAFreshlyInitialisedRepo(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	setVerdict := stubAmendVerdict(t, repo)
	mustRun(t, testBin, repo, "reindex")

	// AC2: the hook resolves out of the box, declared once for the workflow.
	show := mustRun(t, testBin, repo, "workflow", "show", "done")
	if !strings.Contains(show, "amend_review") || !strings.Contains(show, "satelle-story-amend-review") {
		t.Fatalf("a fresh init must resolve the amend hook:\n%s", show)
	}
	if strings.Contains(show, "UNRESOLVED") {
		t.Errorf("the shipped rubric must resolve, not dangle:\n%s", show)
	}

	id := engageForAmend(t, repo, "Add a widget")

	// The freeze is live: story set cannot touch the definition, and says so.
	frozen, err := run(t, testBin, repo, "story", "set", id, "--acceptance", "1. anything")
	if err == nil {
		t.Fatalf("an engaged story's ACs must stay frozen to story set:\n%s", frozen)
	}
	if !strings.Contains(frozen, "story amend") {
		t.Errorf("the freeze refusal should name the amend path:\n%s", frozen)
	}

	// Reject: the gate refuses, and NOTHING changes.
	setVerdict("reject", "stub: this weakens AC2 rather than correcting it")
	rej, err := run(t, testBin, repo, "story", "amend", id,
		"--acceptance", "1. the widget renders", "--reason", "drop the colour requirement")
	if err == nil {
		t.Fatalf("a rejected amendment must not land:\n%s", rej)
	}
	if !strings.Contains(rej, "weakens AC2") {
		t.Errorf("reject notes not surfaced to the agent:\n%s", rej)
	}
	if got := mustRun(t, testBin, repo, "story", "get", id); !strings.Contains(got, "the widget is blue") {
		t.Errorf("a rejected amendment changed the story:\n%s", got)
	}

	// Accept: the correction lands, and the ledger carries old AND new.
	setVerdict("accept", "stub: AC2 was factually wrong")
	if out, err := run(t, testBin, repo, "story", "amend", id,
		"--acceptance", "1. the widget renders\n2. the widget is green",
		"--reason", "AC2 named the wrong colour"); err != nil {
		t.Fatalf("an accepted amendment should land: %v\n%s", err, out)
	}
	got := mustRun(t, testBin, repo, "story", "get", id)
	if !strings.Contains(got, "the widget is green") || strings.Contains(got, "the widget is blue") {
		t.Fatalf("the amended definition is not what the story now carries:\n%s", got)
	}
	led := mustRun(t, testBin, repo, "ledger", "list", "--story", id)
	if !strings.Contains(led, "definition_amended") {
		t.Fatalf("the amendment must be on the ledger:\n%s", led)
	}
	for _, want := range []string{"the widget is blue", "the widget is green", "AC2 named the wrong colour"} {
		if !strings.Contains(led, want) {
			t.Errorf("ledger must record the before/after and the reason, missing %q:\n%s", want, led)
		}
	}
}

// TestAmendStaysFailClosedWhenTheHookIsMissingOrBroken (sty_5c768dd3 AC4):
// promoting the default must not turn absence of a judge into permission. A repo
// that deletes the hook from its own declaration of done, or names a rubric that
// does not resolve, still gets no amend — which is also the escape hatch for a
// repo that wants its definitions permanently frozen.
func TestAmendStaysFailClosedWhenTheHookIsMissingOrBroken(t *testing.T) {
	for _, c := range []struct {
		name    string
		rewrite func(done string) string
	}{
		{
			name: "hook removed",
			rewrite: func(done string) string {
				return strings.ReplaceAll(done, "amend_review = \"satelle-story-amend-review\"\n", "")
			},
		},
		{
			name: "hook names a rubric that does not resolve",
			rewrite: func(done string) string {
				return strings.ReplaceAll(done, "\"satelle-story-amend-review\"", "\"no-such-amend-review\"")
			},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			repo := t.TempDir()
			mustRun(t, testBin, repo, "init")
			setVerdict := stubAmendVerdict(t, repo)
			setVerdict("accept", "the gate would have accepted, had one run")

			// Author the shipped route, then take the hook away.
			materializeDefault(t, repo, "workflows", "done")
			materializeDefault(t, repo, "workflows", "step")
			donePath := filepath.Join(repo, ".satelle", "workflows", "done.toml")
			body, rerr := os.ReadFile(donePath)
			if rerr != nil {
				t.Fatal(rerr)
			}
			rewritten := c.rewrite(string(body))
			if rewritten == string(body) {
				t.Fatalf("fixture did not alter the hook — the default's spelling changed?")
			}
			writeFile(t, donePath, rewritten)
			mustRun(t, testBin, repo, "reindex")

			out := mustRun(t, testBin, repo, "story", "create", "--category", "feature",
				"--title", "Frozen for good", "--body", "Work whose definition must not move",
				"--acceptance", "1. the definition holds")
			id := storyIDFrom(t, out)

			amended, err := run(t, testBin, repo, "story", "amend", id,
				"--acceptance", "1. something easier", "--reason", "make the gate pass")
			if err == nil {
				t.Fatalf("amend must refuse when nothing judges it:\n%s", amended)
			}
			if !strings.Contains(amended, "amend_review") {
				t.Errorf("the refusal should name the hook to declare:\n%s", amended)
			}
			if got := mustRun(t, testBin, repo, "story", "get", id); !strings.Contains(got, "the definition holds") {
				t.Fatalf("a refused amendment changed the story:\n%s", got)
			}
		})
	}
}
