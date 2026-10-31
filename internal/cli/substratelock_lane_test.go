package cli

// sty_08569de9, sty_9c782f0e — a substrate-lane story may change the substrate
// it was filed for while sibling epic children perform, and the refusal names the
// condition that actually applies. The lock is decided by substrateLane over the
// engagements a hook call resolved (resolveEngagementsFor → pickEngagementFor, by
// the tree the edit lands in); these tests engage real stories from real linked
// worktrees and drive the gate functions.

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/workitem"
	"github.com/spf13/cobra"
)

// laneSpec is one story of a laneEnv: a story of a category engaged from its own
// linked worktree.
type laneSpec struct {
	key, category string
}

// laneSession is the session every laneEnv story is engaged by. The tree an edit
// lands in decides its story; the session decides nothing.
const laneSession = "sess"

// laneEnv is a git repo (the main tree M) with the lock config written and one
// performing story per spec, each engaged from its own linked worktree. The
// hook's own tree is stubbed to none of them, so an edit in the main tree is
// attributed to no story.
type laneEnv struct {
	repo  string
	ids   map[string]string // spec key → story id
	trees map[string]string // spec key → linked worktree
}

func newLaneEnv(t *testing.T, specs []laneSpec) *laneEnv {
	t.Helper()
	fenceTempRootsElsewhere(t)
	db, create := attributionRepo(t)
	stubWorktree(t, "/w/none")
	repo, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".satelle", "satelle.toml"), []byte(lockExemptToml), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "init", "-q")
	gitIn(t, repo, "commit", "--allow-empty", "-qm", "seed")
	e := &laneEnv{repo: repo, ids: map[string]string{}, trees: map[string]string{}}
	ctx := context.Background()
	now := time.Now().UTC()
	for _, s := range specs {
		it := create(s.key, "in_progress")
		cat := s.category
		if _, err := db.Stories.Update(ctx, it.ID, workitem.UpdateInput{Category: &cat}, now); err != nil {
			t.Fatal(err)
		}
		tree := linkedWorktree(t, repo)
		recordEngagementOn(t, db, it.ID, "in_progress", tree, laneSession)
		e.ids[s.key], e.trees[s.key] = it.ID, tree
	}
	return e
}

// engagements is what a hook call resolves for an edit of target.
func (e *laneEnv) engagements(target string) *lockEngagements {
	return laneResolve(treeOf(target))
}

// laneResolve is the per-tree resolution the production call sites make.
func laneResolve(tree string) *lockEngagements {
	info, engaged, live, err := resolveEngagementsFor(laneSession, tree)
	if err != nil {
		panic(err)
	}
	return &lockEngagements{info: info, engaged: engaged, live: live}
}

// gate runs the lock on target through the Edit gate or a Bash redirect. The
// Edit gate is handed s (resolved for the target's tree, as `hook gate` does);
// the Bash gate resolves per candidate tree through the same resolution.
func (e *laneEnv) gate(t *testing.T, via, target string, s *lockEngagements) (error, string) {
	t.Helper()
	cmd := &cobra.Command{}
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	raw := []byte(claudeEditEvent(target))
	if via == "bash" {
		return bashSubstrateLockGate(cmd, raw, "echo x > "+target, loadGateProcess(), func(tree string) (*lockEngagements, error) {
			return laneResolve(tree), nil
		}), out.String()
	}
	return substrateLockGate(cmd, raw, target, s), out.String()
}

// rowsByStory counts the ledger rows of a kind per story.
func rowsByStory(t *testing.T, kind string) (map[string]int, []ledger.Entry) {
	t.Helper()
	rows := ledgerRows(t, kind)
	n := map[string]int{}
	for _, r := range rows {
		n[r.StoryID]++
	}
	return n, rows
}

const laneRel = ".satelle/workflows/agents.toml"

