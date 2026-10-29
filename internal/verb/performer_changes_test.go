package verb_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/agentartifact"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/epicset"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// sty_af0b1761: a read-only readiness performer reports the story configuration
// it wants as `changes`, and satelle applies them on the record under the step's
// apply_changes declaration. The fixture route declares it on both readiness
// steps (a container's ready step and a story's readied step); the behaviour
// under test is the binary applying what the route allows, within its limits.

const changesApply = `apply_changes = { ops = ["add-tags", "create-child"], tags = ["order:", "lane:", "depends-on:", "surface:"] }` + "\n"

// changesStep is the ready fixture with apply_changes (and the propose it needs)
// declared on the container's ready step, and apply_changes on the story's.
func changesStep() string { return changesStepMark("ready") }

// changesStepMark is changesStep with the readiness steps' mark set to mark.
func changesStepMark(mark string) string {
	s := strings.Replace(readyStepToml(mark, readyReviewers), "waits_on_children = true\n", "waits_on_children = true\npropose = true\n"+changesApply, 1)
	return strings.Replace(s, "propose = true\nreject_budget = 3\n", "propose = true\n"+changesApply+"reject_budget = 3\n", 1)
}

// stubWorkflow stamps every created story, as a wired resolver does.
type stubWorkflow struct{}

func (stubWorkflow) WorkflowNameFor(context.Context, string) (string, error) { return "default", nil }

func (stubWorkflow) WorkflowStates(context.Context, string) ([]string, bool, error) {
	return nil, true, nil
}

// changesWorld is an epic with two children and a story outside it.
type changesWorld struct {
	r             *readyRig
	epic, a, b, x workitem.Item
}

func newChangesWorld(t *testing.T) changesWorld { return newChangesWorldMark(t, "ready") }

// newChangesWorldMark is newChangesWorld over readiness steps marking with mark.
func newChangesWorldMark(t *testing.T, mark string) changesWorld {
	t.Helper()
	return newChangesWorldFor(t, changesStepMark(mark))
}

// newChangesWorldFor is newChangesWorld over the step catalogue stepToml.
func newChangesWorldFor(t *testing.T, stepToml string) changesWorld {
	t.Helper()
	r := newRigFor(t, stepToml)
	t.Setenv("SATELLE_HOME", t.TempDir())
	verb.SetWorkflowResolver(stubWorkflow{})
	w := changesWorld{r: r}
	w.epic = newStoryOf(t, "epic-parent", "epic", "epic:rr")
	w.a = newStoryOf(t, "feature", "childa", "epic:rr")
	w.b = newStoryOf(t, "feature", "childb", "epic:rr")
	w.x = newStoryOf(t, "feature", "outside")
	r.g.verd = func(it workitem.Item, to string) verb.GateDecision {
		if to == "ready" {
			return verb.GateDecision{} // the container's ready step has no reviewers
		}
		return acceptAll()
	}
	return w
}

// performerReports makes the performer of container's step report changes (and
// reject with notes when rejectNotes is set); every other story's performer
// reports nothing.
func performerReports(containerID string, changes []agentartifact.Change, rejectNotes string) func(workitem.Item, string) (verb.DispatchResult, error) {
	return func(it workitem.Item, to string) (verb.DispatchResult, error) {
		if to != "ready" && to != "plan" {
			return verb.DispatchResult{}, nil // the park edge dispatches nobody
		}
		res := verb.DispatchResult{Dispatched: true, Agent: "ready-reviewer", Skill: "ready-review", Model: "stub-model", ModelResolved: "stub-model-resolved"}
		if it.ID != containerID {
			return res, nil
		}
		res.Changes = changes
		if rejectNotes != "" {
			return res, &verb.PerformerReject{Notes: rejectNotes, Changes: changes}
		}
		return res, nil
	}
}

func tagsOf(t *testing.T, id string) []string { return statusOf(t, id).Tags }

func carries(tags []string, tag string) bool {
	for _, x := range tags {
		if x == tag {
			return true
		}
	}
	return false
}

// rowsBy returns id's rows of kind written by actor.
func rowsBy(t *testing.T, id, kind, actor string) []ledger.Entry {
	t.Helper()
	var out []ledger.Entry
	for _, e := range ledgerRows(t, id, kind) {
		if e.Actor == actor {
			out = append(out, e)
		}
	}
	return out
}

