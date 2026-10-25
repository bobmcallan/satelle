package agentstep

// check_reuse: tree (sty_dc554488): a functional check whose skill opts in is
// recorded as a suite_run on the tree it proved and not re-run while the story,
// check, command and tree are unchanged.

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// reuseHarness is an engine over a temp git repo, a counting fake check, and an
// in-memory evidence store behind the real seam signatures.
type reuseHarness struct {
	g       *Engine
	dir     string
	runs    int
	fail    bool
	records []verb.SuiteEvidence
}

func newReuseHarness(t *testing.T, wired bool) *reuseHarness {
	t.Helper()
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init")
	git("config", "user.email", "t@t")
	git("config", "user.name", "t")
	h := &reuseHarness{dir: dir}
	h.write(t, "a.txt", "a\n")
	git("add", "-A")
	git("commit", "-m", "init")

	h.g, _ = newEngine(t, "", fakeDocs{})
	h.g.repoRoot = dir
	h.g.check = func(context.Context, string, string, string) (string, error) {
		h.runs++
		time.Sleep(2 * time.Millisecond)
		if h.fail {
			return "boom", errors.New("exit 1")
		}
		return "", nil
	}
	if wired {
		h.g.SetSuiteEvidence(
			func(_ context.Context, story string, ev verb.SuiteEvidence) (string, error) {
				ev.ID = "evt_" + string(rune('a'+len(h.records)))
				ev.RecordedAt = time.Date(2026, 10, 25, 3, 0, 0, 0, time.UTC)
				h.records = append(h.records, ev)
				return ev.ID, nil
			},
			func(_ context.Context, story, check, cmdSHA, tree string) (verb.SuiteEvidence, bool) {
				for i := len(h.records) - 1; i >= 0; i-- {
					r := h.records[i]
					if r.Outcome == verb.SuiteGreen && r.Check == check && r.CommandSHA == cmdSHA && r.Tree == tree {
						return r, true
					}
				}
				return verb.SuiteEvidence{}, false
			})
	}
	return h
}

func (h *reuseHarness) write(t *testing.T, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(h.dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (h *reuseHarness) run(command, reuse string) verb.GateDecision {
	return h.g.runCheck(context.Background(), workitem.Item{ID: "sty_r"}, "suite-check", command, "{}", reuse)
}

// AC1: a pass records one green suite_run naming the check, tree, command and duration.
func TestCheckReuseRecordsGreenRun(t *testing.T) {
	h := newReuseHarness(t, true)
	if dec := h.run("make x", "tree"); !dec.Accept {
		t.Fatalf("rejected: %+v", dec)
	}
	if len(h.records) != 1 {
		t.Fatalf("records = %d, want 1", len(h.records))
	}
	r := h.records[0]
	tree, head, err := verb.GitContentTree(h.dir)
	if err != nil {
		t.Fatal(err)
	}
	if r.Check != "suite-check" || r.Tree != tree || r.SHA != head || r.Command != "make x" ||
		r.CommandSHA != verb.CommandDigest("make x") || r.Outcome != verb.SuiteGreen || r.DurationMS <= 0 {
		t.Errorf("evidence = %+v (tree %s head %s)", r, tree, head)
	}
}

// AC2: the same check on an unchanged tree accepts without running, citing the entry.
func TestCheckReuseSkipsRunOnUnchangedTree(t *testing.T) {
	h := newReuseHarness(t, true)
	h.run("make x", "tree")
	dec := h.run("make x", "tree")
	if !dec.Accept || h.runs != 1 || len(h.records) != 1 {
		t.Fatalf("accept=%v runs=%d records=%d, want accepted with 1 run and 1 record", dec.Accept, h.runs, len(h.records))
	}
	for _, want := range []string{"reused suite_run evt_a", "tree " + verb.ShortTree(h.records[0].Tree), "2026-10-25T03:00:00Z", "not re-run"} {
		if !strings.Contains(dec.Notes, want) {
			t.Errorf("notes %q lack %q", dec.Notes, want)
		}
	}
}

// AC2/AC5: a one-line change, an untracked file or an edited command re-runs.
func TestCheckReuseReRunsWhenInputsChange(t *testing.T) {
	h := newReuseHarness(t, true)
	h.run("make x", "tree")

	h.write(t, "a.txt", "a changed\n")
	h.run("make x", "tree")
	if h.runs != 2 {
		t.Errorf("after a one-line change runs = %d, want 2", h.runs)
	}
	h.run("make x", "tree")
	if h.runs != 2 {
		t.Errorf("unchanged again runs = %d, want 2", h.runs)
	}

	h.write(t, "untracked.txt", "u\n")
	h.run("make x", "tree")
	if h.runs != 3 {
		t.Errorf("after an untracked file runs = %d, want 3", h.runs)
	}

	h.run("make y", "tree")
	if h.runs != 4 {
		t.Errorf("after the command changed runs = %d, want 4", h.runs)
	}
}

// AC3: a failure records only a red run and the next presentation re-runs.
func TestCheckReuseFailureIsNeverReused(t *testing.T) {
	h := newReuseHarness(t, true)
	h.fail = true
	if dec := h.run("make x", "tree"); dec.Accept {
		t.Fatal("a failing check accepted")
	}
	if len(h.records) != 1 || h.records[0].Outcome != verb.SuiteRed {
		t.Fatalf("records = %+v, want one red", h.records)
	}
	if dec := h.run("make x", "tree"); dec.Accept || h.runs != 2 {
		t.Errorf("accept=%v runs=%d, want a second failing run", dec.Accept, h.runs)
	}
	h.fail = false
	if dec := h.run("make x", "tree"); !dec.Accept || h.runs != 3 {
		t.Errorf("accept=%v runs=%d, want a green third run", dec.Accept, h.runs)
	}
	if dec := h.run("make x", "tree"); !dec.Accept || h.runs != 3 {
		t.Errorf("accept=%v runs=%d, want the green run reused", dec.Accept, h.runs)
	}
}

// AC4: a check that does not opt in runs every time and records nothing, as does
// an opted-in check when the evidence seam is unwired or the dir is not a repo.
func TestCheckWithoutReuseRunsEveryTime(t *testing.T) {
	h := newReuseHarness(t, true)
	h.run("make x", "")
	h.run("make x", "")
	h.run("make x", "other")
	if h.runs != 3 || len(h.records) != 0 {
		t.Errorf("runs=%d records=%d, want 3 runs and none recorded", h.runs, len(h.records))
	}

	u := newReuseHarness(t, false)
	u.run("make x", "tree")
	u.run("make x", "tree")
	if u.runs != 2 {
		t.Errorf("unwired seam runs = %d, want 2", u.runs)
	}

	n := newReuseHarness(t, true)
	n.g.repoRoot = t.TempDir()
	n.run("make x", "tree")
	n.run("make x", "tree")
	if n.runs != 2 || len(n.records) != 0 {
		t.Errorf("no repo: runs=%d records=%d, want 2 runs and none recorded", n.runs, len(n.records))
	}
}