// AC1 + AC2: a substrate-lane story and non-substrate epic siblings, each
// engaged from its own worktree: an edit in the substrate story's worktree is the
// substrate story's change, so it passes and the other performing stories are
// noted, once per hook call — nothing is refused.
func TestSubstrateLockLaneFromTheSubstrateStorysTree(t *testing.T) {
	for _, via := range []string{"edit", "bash"} {
		t.Run(via, func(t *testing.T) {
			e := newLaneEnv(t, []laneSpec{
				{key: "S", category: "substrate"},
				{key: "F1", category: "fix"},
				{key: "F2", category: "fix"},
				{key: "X", category: "feature"},
			})
			target := filepath.Join(e.trees["S"], laneRel)
			s := e.engagements(target)
			if !s.engaged || s.info.ItemID != e.ids["S"] {
				t.Fatalf("fixture: the edit must be attributed to S by its tree, got %q engaged=%v", s.info.ItemID, s.engaged)
			}
			if err, out := e.gate(t, via, target, s); err != nil {
				t.Fatalf("a substrate-lane story's change must pass (%s): %v\n%s", via, err, out)
			}
			if n, _ := rowsByStory(t, ledger.KindSubstrateLockDeny); len(n) != 0 {
				t.Errorf("an allowed change records no refusal, got %v", n)
			}
			n, rows := rowsByStory(t, ledger.KindSubstrateChangeNote)
			for _, k := range []string{"F1", "F2", "X"} {
				if n[e.ids[k]] != 1 {
					t.Errorf("%s must be noted exactly once, got %d (%v)", k, n[e.ids[k]], n)
				}
			}
			if n[e.ids["S"]] != 0 || len(rows) != 3 {
				t.Errorf("the substrate story is not noted and nothing else is: %v", n)
			}
			for _, r := range rows {
				var p substrateChangePayload
				if err := json.Unmarshal(r.Payload, &p); err != nil || p.SubstrateStory != e.ids["S"] || p.Path != laneRel {
					t.Errorf("note payload = %s (%v), want substrate story %s and path %s", r.Payload, err, e.ids["S"], laneRel)
				}
			}
		})
	}
}

// AC3: an edit attributed by its tree to a story that is not substrate is
// refused even though a substrate story is also performing.
func TestSubstrateLockAttributedNonSubstrateRefused(t *testing.T) {
	for _, via := range []string{"edit", "bash"} {
		t.Run(via, func(t *testing.T) {
			e := newLaneEnv(t, []laneSpec{
				{key: "S", category: "substrate"},
				{key: "F", category: "fix"},
			})
			target := filepath.Join(e.trees["F"], laneRel)
			s := e.engagements(target)
			if !s.engaged || s.info.ItemID != e.ids["F"] {
				t.Fatalf("fixture: the edit must be attributed to F by tree, got %q engaged=%v", s.info.ItemID, s.engaged)
			}
			err, out := e.gate(t, via, target, s)
			if err == nil {
				t.Fatalf("an edit attributed to a non-substrate story must be refused:\n%s", out)
			}
			reason := denyReasonOf(t, out)
			if !strings.Contains(reason, e.ids["F"]+` (status`) || strings.Contains(reason, e.ids["S"]+` (status`) {
				t.Errorf("the deny names F, and not S, as the holder: %s", reason)
			}
			if n, _ := rowsByStory(t, ledger.KindSubstrateLockDeny); n[e.ids["F"]] != 1 || len(n) != 1 {
				t.Errorf("one refusal, on F: %v", n)
			}
			if n, _ := rowsByStory(t, ledger.KindSubstrateChangeNote); len(n) != 0 {
				t.Errorf("a refusal notes nobody: %v", n)
			}
		})
	}
}

// AC2: change notes record an allowed change only. A Bash command with one
// candidate the lane passes (S's own worktree) and a later one it refuses (F's
// own worktree) does not run, so it leaves no substrate_change_note — only the
// refusal.
func TestSubstrateLockRefusedCommandLeavesNoChangeNotes(t *testing.T) {
	e := newLaneEnv(t, []laneSpec{
		{key: "S", category: "substrate"},
		{key: "F", category: "fix"},
		{key: "X", category: "feature"},
	})
	passes := filepath.Join(e.trees["S"], laneRel)
	refused := filepath.Join(e.trees["F"], laneRel)
	cmd := &cobra.Command{}
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	err := bashSubstrateLockGate(cmd, []byte(claudeEditEvent(passes)), "echo x > "+passes+" && echo y > "+refused, loadGateProcess(), func(tree string) (*lockEngagements, error) {
		return laneResolve(tree), nil
	})
	if err == nil {
		t.Fatalf("a command that rewrites F's own worktree substrate must be refused:\n%s", out.String())
	}
	if n, _ := rowsByStory(t, ledger.KindSubstrateChangeNote); len(n) != 0 {
		t.Errorf("a refused command leaves no change note, got %v", n)
	}
	if n, _ := rowsByStory(t, ledger.KindSubstrateLockDeny); n[e.ids["F"]] != 1 || len(n) != 1 {
		t.Errorf("one refusal, on F: %v", n)
	}
}