func sourceOf(t *testing.T, e ledger.Entry) string {
	t.Helper()
	var p struct {
		Source string `json:"source"`
	}
	if err := json.Unmarshal(e.Payload, &p); err != nil {
		t.Fatalf("payload of %s row: %v", e.Kind, err)
	}
	return p.Source
}

func membersOf(t *testing.T, container workitem.Item) []workitem.Item {
	t.Helper()
	var all []workitem.Item
	if err := json.Unmarshal(call(t, "story-list", map[string]any{}), &all); err != nil {
		t.Fatal(err)
	}
	got, err := epicset.MembersFromItems(all, container)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func titled(items []workitem.Item, title string) (workitem.Item, bool) {
	for _, it := range items {
		if it.Title == title {
			return it, true
		}
	}
	return workitem.Item{}, false
}

// AC1, AC2, AC4: under `story ready` the reported tags and child are on the
// record; each change leaves exactly one row recording it, attributed to the
// performer with the source step; the child is a member of the epic, passes the
// create rules, and names the performer — not the driving session — as creator.
func TestReportedChangesApplyUnderStoryReady(t *testing.T) {
	w := newChangesWorld(t)
	t.Setenv(config.SessionEnv, "driving-session")
	config.PublishSessionModel("driving-session", verb.SessionModelRoleInLoop, "driving-model", "claude", "")
	w.r.d.perform = performerReports(w.epic.ID, []agentartifact.Change{
		{Op: "add-tags", Story: w.a.ID, Tags: []string{"order:2", "surface:cli"}},
		{Op: "add-tags", Story: w.epic.ID, Tags: []string{"lane:trunk"}},
		{Op: "create-child", Title: "filed by report", Body: "B", Acceptance: "1. one", Category: "feature", Tags: []string{"surface:cli"}},
	}, "")

	res := readyCall(t, w.epic.ID)[0]
	if res.Outcome != verb.ReadyMarked {
		t.Fatalf("the epic must be readied: %+v", res)
	}
	if got := tagsOf(t, w.a.ID); !carries(got, "order:2") || !carries(got, "surface:cli") {
		t.Errorf("child a tags = %v, want order:2 and surface:cli added", got)
	}
	if got := tagsOf(t, w.epic.ID); !carries(got, "lane:trunk") {
		t.Errorf("epic tags = %v, want lane:trunk added", got)
	}

	child, ok := titled(membersOf(t, statusOf(t, w.epic.ID)), "filed by report")
	if !ok {
		t.Fatal("the reported child must be a member of the epic's set")
	}
	if child.ParentID != w.epic.ID || !carries(child.Tags, "epic:rr") || !carries(child.Tags, "surface:cli") || !carries(child.Tags, "workflow:default") {
		t.Errorf("child must be an epic member under the epic with its reported and stamped tags: %+v", child)
	}
	if child.Status != "backlog" {
		t.Errorf("child status = %q, want backlog", child.Status)
	}
	var wave struct{ Runnable []string }
	if err := json.Unmarshal(call(t, "story-wave", map[string]any{"id": w.epic.ID}), &wave); err != nil || !carries(wave.Runnable, child.ID) {
		t.Errorf("story wave must see the reported child: %v %+v", err, wave)
	}

	// Exactly one row per applied change, by the performer, naming the source step.
	const performer, source = "ready-reviewer", "performer:ready"
	for _, c := range []struct {
		id   string
		want string
	}{{w.a.ID, "order:2"}, {w.epic.ID, "lane:trunk"}} {
		rows := rowsBy(t, c.id, ledger.KindStoryUpdated, performer)
		if len(rows) != 1 || sourceOf(t, rows[0]) != source || !strings.Contains(string(rows[0].Payload), c.want) {
			t.Errorf("%s: want exactly one story_updated row by %s with source %s naming %s, got %+v", c.id, performer, source, c.want, rows)
		}
	}
	created := ledgerRows(t, child.ID, ledger.KindStoryCreated)
	if len(created) != 1 || created[0].Actor != performer || sourceOf(t, created[0]) != source {
		t.Errorf("the child must have exactly one story_created row by the performer with the source: %+v", created)
	}

	// Who created the child is the performer, in every record of it.
	var creator []struct {
		Role, Model, SessionID string
	}
	for _, e := range ledgerRows(t, child.ID, ledger.KindSessionModel) {
		var p struct {
			Role      string `json:"role"`
			Model     string `json:"model"`
			SessionID string `json:"session_id"`
		}
		_ = json.Unmarshal(e.Payload, &p)
		creator = append(creator, struct{ Role, Model, SessionID string }{p.Role, p.Model, p.SessionID})
	}
	if len(creator) != 1 || creator[0].Role != verb.SessionModelRoleCreator || creator[0].Model != "stub-model-resolved" || creator[0].SessionID != "" {
		t.Errorf("creator record = %+v, want the performer's model and no session (the driving session is driving-session / driving-model)", creator)
	}
	if got := ledgerRows(t, child.ID, ledger.KindWorkflowStamped); len(got) != 1 || got[0].Actor != "" {
		t.Errorf("the workflow stamp row keeps its own (empty) actor: %+v", got)
	}
}

// AC4: a performer that reports no model records an explicit unknown with its reason.
func TestReportedChildWithoutAModelRecordsUnknown(t *testing.T) {
	w := newChangesWorld(t)
	t.Setenv(config.SessionEnv, "driving-session")
	config.PublishSessionModel("driving-session", verb.SessionModelRoleInLoop, "driving-model", "claude", "")
	w.r.d.perform = func(it workitem.Item, to string) (verb.DispatchResult, error) {
		res, err := performerReports(w.epic.ID, []agentartifact.Change{{Op: "create-child", Title: "no model", Category: "feature"}}, "")(it, to)
		res.Model, res.ModelResolved = "", ""
		return res, err
	}
	readyCall(t, w.epic.ID)
	child, ok := titled(membersOf(t, statusOf(t, w.epic.ID)), "no model")
	if !ok {
		t.Fatal("the child must exist")
	}
	rows := ledgerRows(t, child.ID, ledger.KindSessionModel)
	if len(rows) != 1 {
		t.Fatalf("want one creator row, got %+v", rows)
	}
	var p struct{ Model, Reason, SessionID string }
	_ = json.Unmarshal(rows[0].Payload, &p)
	if p.Model != "unknown" || p.Reason == "" || p.SessionID != "" {
		t.Errorf("creator row = %+v, want unknown with a reason and no session id", p)
	}
}

// AC1 (engaged route): the performer accepts and reports changes on the
// container's own ready edge; the repaired tags are on the record and in the
// item the gate judged.
func TestReportedChangesAreInTheEngagedGatePayload(t *testing.T) {
	w := newChangesWorld(t)
	w.r.d.perform = performerReports(w.epic.ID, []agentartifact.Change{
		{Op: "add-tags", Story: w.epic.ID, Tags: []string{"lane:trunk"}},
		{Op: "add-tags", Story: w.a.ID, Tags: []string{"order:5"}},
	}, "")
	var judged []string
	w.r.g.verd = func(it workitem.Item, to string) verb.GateDecision {
		if it.ID == w.epic.ID && to == "ready" {
			judged = it.Tags
		}
		return acceptAll()
	}
	if _, err := dispatchRaw(t, "story-set", map[string]any{"id": w.epic.ID, "status": "ready"}); err != nil {
		t.Fatal(err)
	}
	if !carries(judged, "lane:trunk") {
		t.Errorf("the gate judged tags %v, want the repaired record with lane:trunk", judged)
	}
	if !carries(tagsOf(t, w.epic.ID), "lane:trunk") || !carries(tagsOf(t, w.a.ID), "order:5") {
		t.Errorf("the repaired tags must be on the record: epic %v, a %v", tagsOf(t, w.epic.ID), tagsOf(t, w.a.ID))
	}
	if got := statusOf(t, w.epic.ID).Status; got != "ready" {
		t.Errorf("status = %q, want ready", got)
	}
}

// AC3: every limit, one case each; a batch with any violation applies nothing and
// the round fails naming the offending change and the limit.
func TestReportedChangesLimits(t *testing.T) {
	type tc struct {
		name    string
		changes func(w changesWorld) []agentartifact.Change
		want    string
	}
	cases := []tc{
		{"undeclared op", func(w changesWorld) []agentartifact.Change {
			return []agentartifact.Change{{Op: "delete-story", Story: w.a.ID}}
		}, `op "delete-story" is not declared`},
		{"undeclared prefix", func(w changesWorld) []agentartifact.Change {
			return []agentartifact.Change{{Op: "add-tags", Story: w.a.ID, Tags: []string{"workflow:other"}}}
		}, "no declared prefix"},
		{"target outside the epic set", func(w changesWorld) []agentartifact.Change {
			return []agentartifact.Change{{Op: "add-tags", Story: w.x.ID, Tags: []string{"order:1"}}}
		}, "neither the container"},
		{"no target", func(w changesWorld) []agentartifact.Change {
			return []agentartifact.Change{{Op: "add-tags", Tags: []string{"order:1"}}}
		}, "neither the container"},
		{"title change", func(w changesWorld) []agentartifact.Change {
			return []agentartifact.Change{{Op: "add-tags", Story: w.a.ID, Tags: []string{"order:1"}, Title: "renamed"}}
		}, "changes the definition of existing story "},
		{"body change", func(w changesWorld) []agentartifact.Change {
			return []agentartifact.Change{{Op: "add-tags", Story: w.a.ID, Tags: []string{"order:1"}, Body: "new body"}}
		}, "changes the definition of existing story"},
		{"acceptance change", func(w changesWorld) []agentartifact.Change {
			return []agentartifact.Change{{Op: "add-tags", Story: w.a.ID, Tags: []string{"order:1"}, Acceptance: "1. new"}}
		}, "changes the definition of existing story"},
		{"tag removal", func(w changesWorld) []agentartifact.Change {
			return []agentartifact.Change{{Op: "add-tags", Story: w.a.ID, Tags: []string{"order:1"}, Remove: []string{"epic:rr"}}}
		}, "removes tags"},
		{"create-child naming another story", func(w changesWorld) []agentartifact.Change {
			return []agentartifact.Change{{Op: "create-child", Story: w.a.ID, Title: "new", Category: "feature"}}
		}, "a created child has no target"},
		{"duplicate child title", func(w changesWorld) []agentartifact.Change {
			return []agentartifact.Change{{Op: "create-child", Title: "childa", Category: "feature"}}
		}, "duplicates the title"},
		{"duplicate title within the batch", func(w changesWorld) []agentartifact.Change {
			return []agentartifact.Change{
				{Op: "create-child", Title: "twice", Category: "feature"},
				{Op: "create-child", Title: "Twice", Category: "feature"},
			}
		}, "duplicates the title"},
		{"child without a title", func(w changesWorld) []agentartifact.Change {
			return []agentartifact.Change{{Op: "create-child", Category: "feature"}}
		}, "has no title"},
		{"depends-on cycle across two changes", func(w changesWorld) []agentartifact.Change {
			return []agentartifact.Change{
				{Op: "add-tags", Story: w.a.ID, Tags: []string{"depends-on:" + w.b.ID}},
				{Op: "add-tags", Story: w.b.ID, Tags: []string{"depends-on:" + w.a.ID}},
			}
		}, "depends-on cycle"},
		{"depends-on a non-member", func(w changesWorld) []agentartifact.Change {
			return []agentartifact.Change{{Op: "add-tags", Story: w.a.ID, Tags: []string{"depends-on:" + w.x.ID}}}
		}, "leaves the epic set"},
		{"a valid change then a violation applies neither", func(w changesWorld) []agentartifact.Change {
			return []agentartifact.Change{
				{Op: "add-tags", Story: w.a.ID, Tags: []string{"order:9"}},
				{Op: "create-child", Title: "kept out", Category: "feature"},
				{Op: "add-tags", Story: w.x.ID, Tags: []string{"order:1"}},
			}
		}, "changes[2]"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := newChangesWorld(t)
			w.r.d.perform = performerReports(w.epic.ID, c.changes(w), "")
			before := map[string][]string{}
			for _, id := range []string{w.epic.ID, w.a.ID, w.b.ID, w.x.ID} {
				before[id] = tagsOf(t, id)
			}
			storiesBefore := len(membersOf(t, w.epic)) + len(listAll(t))
			res := readyCall(t, w.epic.ID)[0]
			if res.Outcome != verb.ReadyNotAccepted || !strings.Contains(res.Reason, c.want) {
				t.Fatalf("want a not-accepted round whose reason contains %q, got %+v", c.want, res)
			}
			if !strings.Contains(res.Reason, "changes[") {
				t.Errorf("the reason must name the offending change: %q", res.Reason)
			}
			for id, was := range before {
				got := tagsOf(t, id)
				if id != w.a.ID && id != w.b.ID { // the children are readied (marked) on their own rounds
					if strings.Join(got, ",") != strings.Join(was, ",") {
						t.Errorf("%s tags changed: %v -> %v", id, was, got)
					}
					continue
				}
				for _, tag := range got {
					if !carries(was, tag) && tag != "ready" {
						t.Errorf("%s gained %q", id, tag)
					}
				}
			}
			if got := len(membersOf(t, w.epic)) + len(listAll(t)); got != storiesBefore {
				t.Errorf("a story was created: %d -> %d", storiesBefore, got)
			}
			for _, id := range []string{w.epic.ID, w.a.ID, w.b.ID, w.x.ID} {
				for _, kind := range []string{ledger.KindStoryUpdated, ledger.KindStoryCreated} {
					if rows := rowsBy(t, id, kind, "ready-reviewer"); len(rows) != 0 {
						t.Errorf("%s: a refused batch wrote %s rows by the performer: %+v", id, kind, rows)
					}
				}
			}
			if hasMark(t, w.epic.ID) {
				t.Errorf("a refused round must not mark the epic")
			}
		})
	}
}

