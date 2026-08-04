//go:build integration

package tests

// SHA-keyed shared suite evidence, end-to-end (sty_183a0510): record one run,
// let siblings cite it, and run the gate check block DOCUMENTED in the
// reviewer-checks help topic over the enumerated facts.
//
// The block is extracted from the shipped topic rather than copied here, so the
// documentation and the tested decision cannot drift. The binary only reports
// facts; every accept/refuse decision below is the check block's.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/help"
)

// documentedCheckBlock returns the sample gate check block from the
// reviewer-checks help topic — the one consuming repos are told to use.
func documentedCheckBlock(t *testing.T) string {
	t.Helper()
	topic, ok := help.Get("reviewer-checks")
	if !ok {
		t.Fatal("reviewer-checks help topic missing")
	}
	const heading = "### Sample gate check block"
	i := strings.Index(topic.Body, heading)
	if i < 0 {
		t.Fatalf("help topic must document the sample check block under %q", heading)
	}
	rest := topic.Body[i:]
	start := strings.Index(rest, "```bash")
	if start < 0 {
		t.Fatal("no bash block under the sample check-block heading")
	}
	rest = rest[start+len("```bash"):]
	end := strings.Index(rest, "```")
	if end < 0 {
		t.Fatal("unterminated bash block in the help topic")
	}
	script := strings.TrimSpace(rest[:end])
	for _, want := range []string{"satelle ledger citation", "missing_citation", "dangling_citation",
		"red_run", "command_mismatch", "dirty_worktree", "stale_sha"} {
		if !strings.Contains(script, want) {
			t.Errorf("documented check block must name %q", want)
		}
	}
	return script
}

