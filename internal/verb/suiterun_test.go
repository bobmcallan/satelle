package verb_test

// Facts coverage for SHA-keyed shared suite evidence (sty_183a0510). These
// assert what the ENUMERATOR reports — accept-shaped and every violation —
// because no accept/reject rule lives in Go. The gate check block's DECISION
// over these same states is covered end-to-end in tests/suite_citation_test.go,
// which runs the block documented in the reviewer-checks help topic.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// citationFacts mirrors the ledger-citation result for assertions.
type citationFacts struct {
	StoryID    string `json:"story_id"`
	Citations  int    `json:"citations"`
	Cited      bool   `json:"cited"`
	CitationID string `json:"citation_id"`
	RunID      string `json:"run_id"`
	RunFound   bool   `json:"run_found"`
	Dangling   bool   `json:"dangling"`
	Run        *struct {
		StoryID    string `json:"story_id"`
		SHA        string `json:"sha"`
		Command    string `json:"command"`
		Outcome    string `json:"outcome"`
		StartedAt  string `json:"started_at"`
		FinishedAt string `json:"finished_at"`
		RecordedAt string `json:"recorded_at"`
	} `json:"run"`
	HeadSHA        string `json:"head_sha"`
	Dirty          bool   `json:"dirty"`
	SHAMatchesHead bool   `json:"sha_matches_head"`
	Note           string `json:"note"`
}

// suiteRepo makes a temp git repo with one commit, chdirs into it, and returns
// a committer (writes a file and commits it, returning the new HEAD).
func suiteRepo(t *testing.T) (dir string, commit func(name string) string) {
	t.Helper()
	dir = t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init")
	git("config", "user.email", "t@t")
	git("config", "user.name", "t")
	commit = func(name string) string {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		git("add", name)
		git("commit", "-m", name)
		head, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
		if err != nil {
			t.Fatalf("rev-parse: %v", err)
		}
		return strings.TrimSpace(string(head))
	}
	commit("seed.txt")
	cwd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	return dir, commit
}

func newStory(t *testing.T, title string) workitem.Item {
	t.Helper()
	var it workitem.Item
	if err := json.Unmarshal(call(t, "story-create", map[string]any{
		"title": title, "body": "goal for shared suite evidence",
		"acceptance_criteria": "1. cites a suite run",
		"category":            "feature",
	}), &it); err != nil {
		t.Fatal(err)
	}
	return it
}

func facts(t *testing.T, req any) citationFacts {
	t.Helper()
	var f citationFacts
	if err := json.Unmarshal(call(t, "ledger-citation", req), &f); err != nil {
		t.Fatal(err)
	}
	if f.Note == "" {
		t.Error("result must carry the enumeration-only note")
	}
	return f
}

// TestSuiteRunRecordAndReadBack: a typed record round-trips every field, and
// the sha defaults to HEAD (AC1, AC4 — a plain ledger row, no new object).
func TestSuiteRunRecordAndReadBack(t *testing.T) {
	wire(t)
	suiteRepo(t)
	it := newStory(t, "records the suite run")

	var rec ledger.Entry
	json.Unmarshal(call(t, "ledger-record-run", map[string]any{
		"story_id": it.ID, "command": "make integration", "outcome": "green",
		"started_at": "2026-08-04T01:00:00Z", "finished_at": "2026-08-04T01:35:00Z",
	}), &rec)
	if rec.ID == "" || rec.Kind != ledger.KindSuiteRun {
		t.Fatalf("bad record: %+v", rec)
	}

	var entries []ledger.Entry
	json.Unmarshal(call(t, "ledger-list", map[string]any{
		"story_id": it.ID, "kind": ledger.KindSuiteRun,
	}), &entries)
	if len(entries) != 1 {
		t.Fatalf("want 1 suite_run row, got %d", len(entries))
	}
	var p struct {
		SHA        string `json:"sha"`
		Command    string `json:"command"`
		Outcome    string `json:"outcome"`
		StartedAt  string `json:"started_at"`
		FinishedAt string `json:"finished_at"`
	}
	if err := json.Unmarshal(entries[0].Payload, &p); err != nil {
		t.Fatal(err)
	}
	gitHead, _ := exec.Command("git", "rev-parse", "HEAD").Output()
	if p.SHA != strings.TrimSpace(string(gitHead)) {
		t.Errorf("sha = %q, want current HEAD %q", p.SHA, strings.TrimSpace(string(gitHead)))
	}
	if p.Command != "make integration" || p.Outcome != "green" {
		t.Errorf("payload = %+v", p)
	}
	if p.StartedAt != "2026-08-04T01:00:00Z" || p.FinishedAt != "2026-08-04T01:35:00Z" {
		t.Errorf("timestamps did not round-trip: %+v", p)
	}
}