func listAll(t *testing.T) []workitem.Item {
	t.Helper()
	var all []workitem.Item
	if err := json.Unmarshal(call(t, "story-list", map[string]any{}), &all); err != nil {
		t.Fatal(err)
	}
	return all
}

// AC3: a vocabulary the tag set must keep to refuses a reported tag the way
// `story set` does, with nothing applied.
func TestReportedTagOutsideTheVocabularyIsRefused(t *testing.T) {
	w := newChangesWorld(t)
	verb.SetTagVocabulary(surfaceVocab())
	t.Cleanup(verb.ClearTagVocabulary)
	w.r.d.perform = performerReports(w.epic.ID, []agentartifact.Change{
		{Op: "add-tags", Story: w.epic.ID, Tags: []string{"lane:trunk"}},
		{Op: "add-tags", Story: w.epic.ID, Tags: []string{"surface:web"}},
	}, "")
	res := readyCall(t, w.epic.ID)[0]
	if res.Outcome != verb.ReadyNotAccepted || !strings.Contains(res.Reason, "web") {
		t.Fatalf("want a refusal naming the tag, got %+v", res)
	}
	if carries(tagsOf(t, w.epic.ID), "lane:trunk") {
		t.Errorf("nothing may be applied: %v", tagsOf(t, w.epic.ID))
	}
}

