package verb_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/epicset"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// epicCloseWF is one lane for every category: backlog → in_progress → done, with
// a park for cancelled. The epic-parent closes on the same route as its children;
// what differs is only the commit-time membership check (sty_9f4f8e12).
var epicCloseWF = routeHalves(
	`["*"]
obligations = ["raised", "coded", "closed"]
park = { state = "blocked" }
cancel = { state = "cancelled" }

[epic-parent]
obligations = ["raised", "coded", "closed"]
park = { state = "blocked" }
cancel = { state = "cancelled" }

[parent]
obligations = ["raised", "coded", "closed"]
park = { state = "blocked" }
cancel = { state = "cancelled" }
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
`)

func mkEpicStory(t *testing.T, req map[string]any) workitem.Item {
	t.Helper()
	req["title"] = "x"
	if _, ok := req["status"]; !ok {
		req["status"] = "backlog"
	}
	var it workitem.Item
	if err := json.Unmarshal(call(t, "story-create", req), &it); err != nil {
		t.Fatal(err)
	}
	return it
}

func setStatus(t *testing.T, id, status string) error {
	t.Helper()
	_, err := dispatchRaw(t, "story-set", map[string]any{"id": id, "status": status})
	return err
}

// closeChild walks a story to done through the declared route, one at a time.
func closeChild(t *testing.T, id string) {
	t.Helper()
	for _, s := range []string{"in_progress", "done"} {
		if err := setStatus(t, id, s); err != nil {
			t.Fatalf("%s → %s: %v", id, s, err)
		}
	}
}

func engageEpic(t *testing.T, epic workitem.Item) {
	t.Helper()
	if err := setStatus(t, epic.ID, "in_progress"); err != nil {
		t.Fatalf("engage epic: %v", err)
	}
}

// AC3: a done child with parent_id set and one OPEN child that carries only the
// tag (no parent_id). The close refuses and names the open child.
func TestEpicCloseRefusesOpenTagOnlyChild(t *testing.T) {
	wireWithWorkflows(t, epicCloseWF)
	epic := mkEpicStory(t, map[string]any{"category": "epic-parent", "tags": []string{"epic:t"}})
	done := mkEpicStory(t, map[string]any{"category": "fix", "tags": []string{"epic:t"}, "parent_id": epic.ID})
	closeChild(t, done.ID)
	open := mkEpicStory(t, map[string]any{"category": "fix", "tags": []string{"epic:t"}})
	engageEpic(t, epic)

	err := setStatus(t, epic.ID, "done")
	if err == nil {
		t.Fatal("close must refuse while a tag-only child is open")
	}
	if !strings.Contains(err.Error(), open.ID) || !strings.Contains(err.Error(), "backlog") {
		t.Errorf("refusal must name the open child and its status: %v", err)
	}
	if strings.Contains(err.Error(), done.ID) {
		t.Errorf("the done child must not be named: %v", err)
	}
	if got := statusOf(t, epic.ID).Status; got != "in_progress" {
		t.Errorf("epic status = %q after a refused close, want in_progress", got)
	}
}

// AC1: parent_id is not membership — a non-terminal story linked only by
// parent_id (no epic: tag) does not hold the epic open; a cancelled member does
// not either.
func TestEpicCloseIgnoresParentLinkWithoutTag(t *testing.T) {
	wireWithWorkflows(t, epicCloseWF)
	epic := mkEpicStory(t, map[string]any{"category": "epic-parent", "tags": []string{"epic:t"}})
	linked := mkEpicStory(t, map[string]any{"category": "fix", "parent_id": epic.ID})
	cancelled := mkEpicStory(t, map[string]any{"category": "fix", "tags": []string{"epic:t"}})
	for _, s := range []string{"in_progress", "cancelled"} {
		if err := setStatus(t, cancelled.ID, s); err != nil {
			t.Fatalf("%s → %s: %v", cancelled.ID, s, err)
		}
	}
	engageEpic(t, epic)

	if err := setStatus(t, epic.ID, "done"); err != nil {
		t.Fatalf("a parent_id link without the tag is not membership, and cancelled is resolved: %v", err)
	}
	if got := statusOf(t, linked.ID).Status; got != "backlog" {
		t.Errorf("the linked story must be untouched, status = %q", got)
	}
}

// AC4: the reviewer payload is taken while every child is terminal; a
// non-terminal member is filed inside the window before the commit. The commit
// re-reads the set and refuses, naming the member filed in the window.
func TestEpicCloseRereadsSetAtCommit(t *testing.T) {
	wireWithWorkflows(t, epicCloseWF)
	epic := mkEpicStory(t, map[string]any{"category": "epic-parent", "tags": []string{"epic:t"}})
	child := mkEpicStory(t, map[string]any{"category": "fix", "tags": []string{"epic:t"}})
	closeChild(t, child.ID)
	engageEpic(t, epic)

	var snapshot []epicset.Ref
	var late workitem.Item
	verb.SetTransitionGater(gaterFunc(func(item workitem.Item, _ string) verb.GateDecision {
		// The payload a reviewer is handed: every child terminal.
		members := call(t, "story-list", map[string]any{"tag": "epic:t"})
		var all []workitem.Item
		json.Unmarshal(members, &all)
		for _, m := range all {
			if m.ID != item.ID {
				snapshot = append(snapshot, epicset.Ref{ID: m.ID, Status: m.Status})
			}
		}
		// A member is filed AFTER the payload was taken, before the commit.
		late = mkEpicStory(t, map[string]any{"category": "fix", "tags": []string{"epic:t"}})
		return verb.GateDecision{Gated: true, Accept: true, Skill: "satelle-story-done-review"}
	}))
	t.Cleanup(func() { verb.SetTransitionGater(nil) })

	err := setStatus(t, epic.ID, "done")
	if len(snapshot) != 1 || snapshot[0].Status != "done" {
		t.Fatalf("the snapshot must show every child terminal: %+v", snapshot)
	}
	if err == nil {
		t.Fatal("commit must refuse: a non-terminal member was filed after the payload was taken")
	}
	if !strings.Contains(err.Error(), late.ID) {
		t.Errorf("refusal must name the member filed inside the window (%s): %v", late.ID, err)
	}
	if got := statusOf(t, epic.ID).Status; got != "in_progress" {
		t.Errorf("epic status = %q after a refused commit, want in_progress", got)
	}
}

