package verb_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// reanchorScenario wires a git repo plus a backlog/in_progress/done workflow
// with a blocked park, and holds the helpers shared by the re-anchor ordering
// tests (sty_12ce4271).
type reanchorScenario struct {
	t   *testing.T
	dir string
}

func newReanchorScenario(t *testing.T) *reanchorScenario {
	t.Helper()
	withWiring(t)
	dir := gitRepo(t)
	chdir(t, dir)
	wireWithWorkflows(t, routeHalves(
		`["*"]
obligations = ["raised", "coded", "closed"]
park = { state = "blocked" }
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
`))
	verb.SetTransitionGater(stubGater{dec: verb.GateDecision{Gated: false}})
	return &reanchorScenario{t: t, dir: dir}
}

// commit writes name, commits it and returns the new HEAD.
func (s *reanchorScenario) commit(name string) string {
	s.t.Helper()
	if err := os.WriteFile(filepath.Join(s.dir, name), []byte(name+"\n"), 0o644); err != nil {
		s.t.Fatal(err)
	}
	for _, args := range [][]string{{"add", name}, {"commit", "-m", "add " + name}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = s.dir
		if out, err := cmd.CombinedOutput(); err != nil {
			s.t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	out, err := exec.Command("git", "-C", s.dir, "rev-parse", "HEAD").Output()
	if err != nil {
		s.t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

func (s *reanchorScenario) create() workitem.Item {
	s.t.Helper()
	var it workitem.Item
	json.Unmarshal(call(s.t, "story-create", map[string]any{
		"title": "reanchor ordering", "body": "body",
		"acceptance_criteria": "1. x",
		"category":            "feature",
		"tags":                []string{"workflow:eng"},
	}), &it)
	return it
}

func (s *reanchorScenario) set(id, status string) {
	s.t.Helper()
	call(s.t, "story-set", map[string]any{"id": id, "status": status})
}

type reanchorRow struct {
	From, To       string
	SinceSHA       string
	HeadSHA        string
	Files          []string
	ReanchorResume bool
}

func (s *reanchorScenario) rows(id string) []reanchorRow {
	s.t.Helper()
	var recs []ledger.Entry
	json.Unmarshal(call(s.t, "ledger-list", map[string]any{"story_id": id, "kind": ledger.KindChangeRecord}), &recs)
	var out []reanchorRow
	for _, e := range recs {
		var p struct {
			From           string   `json:"from"`
			To             string   `json:"to"`
			SinceSHA       string   `json:"since_sha"`
			HeadSHA        string   `json:"head_sha"`
			Files          []string `json:"files"`
			ReanchorResume bool     `json:"reanchor_resume"`
		}
		json.Unmarshal(e.Payload, &p)
		out = append(out, reanchorRow{p.From, p.To, p.SinceSHA, p.HeadSHA, p.Files, p.ReanchorResume})
	}
	return out
}

func (s *reanchorScenario) diff(id string) (baseline string, files []string) {
	s.t.Helper()
	var res struct {
		Files    []string `json:"files"`
		Baseline string   `json:"baseline_sha"`
	}
	json.Unmarshal(call(s.t, "story-diff", map[string]any{"id": id}), &res)
	return res.Baseline, res.Files
}

func containsStr(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

// A re-anchor written while the story sat in backlog, before it ever engaged,
// must not override the engagement baseline: the story did not take its tree
// until it engaged, so commits merged in between are not its work (AC1, AC2).
func TestBacklogReanchorDoesNotOverrideEngagementBaseline(t *testing.T) {
	s := newReanchorScenario(t)
	it := s.create()

	// Park and resume while still in backlog: writes a re-anchor row at A.
	shaA := s.commit("a_before.txt")
	s.set(it.ID, "blocked")
	s.set(it.ID, "backlog")
	pre := s.rows(it.ID)
	if len(pre) == 0 || !pre[len(pre)-1].ReanchorResume || pre[len(pre)-1].HeadSHA != shaA {
		t.Fatalf("setup: want a backlog re-anchor at %s, got %+v", shaA, pre)
	}

	// Other work lands on the trunk, then the story first engages: baseline at B.
	shaB := s.commit("b_merged_elsewhere.txt")
	s.set(it.ID, "in_progress")
	s.commit("c_mine.txt")

	var baselines []ledger.Entry
	json.Unmarshal(call(t, "ledger-list", map[string]any{"story_id": it.ID, "kind": ledger.KindEngagementBaseline}), &baselines)
	if len(baselines) != 1 {
		t.Fatalf("want 1 engagement_baseline, got %d", len(baselines))
	}
	var bp struct {
		HeadSHA string `json:"head_sha"`
	}
	json.Unmarshal(baselines[0].Payload, &bp)
	if bp.HeadSHA != shaB {
		t.Fatalf("baseline head_sha=%q want %q", bp.HeadSHA, shaB)
	}

	// AC1: the diff measures from the baseline, not from the older re-anchor.
	baseline, files := s.diff(it.ID)
	if baseline != shaB {
		t.Errorf("diff baseline_sha=%q want engagement baseline %q (re-anchor was %q)", baseline, shaB, shaA)
	}
	if len(files) != 1 || files[0] != "c_mine.txt" {
		t.Errorf("diff files=%v want only [c_mine.txt]", files)
	}

	// AC2: the engaging edge's change_record anchors at the baseline too.
	var engaging *reanchorRow
	for _, r := range s.rows(it.ID) {
		if r.From == "backlog" && r.To == "in_progress" && !r.ReanchorResume {
			r := r
			engaging = &r
		}
	}
	if engaging == nil {
		t.Fatal("no change_record on the engaging edge")
	}
	if engaging.SinceSHA != shaB {
		t.Errorf("engaging row since_sha=%q want baseline %q", engaging.SinceSHA, shaB)
	}
	for _, f := range []string{"a_before.txt", "b_merged_elsewhere.txt"} {
		if containsStr(engaging.Files, f) {
			t.Errorf("engaging row holds already-merged file %q: %v", f, engaging.Files)
		}
	}

	// A later row anchors from the engaging row, never from the backlog re-anchor.
	s.set(it.ID, "done")
	rows := s.rows(it.ID)
	last := rows[len(rows)-1]
	if last.SinceSHA == shaA || last.SinceSHA == "" {
		t.Errorf("later row since_sha=%q must not be the pre-engagement re-anchor %q", last.SinceSHA, shaA)
	}
	if !containsStr(last.Files, "c_mine.txt") || containsStr(last.Files, "b_merged_elsewhere.txt") {
		t.Errorf("later row files=%v want c_mine.txt and not b_merged_elsewhere.txt", last.Files)
	}
}

// Parking and resuming an ENGAGED story keeps re-anchoring at the resume point:
// the diff, the next change_record and the park-window exclusion are unchanged
// by the pre-engagement filter (AC3).
func TestEngagedParkResumeReanchorsNextChangeRecord(t *testing.T) {
	s := newReanchorScenario(t)
	it := s.create()
	s.set(it.ID, "in_progress")
	s.commit("mine_before.txt")
	s.set(it.ID, "blocked")
	foreignSHA := s.commit("foreign_during_park.txt")
	s.set(it.ID, "in_progress")
	s.commit("mine_after.txt")

	baseline, files := s.diff(it.ID)
	if baseline != foreignSHA {
		t.Errorf("diff baseline_sha=%q want resume re-anchor %q", baseline, foreignSHA)
	}
	if containsStr(files, "foreign_during_park.txt") || !containsStr(files, "mine_after.txt") {
		t.Errorf("diff files=%v want mine_after.txt and not foreign_during_park.txt", files)
	}

	s.set(it.ID, "done")
	rows := s.rows(it.ID)
	last := rows[len(rows)-1]
	if last.SinceSHA != foreignSHA {
		t.Errorf("next change_record since_sha=%q want resume re-anchor %q", last.SinceSHA, foreignSHA)
	}
	if containsStr(last.Files, "foreign_during_park.txt") {
		t.Errorf("next change_record holds the park-window commit: %v", last.Files)
	}
}