// TestSuiteRunRecordRefusesBadRequest: a missing command or an outcome outside
// green/red is a REQUEST error (the vocabulary readers share), not a verdict.
func TestSuiteRunRecordRefusesBadRequest(t *testing.T) {
	wire(t)
	suiteRepo(t)
	it := newStory(t, "bad record requests")

	cases := []struct {
		name string
		req  map[string]any
		want string
	}{
		{"no command", map[string]any{"story_id": it.ID, "outcome": "green"}, "--command"},
		{"no outcome", map[string]any{"story_id": it.ID, "command": "make integration"}, "--outcome"},
		{"bad outcome", map[string]any{"story_id": it.ID, "command": "make integration", "outcome": "maybe"}, "--outcome"},
		{"no story", map[string]any{"command": "make integration", "outcome": "green"}, "story id"},
		{"unknown story", map[string]any{"story_id": "sty_deadbeef", "command": "c", "outcome": "green"}, "not found"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := dispatchRaw(t, "ledger-record-run", c.req)
			if err == nil {
				t.Fatal("want an error")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error %q should mention %q", err, c.want)
			}
		})
	}
}

// TestSuiteCitationFactsAcceptShape: a green run at clean HEAD, cited by a
// SIBLING story, enumerates as the accept shape (AC2 accept case). Citation is
// the first write of Entry.Refs (AC4).
func TestSuiteCitationFactsAcceptShape(t *testing.T) {
	wire(t)
	suiteRepo(t)
	runner := newStory(t, "ran the suite")
	rider := newStory(t, "rides the suite run")

	var rec ledger.Entry
	json.Unmarshal(call(t, "ledger-record-run", map[string]any{
		"story_id": runner.ID, "command": "make integration", "outcome": "green",
	}), &rec)
	call(t, "ledger-cite-run", map[string]any{"story_id": rider.ID, "run_id": rec.ID})

	// The citation carries the run id in Refs — nothing else writes Refs today.
	var cites []ledger.Entry
	json.Unmarshal(call(t, "ledger-list", map[string]any{
		"story_id": rider.ID, "kind": ledger.KindSuiteCitation,
	}), &cites)
	if len(cites) != 1 {
		t.Fatalf("want 1 citation row, got %d", len(cites))
	}
	var refs struct {
		SuiteRun string `json:"suite_run"`
	}
	if err := json.Unmarshal(cites[0].Refs, &refs); err != nil {
		t.Fatal(err)
	}
	if refs.SuiteRun != rec.ID {
		t.Errorf("refs.suite_run = %q, want %q", refs.SuiteRun, rec.ID)
	}

	f := facts(t, map[string]any{"id": rider.ID})
	if !f.Cited || !f.RunFound || f.Dangling {
		t.Fatalf("accept shape broken: %+v", f)
	}
	if f.Run == nil || f.Run.Outcome != "green" || f.Run.Command != "make integration" {
		t.Fatalf("run facts = %+v", f.Run)
	}
	if f.Run.StoryID != runner.ID {
		t.Errorf("run.story_id = %q, want the RECORDING story %q", f.Run.StoryID, runner.ID)
	}
	if f.Dirty || !f.SHAMatchesHead || f.HeadSHA == "" {
		t.Errorf("want clean HEAD matching the run: %+v", f)
	}
	if f.RunID != rec.ID || f.CitationID != cites[0].ID || f.Citations != 1 {
		t.Errorf("ids/count wrong: %+v", f)
	}
}