// AC3: a non-epic container's members are its parent_id children: one may be
// targeted, a story outside it may not.
func TestReportedChangesTargetAParentIDMember(t *testing.T) {
	newWorld := func(t *testing.T) (*readyRig, workitem.Item, workitem.Item, workitem.Item) {
		r := newRigFor(t, changesStep())
		parent := newStoryOf(t, "feature", "parent")
		var member workitem.Item
		if err := json.Unmarshal(call(t, "story-create", map[string]any{"title": "member", "category": "feature", "parent_id": parent.ID}), &member); err != nil {
			t.Fatal(err)
		}
		return r, parent, member, newStoryOf(t, "feature", "outsider")
	}
	t.Run("member", func(t *testing.T) {
		r, parent, member, _ := newWorld(t)
		r.d.perform = performerReports(parent.ID, []agentartifact.Change{
			{Op: "add-tags", Story: member.ID, Tags: []string{"order:3"}},
			{Op: "create-child", Title: "sibling filed", Category: "feature"},
		}, "")
		if res := readyCall(t, parent.ID)[0]; res.Outcome != verb.ReadyMarked {
			t.Fatalf("%+v", res)
		}
		if !carries(tagsOf(t, member.ID), "order:3") {
			t.Errorf("the parent_id member must be targetable: %v", tagsOf(t, member.ID))
		}
		child, ok := titled(membersOf(t, statusOf(t, parent.ID)), "sibling filed")
		if !ok || child.ParentID != parent.ID {
			t.Errorf("a child of a non-epic container is a parent_id member: %+v", child)
		}
	})
	t.Run("outsider", func(t *testing.T) {
		r, parent, _, outsider := newWorld(t)
		r.d.perform = performerReports(parent.ID, []agentartifact.Change{{Op: "add-tags", Story: outsider.ID, Tags: []string{"order:3"}}}, "")
		res := readyCall(t, parent.ID)[0]
		if res.Outcome != verb.ReadyNotAccepted || !strings.Contains(res.Reason, "neither the container") {
			t.Fatalf("want the outsider refused, got %+v", res)
		}
		if carries(tagsOf(t, outsider.ID), "order:3") {
			t.Error("the outsider must be untouched")
		}
	})
}

