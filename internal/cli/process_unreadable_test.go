package cli

// sty_d6e209aa: a missing, unreadable or divergent authored process is reported,
// never silently replaced by the embedded default. AC1 (unreadable refuses on
// every surface), AC3 (absent is reported, never refused).

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
)

// isolateProcessEnv keeps a process-of-record test off the real ~/.satelle and
// off any SATELLE_CONFIG override, with no hosted endpoint.
func isolateProcessEnv(t *testing.T) {
	t.Helper()
	t.Setenv("SATELLE_HOME", t.TempDir())
	t.Setenv(config.SessionEnv, "")
	_ = os.Unsetenv(config.SessionEnv)
	t.Setenv("SATELLE_CONFIG", "")
	_ = os.Unsetenv("SATELLE_CONFIG")
	t.Setenv("SATELLE_SERVER_ENDPOINT", "none")
}

// gitMainTree makes an empty git repo with one commit and returns its root.
func gitMainTree(t *testing.T, base string) string {
	t.Helper()
	main := filepath.Join(base, "proj")
	if err := os.MkdirAll(main, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, main, "README.md", "x\n")
	gitIn(t, main, "init", "-q")
	gitIn(t, main, "config", "user.email", "t@example.com")
	gitIn(t, main, "config", "user.name", "t")
	gitIn(t, main, "add", "-A")
	gitIn(t, main, "commit", "-q", "-m", "init")
	return main
}

// alwaysPassSkill is a functional-check gate skill that always accepts, so a
// transition under the embedded route runs with no agent CLI.
func alwaysPassSkill(name string) string {
	return "---\nname: " + name + "\ntype: skill\ndescription: always-pass functional check for a fixture\n---\n\n```check\ntrue\n```\n"
}

// overrideEmbeddedGates replaces the embedded route's judged gates with
// functional checks (a same-named authored skill overrides its default).
func overrideEmbeddedGates(t *testing.T, root, dataDir string) {
	t.Helper()
	for _, name := range []string{
		"satelle-story-intent-review", "satelle-estimate-actual-review", "satelle-step-summary",
	} {
		writeFile(t, root, dataDir+"/skills/"+name+".md", alwaysPassSkill(name))
	}
}

func idOf(t *testing.T, stdout string) string {
	t.Helper()
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(stdout), &created); err != nil || created.ID == "" {
		t.Fatalf("parse story JSON: %v\n%s", err, stdout)
	}
	return created.ID
}

// wantUnreadableRefusal asserts the refusal text of AC1: the unreadable path, the
// embedded default route that would otherwise have governed (as binary-shipped),
// and that the story's gates would not be the repository's.
func wantUnreadableRefusal(t *testing.T, surface, wfPath, text string) {
	t.Helper()
	for _, want := range []string{
		wfPath, "embedded default route", "binary-shipped", `lane "default"`,
		"gates would not be the repository's gates",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("%s: refusal must carry %q:\n%s", surface, want, text)
		}
	}
}