// TestSuiteCitationFactsViolations: every violation the gate must be able to
// name is REPORTED (never an error) — missing, dangling, red, command mismatch,
// dirty worktree, stale sha (AC2 refusal cases, AC3 enumeration-only).
func TestSuiteCitationFactsViolations(t *testing.T) {
	wire(t)
	dir, commit := suiteRepo(t)

	record := func(story, command, outcome, sha string) ledger.Entry {
		t.Helper()
		req := map[string]any{"story_id": story, "command": command, "outcome": outcome}
		if sha != "" {
			req["sha"] = sha
		}
		var e ledger.Entry
		json.Unmarshal(call(t, "ledger-record-run", req), &e)
		return e
	}

	t.Run("missing citation", func(t *testing.T) {
		it := newStory(t, "cites nothing")
		f := facts(t, map[string]any{"id": it.ID})
		if f.Cited || f.Citations != 0 || f.RunFound {
			t.Fatalf("want no citation reported: %+v", f)
		}
	})

	t.Run("dangling citation", func(t *testing.T) {
		it := newStory(t, "cites a ghost")
		call(t, "ledger-cite-run", map[string]any{"story_id": it.ID, "run_id": "evt_ghost01"})
		f := facts(t, map[string]any{"id": it.ID})
		if !f.Cited || f.RunFound || !f.Dangling || f.RunID != "evt_ghost01" {
			t.Fatalf("want a dangling citation reported: %+v", f)
		}
	})

	t.Run("red run", func(t *testing.T) {
		it := newStory(t, "cites a red run")
		rec := record(it.ID, "make integration", "red", "")
		call(t, "ledger-cite-run", map[string]any{"story_id": it.ID, "run_id": rec.ID})
		f := facts(t, map[string]any{"id": it.ID})
		if !f.RunFound || f.Run.Outcome != "red" {
			t.Fatalf("want the red outcome reported: %+v", f)
		}
		if !f.SHAMatchesHead {
			t.Error("a red run at HEAD still matches HEAD — outcome is a separate fact")
		}
	})

	t.Run("command mismatch is reported verbatim", func(t *testing.T) {
		it := newStory(t, "cites another suite")
		rec := record(it.ID, "go test ./...", "green", "")
		call(t, "ledger-cite-run", map[string]any{"story_id": it.ID, "run_id": rec.ID})
		f := facts(t, map[string]any{"id": it.ID})
		if f.Run.Command != "go test ./..." {
			t.Fatalf("command must be echoed for the gate to compare: %+v", f.Run)
		}
	})

	t.Run("stale sha", func(t *testing.T) {
		it := newStory(t, "cites an older commit")
		rec := record(it.ID, "make integration", "green", "")
		call(t, "ledger-cite-run", map[string]any{"story_id": it.ID, "run_id": rec.ID})
		newHead := commit("later.txt")
		f := facts(t, map[string]any{"id": it.ID})
		if f.SHAMatchesHead {
			t.Fatalf("HEAD moved to %s — must not match run sha %s", newHead, f.Run.SHA)
		}
		if f.HeadSHA != newHead || f.Dirty {
			t.Errorf("want clean new HEAD reported: %+v", f)
		}
	})

	t.Run("dirty worktree", func(t *testing.T) {
		it := newStory(t, "cites a run but has edits")
		rec := record(it.ID, "make integration", "green", "")
		call(t, "ledger-cite-run", map[string]any{"story_id": it.ID, "run_id": rec.ID})
		if err := os.WriteFile(filepath.Join(dir, "scratch.txt"), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Remove(filepath.Join(dir, "scratch.txt")) })
		f := facts(t, map[string]any{"id": it.ID})
		if !f.Dirty {
			t.Fatalf("want the dirty worktree reported: %+v", f)
		}
		if !f.SHAMatchesHead {
			t.Error("dirty is its own fact — the sha still matches HEAD")
		}
	})
}

// TestSuiteCitationNewestWinsAndStdinID: a re-cite supersedes, and the story id
// may arrive as the piped transition payload a functional check receives.
func TestSuiteCitationNewestWinsAndStdinID(t *testing.T) {
	wire(t)
	suiteRepo(t)
	it := newStory(t, "cites twice")

	var first, second ledger.Entry
	json.Unmarshal(call(t, "ledger-record-run", map[string]any{
		"story_id": it.ID, "command": "old suite", "outcome": "red",
	}), &first)
	call(t, "ledger-cite-run", map[string]any{"story_id": it.ID, "run_id": first.ID})
	time.Sleep(5 * time.Millisecond)
	json.Unmarshal(call(t, "ledger-record-run", map[string]any{
		"story_id": it.ID, "command": "make integration", "outcome": "green",
	}), &second)
	call(t, "ledger-cite-run", map[string]any{"story_id": it.ID, "run_id": second.ID})

	f := facts(t, map[string]any{"id": it.ID})
	if f.Citations != 2 || f.RunID != second.ID || f.Run.Outcome != "green" {
		t.Fatalf("newest citation must win: %+v", f)
	}

	// Transition-payload shape: {story:{id}, from, to} with no argv id.
	piped := facts(t, map[string]any{
		"story": map[string]any{"id": it.ID}, "from": "in_progress", "to": "integration",
	})
	if piped.StoryID != it.ID || piped.RunID != second.ID {
		t.Fatalf("stdin transition payload must resolve the story: %+v", piped)
	}

	if _, err := dispatchRaw(t, "ledger-citation", map[string]any{}); err == nil {
		t.Error("no id anywhere should error, not report an empty citation")
	}
}