// suiteCitationRepo makes a git repo with satelle initialised, plus a bin dir
// whose `satelle` is the binary under test (the check block calls it by name).
func suiteCitationRepo(t *testing.T) (repo string, gate func(story, expected string) (string, error), commit func(string)) {
	t.Helper()
	repo = t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init")
	git("config", "user.email", "t@t")
	git("config", "user.name", "t")
	// satelle's own runtime writes (backlog views, local settings) must not make
	// the tree dirty — a consuming repo ignores them the same way.
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte(".satelle/\n.claude/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, testBin, repo, "init")
	git("add", "-A")
	git("commit", "-m", "init")

	binDir := t.TempDir()
	if err := os.Symlink(testBin, filepath.Join(binDir, "satelle")); err != nil {
		t.Fatalf("link satelle onto PATH: %v", err)
	}
	script := filepath.Join(t.TempDir(), "check.sh")
	if err := os.WriteFile(script, []byte(documentedCheckBlock(t)), 0o755); err != nil {
		t.Fatal(err)
	}

	// gate runs the documented check block exactly as a functional check does:
	// the transition payload on stdin, exit status is the verdict.
	gate = func(story, expected string) (string, error) {
		t.Helper()
		cmd := exec.Command("bash", script)
		cmd.Dir = repo
		cmd.Stdin = strings.NewReader(`{"story":{"id":"` + story + `"},"from":"in_progress","to":"integration"}`)
		cmd.Env = append(isolatedEnv(t),
			"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
			"EXPECTED="+expected)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	commit = func(name string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(repo, name), []byte(name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		git("add", name)
		git("commit", "-m", name)
	}
	return repo, gate, commit
}

func newCitationStory(t *testing.T, repo, title string) string {
	t.Helper()
	out := mustRun(t, testBin, repo, "story", "create",
		"--title", title,
		"--body", "Rides a shared suite run rather than re-running the suite.",
		"--acceptance", "1. the suite-citation gate accepts",
		"--category", "chore")
	id := extractID(out, "sty_")
	if id == "" {
		t.Fatalf("no story id in:\n%s", out)
	}
	return id
}

// TestSuiteRunRecordReadBackCLI covers AC1 at the CLI: a typed record-run
// command (no raw payload flag) round-trips every field through `ledger list`.
func TestSuiteRunRecordReadBackCLI(t *testing.T) {
	repo, _, _ := suiteCitationRepo(t)
	story := newCitationStory(t, repo, "records the shared suite run")

	out := mustRun(t, testBin, repo, "ledger", "record-run",
		"--story", story, "--command", "make integration", "--outcome", "green",
		"--started-at", "2026-08-04T01:00:00Z", "--finished-at", "2026-08-04T01:35:00Z")
	runID := extractID(out, "evt_")
	if runID == "" {
		t.Fatalf("record-run must return the entry id:\n%s", out)
	}

	// `ledger append` must not have grown a raw payload escape hatch.
	if help, err := run(t, testBin, repo, "ledger", "append", "--help"); err == nil && strings.Contains(help, "--payload") {
		t.Error("ledger append must stay typed — no raw --payload flag")
	}

	listed := mustRun(t, testBin, repo, "ledger", "list", "--story", story, "--kind", "suite_run")
	var rows []struct {
		ID      string          `json:"id"`
		StoryID string          `json:"story_id"`
		Kind    string          `json:"kind"`
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal([]byte(listed), &rows); err != nil {
		t.Fatalf("ledger list: %v\n%s", err, listed)
	}
	if len(rows) != 1 || rows[0].ID != runID || rows[0].StoryID != story {
		t.Fatalf("want one suite_run row for %s:\n%s", story, listed)
	}
	var p map[string]string
	if err := json.Unmarshal(rows[0].Payload, &p); err != nil {
		t.Fatal(err)
	}
	head, _ := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	want := map[string]string{
		"sha":         strings.TrimSpace(string(head)),
		"command":     "make integration",
		"outcome":     "green",
		"started_at":  "2026-08-04T01:00:00Z",
		"finished_at": "2026-08-04T01:35:00Z",
	}
	for k, v := range want {
		if p[k] != v {
			t.Errorf("payload[%s] = %q, want %q", k, p[k], v)
		}
	}
}

// TestSuiteCitationSharedAcrossStories covers AC6 (and the accept half of AC2):
// two stories cite one recorded run and the documented check block accepts
// both; a third story at a later commit is refused naming stale_sha.
func TestSuiteCitationSharedAcrossStories(t *testing.T) {
	repo, gate, commit := suiteCitationRepo(t)
	const suite = "echo suite"

	a := newCitationStory(t, repo, "story A ran the suite")
	b := newCitationStory(t, repo, "story B rides the run")
	c := newCitationStory(t, repo, "story C lands after the run")

	runID := extractID(mustRun(t, testBin, repo, "ledger", "record-run",
		"--story", a, "--command", suite, "--outcome", "green"), "evt_")
	if runID == "" {
		t.Fatal("no run id recorded")
	}
	mustRun(t, testBin, repo, "ledger", "cite-run", "--story", a, "--run", runID)
	mustRun(t, testBin, repo, "ledger", "cite-run", "--story", b, "--run", runID)

	for _, id := range []string{a, b} {
		out, err := gate(id, suite)
		if err != nil {
			t.Fatalf("gate must accept %s on the shared run: %v\n%s", id, err, out)
		}
		if !strings.Contains(out, "suite citation accepted") {
			t.Errorf("accept output for %s:\n%s", id, out)
		}
	}

	// A later commit: the same run no longer covers HEAD.
	commit("later.txt")
	mustRun(t, testBin, repo, "ledger", "cite-run", "--story", c, "--run", runID)
	out, err := gate(c, suite)
	if err == nil {
		t.Fatalf("gate must refuse a citation from a later commit:\n%s", out)
	}
	if !strings.Contains(out, "stale_sha") {
		t.Errorf("refusal must name stale_sha:\n%s", out)
	}
	// And the earlier citers are now stale too — the rule is HEAD, not history.
	if out, err := gate(a, suite); err == nil || !strings.Contains(out, "stale_sha") {
		t.Errorf("story A must also be stale once HEAD moves:\n%s", out)
	}
}

// TestSuiteCitationGateRefusals covers the refusal half of AC2: the documented
// check block names every violation. The binary reported facts and exited 0 in
// each case — the decision is the gate's.
func TestSuiteCitationGateRefusals(t *testing.T) {
	repo, gate, _ := suiteCitationRepo(t)
	const suite = "echo suite"

	t.Run("missing_citation", func(t *testing.T) {
		id := newCitationStory(t, repo, "cites nothing at all")
		out, err := gate(id, suite)
		if err == nil || !strings.Contains(out, "missing_citation") {
			t.Fatalf("want missing_citation, got err=%v:\n%s", err, out)
		}
		// The verb itself reported facts happily — no verdict in Go.
		if _, verr := run(t, testBin, repo, "ledger", "citation", id); verr != nil {
			t.Errorf("enumeration must exit 0 with no citation: %v", verr)
		}
	})

	t.Run("dangling_citation", func(t *testing.T) {
		id := newCitationStory(t, repo, "cites a run that never existed")
		mustRun(t, testBin, repo, "ledger", "cite-run", "--story", id, "--run", "evt_ghost01")
		out, err := gate(id, suite)
		if err == nil || !strings.Contains(out, "dangling_citation") {
			t.Fatalf("want dangling_citation, got err=%v:\n%s", err, out)
		}
	})

	t.Run("red_run", func(t *testing.T) {
		id := newCitationStory(t, repo, "cites a failed suite run")
		runID := extractID(mustRun(t, testBin, repo, "ledger", "record-run",
			"--story", id, "--command", suite, "--outcome", "red"), "evt_")
		mustRun(t, testBin, repo, "ledger", "cite-run", "--story", id, "--run", runID)
		out, err := gate(id, suite)
		if err == nil || !strings.Contains(out, "red_run") {
			t.Fatalf("want red_run, got err=%v:\n%s", err, out)
		}
	})

	t.Run("command_mismatch", func(t *testing.T) {
		id := newCitationStory(t, repo, "cites a different suite")
		runID := extractID(mustRun(t, testBin, repo, "ledger", "record-run",
			"--story", id, "--command", "go test ./...", "--outcome", "green"), "evt_")
		mustRun(t, testBin, repo, "ledger", "cite-run", "--story", id, "--run", runID)
		out, err := gate(id, suite)
		if err == nil || !strings.Contains(out, "command_mismatch") {
			t.Fatalf("want command_mismatch, got err=%v:\n%s", err, out)
		}
	})

	t.Run("dirty_worktree", func(t *testing.T) {
		id := newCitationStory(t, repo, "cites a run with edits outstanding")
		runID := extractID(mustRun(t, testBin, repo, "ledger", "record-run",
			"--story", id, "--command", suite, "--outcome", "green"), "evt_")
		mustRun(t, testBin, repo, "ledger", "cite-run", "--story", id, "--run", runID)
		scratch := filepath.Join(repo, "scratch.txt")
		if err := os.WriteFile(scratch, []byte("uncommitted\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		defer os.Remove(scratch)
		out, err := gate(id, suite)
		if err == nil || !strings.Contains(out, "dirty_worktree") {
			t.Fatalf("want dirty_worktree, got err=%v:\n%s", err, out)
		}
	})
}