// AC5: fails closed — an epic whose set cannot be determined does not close, and
// the refusal says why.
func TestEpicCloseFailsClosedWhenSetUndetermined(t *testing.T) {
	cases := map[string]struct {
		setup func(t *testing.T) workitem.Item
		want  string
	}{
		"no epic tag": {
			setup: func(t *testing.T) workitem.Item {
				return mkEpicStory(t, map[string]any{"category": "epic-parent"})
			},
			want: "no epic:<theme> tag",
		},
		"two epic tags": {
			setup: func(t *testing.T) workitem.Item {
				return mkEpicStory(t, map[string]any{"category": "epic-parent", "tags": []string{"epic:a", "epic:b"}})
			},
			want: "2 epic tags",
		},
		"two epic-parents on one tag": {
			setup: func(t *testing.T) workitem.Item {
				mkEpicStory(t, map[string]any{"category": "epic-parent", "tags": []string{"epic:t"}})
				return mkEpicStory(t, map[string]any{"category": "epic-parent", "tags": []string{"epic:t"}})
			},
			want: "2 epic-parents carry epic:t",
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			wireWithWorkflows(t, epicCloseWF)
			epic := c.setup(t)
			engageEpic(t, epic)
			err := setStatus(t, epic.ID, "done")
			if err == nil {
				t.Fatal("an undetermined set must not close")
			}
			if !strings.Contains(err.Error(), "undetermined") || !strings.Contains(err.Error(), c.want) {
				t.Errorf("refusal must say why (%q): %v", c.want, err)
			}
			if got := statusOf(t, epic.ID).Status; got != "in_progress" {
				t.Errorf("status = %q, want in_progress", got)
			}
		})
	}
}

// A non-epic container is unchanged: its members stay the parent_id links and it
// closes with no epic: tag at all.
func TestNonEpicContainerCloseUnchanged(t *testing.T) {
	wireWithWorkflows(t, epicCloseWF)
	parent := mkEpicStory(t, map[string]any{"category": "parent"})
	engageEpic(t, parent)
	if err := setStatus(t, parent.ID, "done"); err != nil {
		t.Fatalf("a non-epic container closes without an epic: tag: %v", err)
	}
}

// AC8: a proposal filed with an epic: tag whose container is already done or
// cancelled is filed without that tag and with a body note naming the container.
func TestCreateDropsEpicTagOfClosedContainer(t *testing.T) {
	for _, terminal := range []string{"done", "cancelled"} {
		t.Run(terminal, func(t *testing.T) {
			wireWithWorkflows(t, epicCloseWF)
			epic := mkEpicStory(t, map[string]any{"category": "epic-parent", "tags": []string{"epic:t"}})
			engageEpic(t, epic)
			if err := setStatus(t, epic.ID, terminal); err != nil {
				t.Fatalf("epic → %s: %v", terminal, err)
			}

			p := mkEpicStory(t, map[string]any{
				"category": "fix", "tags": []string{"epic:t", "retrospective:sty_x"}, "body": "proposal body",
			})
			for _, tag := range p.Tags {
				if tag == "epic:t" {
					t.Errorf("the closed container's tag was stamped: %v", p.Tags)
				}
			}
			if !containsTag(p.Tags, "retrospective:sty_x") {
				t.Errorf("every other tag is kept: %v", p.Tags)
			}
			if !strings.Contains(p.Body, "proposal body") || !strings.Contains(p.Body, epic.ID) || !strings.Contains(p.Body, terminal) {
				t.Errorf("body must keep its text and note the container and why:\n%s", p.Body)
			}
		})
	}
}

func TestCreateKeepsEpicTagOfLiveContainer(t *testing.T) {
	wireWithWorkflows(t, epicCloseWF)
	mkEpicStory(t, map[string]any{"category": "epic-parent", "tags": []string{"epic:t"}})
	p := mkEpicStory(t, map[string]any{"category": "fix", "tags": []string{"epic:t"}, "body": "b"})
	if !containsTag(p.Tags, "epic:t") || strings.Contains(p.Body, "Filed without") {
		t.Errorf("a live container keeps the tag and gets no note: tags=%v body=%q", p.Tags, p.Body)
	}
	// A theme with no container, and the creation of a new epic-parent itself,
	// are left alone.
	q := mkEpicStory(t, map[string]any{"category": "fix", "tags": []string{"epic:other"}})
	if !containsTag(q.Tags, "epic:other") {
		t.Errorf("a theme with no container keeps its tag: %v", q.Tags)
	}
}

func containsTag(tags []string, want string) bool {
	for _, t := range tags {
		if t == want {
			return true
		}
	}
	return false
}