// titleRejectingReviewer refuses only the draft with the given title.
type titleRejectingReviewer struct{ title string }

func (r titleRejectingReviewer) ReviewCreate(_ context.Context, d verb.CreateDraft) (verb.GateDecision, error) {
	if d.Title == r.title {
		return verb.GateDecision{Gated: true, Accept: false, Skill: "create-gate", Notes: "structure refused"}, nil
	}
	return verb.GateDecision{Gated: true, Accept: true, Skill: "create-gate"}, nil
}

// AC3/AC4 all-or-nothing: with two reported children where only the second is
// refused by the create review, the first is not stored either, no tag is
// applied, and the performer leaves no story_updated or story_created row.
func TestReportedBatchWithOnlyTheSecondChildRefusedAppliesNothing(t *testing.T) {
	w := newChangesWorld(t)
	verb.SetCreateReviewer(titleRejectingReviewer{title: "second"})
	t.Cleanup(func() { verb.SetCreateReviewer(nil) })
	w.r.d.perform = performerReports(w.epic.ID, []agentartifact.Change{
		{Op: "add-tags", Story: w.a.ID, Tags: []string{"order:2"}},
		{Op: "create-child", Title: "first", Category: "feature"},
		{Op: "create-child", Title: "second", Category: "feature"},
	}, "")
	res := readyCall(t, w.epic.ID)[0]
	if res.Outcome != verb.ReadyNotAccepted || !strings.Contains(res.Reason, "second") {
		t.Fatalf("want the round failed by the create review of the second child, got %+v", res)
	}
	for _, title := range []string{"first", "second"} {
		if _, ok := titled(listAll(t), title); ok {
			t.Errorf("child %q must not exist", title)
		}
	}
	if carries(tagsOf(t, w.a.ID), "order:2") {
		t.Error("no tag may be applied when a child is refused")
	}
	for _, id := range []string{w.epic.ID, w.a.ID} {
		for _, kind := range []string{ledger.KindStoryUpdated, ledger.KindStoryCreated} {
			if rows := rowsBy(t, id, kind, "ready-reviewer"); len(rows) != 0 {
				t.Errorf("%s: no %s row from the performer may exist, got %d", id, kind, len(rows))
			}
		}
	}
}

