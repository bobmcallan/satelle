package verb_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// wireTrunkEpic is wireTrunk with the epic seat mode and a parent the stories
// under test share, so two of them can be in flight from distinct trees.
func wireTrunkEpic(t *testing.T) (trunkEnv, string) {
	t.Helper()
	e := wireTrunk(t, config.TrunkConfig{}, "", false)
	verb.SetEngagementMode(config.Config{Engagement: config.EngagementConfig{Parallel: config.ParallelEpic}})
	var parent workitem.Item
	if err := json.Unmarshal(call(t, "story-create", map[string]any{"title": "Container", "category": "feature"}), &parent); err != nil {
		t.Fatal(err)
	}
	return e, parent.ID
}

// createUnder creates a backlog story under parent on the trunk workflow.
func (e trunkEnv) createUnder(t *testing.T, parent string) string {
	t.Helper()
	var it workitem.Item
	raw := call(t, "story-create", map[string]any{
		"title": "child", "body": "goal", "acceptance_criteria": "1. checked",
		"category": "feature", "tags": []string{"workflow:eng"}, "parent_id": parent,
	})
	if err := json.Unmarshal(raw, &it); err != nil {
		t.Fatal(err)
	}
	return it.ID
}

// engageFrom engages a fresh story under parent from the working tree dir and
// returns its ID: the process works in dir, and the trunk check inspects it.
func (e trunkEnv) engageFrom(t *testing.T, parent, dir string) string {
	t.Helper()
	chdir(t, dir)
	verb.SetTrunkConfig(config.TrunkConfig{}, dir)
	e.gater.dir = dir
	id := e.createUnder(t, parent)
	if err := e.engage(t, id); err != nil {
		t.Fatalf("engage from %s: %v", dir, err)
	}
	return id
}

// A story engaged from a linked worktree is not refused for uncommitted changes
// in the main checkout when another story in flight was engaged there, and the
// line and the ledger name that story (sty_f1db1260).
func TestTrunkEngage_LinkedWorktreeForeignDirtyProceeds(t *testing.T) {
	e, parent := wireTrunkEpic(t)
	owner := e.engageFrom(t, parent, e.repos.Subject)
	e.out.Reset()
	e.dirty(t)
	linked := e.repos.LinkedWorktree(t, e.repos.Subject, "child")

	id := e.engageFrom(t, parent, linked)

	if got := e.status(t, id); got != "in_progress" {
		t.Fatalf("status = %q", got)
	}
	got := e.line(t)
	if !strings.HasPrefix(got, "satelle: trunk main level with origin/main; ") || !strings.Contains(got, "belong to engaged story "+owner) {
		t.Fatalf("line = %q", got)
	}
	rows := e.rows(t, id, ledger.KindTrunkCheck)
	if len(rows) != 1 {
		t.Fatalf("want 1 trunk_check row, got %d", len(rows))
	}
	var p struct {
		DirtyOwner string `json:"dirty_owner"`
	}
	if err := json.Unmarshal(rows[0].Payload, &p); err != nil {
		t.Fatal(err)
	}
	if p.DirtyOwner != owner {
		t.Fatalf("dirty_owner = %q, want %q", p.DirtyOwner, owner)
	}
}

// A dirty main that no story in flight accounts for is still reported and
// refused by default.
func TestTrunkEngage_LinkedWorktreeUnaccountedDirtyRefused(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, e trunkEnv, parent, other string)
	}{
		{"no other story is engaged", func(*testing.T, trunkEnv, string, string) {}},
		{"the owner finished", func(t *testing.T, e trunkEnv, parent, _ string) {
			id := e.engageFrom(t, parent, e.repos.Subject)
			if _, err := dispatchRaw(t, "story-set", map[string]any{"id": id, "status": "done"}); err != nil {
				t.Fatal(err)
			}
		}},
		{"the owner is parked", func(t *testing.T, e trunkEnv, parent, _ string) {
			id := e.engageFrom(t, parent, e.repos.Subject)
			if _, err := dispatchRaw(t, "story-set", map[string]any{"id": id, "status": "blocked", "reason": "waiting"}); err != nil {
				t.Fatal(err)
			}
		}},
		{"the other story was engaged in a different tree", func(t *testing.T, e trunkEnv, parent, other string) {
			e.engageFrom(t, parent, other)
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e, parent := wireTrunkEpic(t)
			c.setup(t, e, parent, e.repos.LinkedWorktree(t, e.repos.Subject, "other"))
			e.out.Reset()
			e.dirty(t)
			linked := e.repos.LinkedWorktree(t, e.repos.Subject, "child")
			chdir(t, linked)
			verb.SetTrunkConfig(config.TrunkConfig{}, linked)
			e.gater.dir = linked

			id := e.createUnder(t, parent)
			err := e.engage(t, id)

			if err == nil || !strings.Contains(err.Error(), "refused: trunk dirty tree on main") {
				t.Fatalf("engage err = %v, want a dirty-tree refusal", err)
			}
			if got := e.status(t, id); got != "backlog" {
				t.Fatalf("status = %q, want backlog", got)
			}
			got := e.line(t)
			if got != "satelle: trunk dirty tree on main" {
				t.Fatalf("line = %q", got)
			}
		})
	}
}