func TestUnreadableAuthoredProcessRefusesEverySurface(t *testing.T) {
	isolateProcessEnv(t)
	main := gitMainTree(t, t.TempDir())
	writeFile(t, main, ".satelle/satelle.toml", "[review]\ngate_create = false\n")
	writeFile(t, main, ".satelle/workflows/done.toml", featureLaneDone)
	writeFile(t, main, ".satelle/workflows/step.toml", featureLaneStep)
	writeFile(t, main, ".satelle/skills/wt-place-check.md", placeCheckSkill)
	t.Chdir(main)
	if out, err := runRoot(t, "reindex"); err != nil {
		t.Fatalf("reindex: %v\n%s", err, out)
	}

	// A story engaged while the process is readable, so it carries a stored
	// route document: the refusal must not be bypassed by serving that.
	id := createFeatureStory(t, "engaged under a readable process")
	if out, err := runRoot(t, "story", "set", id, "--status", "plan"); err != nil {
		t.Fatalf("plan transition: %v\n%s", err, out)
	}
	if out, err := runRoot(t, "story", "route", id); err != nil || !strings.Contains(out, "**plan**") {
		t.Fatalf("baseline route: %v\n%s", err, out)
	}
	ledgerBefore, err := runRoot(t, "ledger", "list", "--story", id)
	if err != nil {
		t.Fatalf("ledger list: %v\n%s", err, ledgerBefore)
	}
	storiesBefore, err := runRoot(t, "story", "list")
	if err != nil {
		t.Fatalf("story list: %v\n%s", err, storiesBefore)
	}

	// Replace the workflows dir with a regular file: unreadable for root too. The
	// real dir is parked beside it so the stores can be read back afterwards —
	// every store command is refused while it is unreadable.
	wf := filepath.Join(main, ".satelle", "workflows")
	if err := os.Rename(wf, wf+".parked"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wf, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	restore := func() {
		t.Helper()
		if err := os.Remove(wf); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(wf+".parked", wf); err != nil {
			t.Fatal(err)
		}
	}

	refused := func(surface string, args ...string) string {
		t.Helper()
		out, err := runRoot(t, args...)
		if err == nil {
			t.Fatalf("%s must be refused while the workflows dir is unreadable\n%s", surface, out)
		}
		text := err.Error() + "\n" + out
		wantUnreadableRefusal(t, surface, wf, text)
		return text
	}

	refused("transition", "story", "set", id, "--status", "in_progress")
	refused("story route (engaged, stored route doc)", "story", "route", id)
	refused("create", "story", "create", "--title", "born under an unreadable process", "--body", "b",
		"--acceptance", "1. a", "--category", "feature")
	refused("amend", "story", "amend", id, "--title", "a corrected title", "--reason", "wrong")
	refused("restamp", "story", "restamp", id)

	// The three read-only diagnostics each name it, exit non-zero, and never
	// judge the embedded route as if it governed.
	for _, args := range [][]string{{"doctor"}, {"validate"}, {"agent", "validate"}} {
		out, err := runRoot(t, args...)
		if err == nil {
			t.Errorf("%v must exit non-zero while the workflows dir is unreadable\n%s", args, out)
		}
		text := out
		if err != nil {
			text += "\n" + err.Error()
		}
		for _, want := range []string{wf, "embedded default route"} {
			if !strings.Contains(text, want) {
				t.Errorf("%v must carry %q:\n%s", args, want, text)
			}
		}
		if strings.Contains(text, "node.alloc") || strings.Contains(text, "hook.alloc") {
			t.Errorf("%v judged the embedded route's allocations as if it governed:\n%s", args, text)
		}
		if args[0] == "doctor" {
			if n := strings.Count(out, "cannot be read"); n != 1 {
				t.Errorf("doctor must print the unreadable finding once, printed %d times:\n%s", n, out)
			}
		}
	}

	// Nothing was recorded by any refusal: read the stores back through the
	// restored dir.
	restore()
	if after, err := runRoot(t, "ledger", "list", "--story", id); err != nil || after != ledgerBefore {
		t.Errorf("a refusal recorded something on the ledger (err %v)\nbefore:\n%s\nafter:\n%s", err, ledgerBefore, after)
	}
	if after, err := runRoot(t, "story", "list"); err != nil || after != storiesBefore {
		t.Errorf("a refusal stored or changed a story (err %v)\nbefore:\n%s\nafter:\n%s", err, storiesBefore, after)
	}
}

// AC3: an ABSENT workflows dir keeps today's embedded backstop and is reported on
// all three surfaces — stderr at engage, the route document, doctor — never
// refused, and stdout stays JSON.
func TestAbsentAuthoredProcessIsReportedNeverRefused(t *testing.T) {
	isolateProcessEnv(t)
	main := gitMainTree(t, t.TempDir())
	writeFile(t, main, ".satelle/satelle.toml", "[review]\ngate_create = false\n")
	overrideEmbeddedGates(t, main, ".satelle")
	t.Chdir(main)
	// Index the overriding skills BEFORE anything transitions: unindexed, the
	// embedded rubrics would run and dispatch a real reviewer.
	if out, err := runRoot(t, "reindex"); err != nil {
		t.Fatalf("reindex: %v\n%s", err, out)
	}
	wf := filepath.Join(main, ".satelle", "workflows")
	if _, err := os.Stat(wf); !os.IsNotExist(err) {
		t.Fatalf("fixture must have no workflows dir: %v", err)
	}
	wantReport := func(surface, text string) {
		t.Helper()
		for _, want := range []string{wf, "embedded default route", "binary's defaults, not authored ones"} {
			if !strings.Contains(text, want) {
				t.Errorf("%s: absent-process report must carry %q:\n%s", surface, want, text)
			}
		}
	}

	// story route, live-render path: the story has not transitioned.
	idA := createFeatureStory(t, "absent process, live route")
	live, err := runRoot(t, "story", "route", idA)
	if err != nil {
		t.Fatalf("story route (live): %v\n%s", err, live)
	}
	if !strings.Contains(live, "## Process of record") {
		t.Errorf("live route lacks the Process of record section:\n%s", live)
	}
	wantReport("story route (live)", live)

	// engage: the backstop transition still succeeds; the report is on stderr and
	// stdout is still the story JSON.
	var stdout string
	var serr error
	stderr := captureStderr(t, func() {
		stdout, _, serr = runRootSplit(t, "", "story", "set", idA, "--status", "in_progress")
	})
	if serr != nil {
		t.Fatalf("the embedded backstop must still govern an absent dir: %v\n%s", serr, stdout)
	}
	wantReport("engage stderr", stderr)
	if !json.Valid([]byte(stdout)) || strings.Contains(stdout, "binary's defaults") {
		t.Errorf("stdout must stay JSON and carry no report:\n%s", stdout)
	}

	// story route, stored-doc path: the transition wrote a route document.
	stored, err := runRoot(t, "story", "route", idA)
	if err != nil {
		t.Fatalf("story route (stored): %v\n%s", err, stored)
	}
	if !strings.Contains(stored, "### backlog → in_progress") || !strings.Contains(stored, "## Process of record") {
		t.Errorf("stored route must carry the outcomes and the Process of record section:\n%s", stored)
	}
	wantReport("story route (stored)", stored)

	// doctor: a warning, not an error.
	out, _ := runRoot(t, "doctor")
	wantReport("doctor", out)
	if strings.Contains(out, "cannot be read") {
		t.Errorf("an absent dir must not be reported as unreadable:\n%s", out)
	}
}