// AC3 all-or-nothing under a write conflict: the second tag target changes status
// between its read and its compare-and-set write. The first target's tags are
// restored, the child is not created, and no row from the performer is left — the
// whole batch is undone, not partly applied.
func TestReportedBatchWriteConflictRollsEverythingBack(t *testing.T) {
	w := newChangesWorld(t)
	w.r.d.perform = performerReports(w.epic.ID, []agentartifact.Change{
		{Op: "add-tags", Story: w.a.ID, Tags: []string{"order:2"}},
		{Op: "add-tags", Story: w.b.ID, Tags: []string{"order:3"}},
		{Op: "create-child", Title: "late child", Category: "feature"},
	}, "")
	verb.SetAfterTagCASGetHook(func(c context.Context, id, _ string) {
		if id == w.b.ID {
			if err := verb.ForceStatus(c, id, "plan"); err != nil {
				t.Errorf("forcing the conflict: %v", err)
			}
		}
	})
	t.Cleanup(func() { verb.SetAfterTagCASGetHook(nil) })
	res := readyCall(t, w.epic.ID)[0]
	if res.Outcome != verb.ReadyNotAccepted || !strings.Contains(res.Reason, "none applied") {
		t.Fatalf("want the round failed with nothing applied, got %+v", res)
	}
	if got := tagsOf(t, w.a.ID); carries(got, "order:2") {
		t.Errorf("first target tags = %v, want the reported tag rolled back", got)
	}
	if carries(tagsOf(t, w.b.ID), "order:3") {
		t.Error("the conflicting target must not carry the reported tag")
	}
	if _, ok := titled(listAll(t), "late child"); ok {
		t.Error("the child must not be created when a tag write conflicts")
	}
	for _, id := range []string{w.epic.ID, w.a.ID, w.b.ID} {
		for _, kind := range []string{ledger.KindStoryUpdated, ledger.KindStoryCreated} {
			if rows := rowsBy(t, id, kind, "ready-reviewer"); len(rows) != 0 {
				t.Errorf("%s: no %s row from the performer may exist after the rollback, got %d", id, kind, len(rows))
			}
		}
	}
}