// AC4 + AC5: every condition that is not the lane is still refused while a
// non-substrate story performs, and the deny names the holder (id, status,
// category), the condition and its fix — and no file-a-substrate-story advice
// when a substrate story is already performing.
func TestSubstrateLockRefusalNamesTheCondition(t *testing.T) {
	const advice = "make the change under a substrate-lane story"
	cases := []struct {
		name  string
		specs []laneSpec
		// target: the key whose worktree is edited ("" = the main tree)
		target string
		// holders are the spec keys the deny must name
		holders []string
		want    func(ids map[string]string) []string
		not     []string
	}{
		{
			name:    "nothing engaged from this tree",
			specs:   []laneSpec{{key: "F1", category: "feature"}},
			holders: []string{"F1"},
			want: func(map[string]string) []string {
				return []string{"No performing story is engaged from", "Engage the substrate story from this tree", advice}
			},
			not: []string{"re-engage", "attributed to story"},
		},
		{
			name: "nothing engaged from this tree, a substrate story elsewhere",
			specs: []laneSpec{
				{key: "S", category: "substrate"},
				{key: "F1", category: "feature"},
			},
			holders: []string{"F1"},
			want: func(map[string]string) []string {
				return []string{"No performing story is engaged from", "edit from its worktree"}
			},
			// a substrate story is performing: no file-a-substrate-story advice
			not: []string{advice, "file one"},
		},
		{
			name: "attributed, a substrate story elsewhere",
			specs: []laneSpec{
				{key: "S", category: "substrate"},
				{key: "F", category: "fix"},
			},
			target:  "F",
			holders: []string{"F"},
			want: func(ids map[string]string) []string {
				return []string{"attributed to story " + ids["F"], "worktree of substrate-lane story " + ids["S"]}
			},
			not: []string{advice},
		},
		{
			name:    "attributed, no substrate story",
			specs:   []laneSpec{{key: "F", category: "fix"}},
			target:  "F",
			holders: []string{"F"},
			want: func(ids map[string]string) []string {
				return []string{"attributed to story " + ids["F"], advice, "finish or park " + ids["F"]}
			},
			not: []string{"worktree of substrate-lane story"},
		},
	}
	for _, via := range []string{"edit", "bash"} {
		for _, tc := range cases {
			t.Run(via+"/"+tc.name, func(t *testing.T) {
				e := newLaneEnv(t, tc.specs)
				dir := e.repo
				if tc.target != "" {
					dir = e.trees[tc.target]
				}
				target := filepath.Join(dir, laneRel)
				s := e.engagements(target)
				err, out := e.gate(t, via, target, s)
				if err == nil {
					t.Fatalf("the lock must still refuse while a non-substrate story performs:\n%s", out)
				}
				reason := denyReasonOf(t, out)
				for _, k := range tc.holders {
					cat := ""
					for _, sp := range tc.specs {
						if sp.key == k {
							cat = sp.category
						}
					}
					if want := e.ids[k] + ` (status "in_progress", category "` + cat + `")`; !strings.Contains(reason, want) {
						t.Errorf("deny must name the holder as %s: %s", want, reason)
					}
				}
				for _, w := range tc.want(e.ids) {
					if !strings.Contains(reason, w) {
						t.Errorf("deny missing %q: %s", w, reason)
					}
				}
				for _, w := range tc.not {
					if strings.Contains(reason, w) {
						t.Errorf("deny must not contain %q: %s", w, reason)
					}
				}
				if n, _ := rowsByStory(t, ledger.KindSubstrateChangeNote); len(n) != 0 {
					t.Errorf("a refusal notes nobody: %v", n)
				}
			})
		}
	}
}

// substrateLane is pure over the engagements and the category answer: a
// category the store could not give is "not substrate", so a doubt refuses.
func TestSubstrateLaneDoubtIsNotTheLane(t *testing.T) {
	s := &lockEngagements{
		info: engagement{ItemID: "sty_a", SessionID: "sess"}, engaged: true,
		live: []engagement{{ItemID: "sty_a", SessionID: "sess"}, {ItemID: "sty_b", SessionID: "other"}},
	}
	if lane := substrateLane(s, func(string) bool { return false }); lane.Lane != nil || lane.Why != laneOwnNotSubstrate {
		t.Errorf("an unreadable category is not the lane: %+v", lane)
	}
	lane := substrateLane(s, func(id string) bool { return id == "sty_a" })
	if lane.Lane == nil || lane.Lane.ItemID != "sty_a" || len(lane.Noted) != 1 || lane.Noted[0].ItemID != "sty_b" {
		t.Errorf("the attributed substrate story is the lane and its sibling is noted: %+v", lane)
	}
}