// AC4: a child the create review refuses is refused with nothing applied to the
// existing stories (every child is reviewed before any tag or child is stored).
func TestReportedChildRefusedByTheCreateReviewAppliesNothing(t *testing.T) {
	w := newChangesWorld(t)
	verb.SetCreateReviewer(rejectingCreateReviewer{})
	t.Cleanup(func() { verb.SetCreateReviewer(nil) })
	w.r.d.perform = performerReports(w.epic.ID, []agentartifact.Change{
		{Op: "add-tags", Story: w.epic.ID, Tags: []string{"lane:trunk"}},
		{Op: "create-child", Title: "refused", Category: "feature"},
	}, "")
	res := readyCall(t, w.epic.ID)[0]
	if res.Outcome != verb.ReadyNotAccepted || !strings.Contains(res.Reason, "refused") {
		t.Fatalf("want the round failed by the create review, got %+v", res)
	}
	if carries(tagsOf(t, w.epic.ID), "lane:trunk") {
		t.Error("no tag may be applied when the child is refused")
	}
	if _, ok := titled(listAll(t), "refused"); ok {
		t.Error("the refused child must not exist")
	}
}

type rejectingCreateReviewer struct{}

func (rejectingCreateReviewer) ReviewCreate(context.Context, verb.CreateDraft) (verb.GateDecision, error) {
	return verb.GateDecision{Gated: true, Accept: false, Skill: "create-gate", Notes: "structure refused"}, nil
}

// AC5 (story ready): a reject carrying changes applies them within the limits and
// records the reject; the status does not change and the story is not marked.
func TestRejectWithChangesUnderStoryReady(t *testing.T) {
	w := newChangesWorld(t)
	w.r.d.perform = performerReports(w.epic.ID, []agentartifact.Change{{Op: "add-tags", Story: w.epic.ID, Tags: []string{"lane:trunk"}}}, "premise wrong: no such lane")
	res := readyCall(t, w.epic.ID)[0]
	if res.Outcome != verb.ReadyNotAccepted || !strings.Contains(res.Reason, "premise wrong") {
		t.Fatalf("want a not-accepted round with the notes, got %+v", res)
	}
	if !carries(tagsOf(t, w.epic.ID), "lane:trunk") {
		t.Errorf("the reject's changes must be applied: %v", tagsOf(t, w.epic.ID))
	}
	if got := statusOf(t, w.epic.ID).Status; got != "backlog" || hasMark(t, w.epic.ID) {
		t.Errorf("status %q marked=%v, want backlog and unmarked", got, hasMark(t, w.epic.ID))
	}
	if rows := ledgerRows(t, w.epic.ID, ledger.KindPerformerReject); len(rows) != 1 || !strings.Contains(rows[0].Body, "premise wrong") {
		t.Errorf("one performer_reject row with the notes expected: %+v", rows)
	}
}

// AC5 (engaged): the same reject parks the story blocked with the notes, the
// changes applied.
func TestRejectWithChangesParksTheEngagedStory(t *testing.T) {
	w := newChangesWorld(t)
	w.r.d.perform = performerReports(w.epic.ID, []agentartifact.Change{{Op: "add-tags", Story: w.epic.ID, Tags: []string{"lane:trunk"}}}, "premise wrong: no such lane")
	if _, err := dispatchRaw(t, "story-set", map[string]any{"id": w.epic.ID, "status": "ready"}); err != nil {
		t.Fatalf("a premise reject must park, not refuse: %v", err)
	}
	if got := statusOf(t, w.epic.ID); got.Status != "blocked" || !carries(got.Tags, "lane:trunk") {
		t.Errorf("want blocked with lane:trunk applied, got %q %v", got.Status, got.Tags)
	}
	found := false
	for _, e := range ledgerRows(t, w.epic.ID, ledger.KindComment) {
		found = found || strings.Contains(e.Body, "premise wrong")
	}
	if !found {
		t.Error("the park must carry the notes")
	}
}

// AC5: a reject whose changes break a limit applies none of them, and the notes
// say so.
func TestRejectWithRefusedChangesRecordsTheRefusal(t *testing.T) {
	w := newChangesWorld(t)
	w.r.d.perform = performerReports(w.epic.ID, []agentartifact.Change{{Op: "add-tags", Story: w.x.ID, Tags: []string{"order:1"}}}, "premise wrong")
	res := readyCall(t, w.epic.ID)[0]
	if res.Outcome != verb.ReadyNotAccepted || !strings.Contains(res.Reason, "premise wrong") || !strings.Contains(res.Reason, "neither the container") {
		t.Fatalf("the reject notes must name the refused change, got %+v", res)
	}
	if carries(tagsOf(t, w.x.ID), "order:1") {
		t.Error("nothing may be applied")
	}
}

// AC6: a step that declares no apply_changes refuses a report, naming the missing
// declaration, and applies nothing; on both routes.
func TestChangesOnAStepWithoutApplyChangesAreRefused(t *testing.T) {
	r := newReadyRig(t, "ready", readyReviewers)
	a := newStoryOf(t, "feature", "a")
	r.d.perform = performerReports(a.ID, []agentartifact.Change{{Op: "add-tags", Story: a.ID, Tags: []string{"order:1"}}}, "")
	res := readyCall(t, a.ID)[0]
	if res.Outcome != verb.ReadyNotAccepted || !strings.Contains(res.Reason, "declares no apply_changes") {
		t.Fatalf("want a refusal naming the missing declaration, got %+v", res)
	}
	if carries(tagsOf(t, a.ID), "order:1") {
		t.Error("nothing may be applied")
	}
	_, err := dispatchRaw(t, "story-set", map[string]any{"id": a.ID, "status": "plan"})
	if err == nil || !strings.Contains(err.Error(), "declares no apply_changes") {
		t.Fatalf("the engaged transition must be refused naming the missing declaration, got %v", err)
	}
	if got := statusOf(t, a.ID); got.Status != "backlog" || carries(got.Tags, "order:1") {
		t.Errorf("the refused transition must change nothing: %q %v", got.Status, got.Tags)
	}
}

// AC2: a story created by hand keeps its story_created row: no actor, no payload.
func TestCLICreateKeepsItsStoryCreatedRow(t *testing.T) {
	wire(t)
	var it workitem.Item
	if err := json.Unmarshal(call(t, "story-create", map[string]any{"title": "by hand", "category": "feature"}), &it); err != nil {
		t.Fatal(err)
	}
	rows := ledgerRows(t, it.ID, ledger.KindStoryCreated)
	var payload map[string]any
	if len(rows) == 1 && len(rows[0].Payload) > 0 {
		_ = json.Unmarshal(rows[0].Payload, &payload)
	}
	// Actor-less and sourceless — the payload carries the story's definition and
	// initial status for the fold (sty_d84be8bc), and no source.
	if len(rows) != 1 || rows[0].Actor != "" || payload["source"] != nil || payload["title"] != "by hand" || payload["status"] != "backlog" {
		t.Errorf("the CLI story_created row must stay actor-less and sourceless, carrying the definition: %+v", rows)
	}
}
