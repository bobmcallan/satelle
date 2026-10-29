package verb_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// releaseWF is the container route of a repo that releases on demand: the epic
// waits at ready, merges at merging (after_children = integrated), releases at
// release and closes at done. The recover edge release → merging is the way back
// a later release takes, so a subset release leaves the rest eligible.
func releaseWF() map[string]string {
	return routeHalves(
		`["*"]
obligations = ["raised", "planned", "coded", "integrated", "closed"]
park = { state = "blocked" }
cancel = { state = "cancelled" }

[epic-parent]
obligations = ["raised", "ready", "merged", "parent-released", "children-resolved"]
park = { state = "blocked" }
cancel = { state = "cancelled" }
recover = [{ step = "merging", from = ["release"] }]
`,
		`[raised]
status = "backlog"
start = true

[planned]
status = "plan"
agent = "executor"
requires = ["raised"]

[coded]
status = "in_progress"
agent = "executor"
requires = ["planned"]

[integrated]
status = "integration"
agent = "executor"
requires = ["coded"]

[closed]
status = "done"
terminal = true
requires = ["integrated"]

[ready]
status = "ready"
waits_on_children = true
schedule = "parallel"
requires = ["raised"]

[merged]
status = "merging"
agent = "executor"
after_children = "integrated"
requires = ["ready"]

[parent-released]
status = "release"
agent = "executor"
requires = ["merged"]

[children-resolved]
status = "done"
agent = "reviewer"
terminal = true
requires = ["parent-released"]
`)
}

// releaseProbe stands in for the merging step's performer: it records the
// container's release document as the performer would read it.
type releaseProbe struct {
	t    *testing.T
	epic string
	seen []string
}

func (p *releaseProbe) DispatchExecutor(_ context.Context, it workitem.Item, to string) (verb.DispatchResult, error) {
	if it.ID == p.epic && to == "merging" {
		p.seen = append(p.seen, releaseDocBody(p.t, p.epic))
	}
	return verb.DispatchResult{}, nil
}

func releaseDocBody(t *testing.T, id string) string {
	t.Helper()
	raw, err := dispatchRaw(t, "story-doc-get", map[string]any{"story_id": id, "name": "release"})
	if err != nil {
		return ""
	}
	var d struct {
		Body string `json:"body"`
	}
	json.Unmarshal(raw, &d)
	return d.Body
}

// releaseScene is an epic waiting at ready over two children, a and b, both in
// backlog. The working directory is treeA; treeB is free for a child.
type releaseScene struct {
	db           *store.DB
	treeA, treeB string
	epic, a, b   workitem.Item
	probe        *releaseProbe
}

func releaseScenario(t *testing.T) releaseScene {
	t.Helper()
	s := releaseScene{}
	s.treeA, s.treeB = twoWorktrees(t)
	chdir(t, s.treeA)
	withWiring(t)
	s.db = wireWithWorkflowsStore(t, releaseWF())
	verb.SetStoryDir(t.TempDir())
	verb.SetEngagementMode(config.Config{Engagement: config.EngagementConfig{Parallel: config.ParallelEpic}})
	s.epic = waveEpic(t)
	s.a = acChild(t, s.epic, "fix")
	s.b = acChild(t, s.epic, "fix")
	acceptEpicReadiness(t, s.db, s.epic.ID, "backlog", "ready")
	moveTo(t, s.epic.ID, "ready")
	s.probe = &releaseProbe{t: t, epic: s.epic.ID}
	verb.SetExecutorDispatcher(s.probe)
	return s
}

// integrate walks a child to integration from tree, then returns to treeA.
func (s releaseScene) integrate(t *testing.T, tree string, c workitem.Item) {
	t.Helper()
	chdir(t, tree)
	for _, st := range []string{"plan", "in_progress", "integration"} {
		moveTo(t, c.ID, st)
	}
	chdir(t, s.treeA)
}

func releaseSet(t *testing.T, id, status string, release ...string) (workitem.Item, error) {
	t.Helper()
	raw, err := dispatchRaw(t, "story-set", map[string]any{"id": id, "status": status, "release": release})
	var it workitem.Item
	if err == nil {
		json.Unmarshal(raw, &it)
	}
	return it, err
}

func readinessRows(t *testing.T, db *store.DB, id string) int {
	t.Helper()
	n := 0
	for _, k := range []string{ledger.KindReadinessAccept, ledger.KindReadinessReuse} {
		rows, err := db.Ledger.ListByStory(context.Background(), id, k)
		if err != nil {
			t.Fatal(err)
		}
		n += len(rows)
	}
	return n
}

func wantNames(t *testing.T, doc string, in, out []workitem.Item) {
	t.Helper()
	for _, c := range in {
		if !strings.Contains(doc, c.ID) {
			t.Errorf("release document must name %s: %q", c.ID, doc)
		}
	}
	for _, c := range out {
		if strings.Contains(doc, c.ID) {
			t.Errorf("release document must not name %s: %q", c.ID, doc)
		}
	}
}

// AC1: --release all enters the step over a member that has not discharged the
// obligation, and selects just the members that have; the performer reads them.
func TestReleaseAllEntersWithTheDischargedMembers(t *testing.T) {
	s := releaseScenario(t)
	s.integrate(t, s.treeB, s.a)

	if _, err := releaseSet(t, s.epic.ID, "merging", "all"); err != nil {
		t.Fatalf("--release all must enter the step once a member has discharged it: %v", err)
	}
	if got := statusOf(t, s.epic.ID).Status; got != "merging" {
		t.Fatalf("epic must be merging, got %q", got)
	}
	if len(s.probe.seen) != 1 {
		t.Fatalf("the performer must run once, ran %d", len(s.probe.seen))
	}
	wantNames(t, s.probe.seen[0], []workitem.Item{s.a}, []workitem.Item{s.b})
}

// AC1: --release <ids> enters the step for just those members.
func TestReleaseNamedSubsetEnters(t *testing.T) {
	s := releaseScenario(t)
	s.integrate(t, s.treeB, s.a)

	if _, err := releaseSet(t, s.epic.ID, "merging", s.a.ID); err != nil {
		t.Fatalf("--release naming a discharged member must enter the step: %v", err)
	}
	if len(s.probe.seen) != 1 {
		t.Fatalf("the performer must run once, ran %d", len(s.probe.seen))
	}
	wantNames(t, s.probe.seen[0], []workitem.Item{s.a}, []workitem.Item{s.b})
	if got := statusOf(t, s.b.ID).Status; got != "backlog" {
		t.Errorf("a member left out keeps its status, got %q", got)
	}
}

// AC1: a named id that is not a member, or has not discharged the obligation, is
// refused by name; --release all with nothing discharged is refused; nothing moves.
func TestReleaseRefusesWhatItCannotRelease(t *testing.T) {
	s := releaseScenario(t)

	if _, err := releaseSet(t, s.epic.ID, "merging", "all"); err == nil || !strings.Contains(err.Error(), "nothing to release") {
		t.Fatalf("--release all with no discharged member must refuse: %v", err)
	}

	s.integrate(t, s.treeB, s.a)
	outsider := mkEpicStory(t, map[string]any{"category": "fix"})
	_, err := releaseSet(t, s.epic.ID, "merging", s.a.ID, outsider.ID)
	wantContains(t, err, outsider.ID, "not a member")

	_, err = releaseSet(t, s.epic.ID, "merging", s.a.ID, s.b.ID)
	wantContains(t, err, s.b.ID, "backlog", `has not discharged "integrated"`)

	_, err = releaseSet(t, s.epic.ID, "merging", "all", s.a.ID)
	wantContains(t, err, "not both")

	if got := statusOf(t, s.epic.ID).Status; got != "ready" {
		t.Errorf("a refused release leaves the epic at ready, got %q", got)
	}
	if len(s.probe.seen) != 0 {
		t.Errorf("a refused release runs no performer, ran %d", len(s.probe.seen))
	}
}

// AC1: --release on a step that does not wait on children is refused, as is a
// --release with no status change.
func TestReleaseRefusedOffAnAfterChildrenStep(t *testing.T) {
	s := releaseScenario(t)
	chdir(t, s.treeB)
	_, err := releaseSet(t, s.a.ID, "plan", "all")
	wantContains(t, err, "does not wait on children")

	_, err = dispatchRaw(t, "story-set", map[string]any{"id": s.epic.ID, "release": []string{"all"}})
	wantContains(t, err, "--status")
}

// AC1: a subset on a route with no way back into the step is refused, so a
// partial release cannot strand the members it leaves out.
func TestReleaseSubsetRefusedWithoutAWayBack(t *testing.T) {
	s := afterChildrenScenario(t)

	_, err := releaseSet(t, s.epic.ID, "merging", s.coded.ID)
	wantContains(t, err, "no way back into merging")
	_, err = releaseSet(t, s.epic.ID, "merging", "all")
	wantContains(t, err, "no way back into merging")
	if got := statusOf(t, s.epic.ID).Status; got != "ready" {
		t.Errorf("a refused release leaves the epic at ready, got %q", got)
	}
}

// AC2: two releases in sequence. The first releases a; b keeps its status; the
// epic returns from release to merging by the recover edge, never through ready,
// and the second release names b alone.
func TestReleaseTwiceReturnsByTheRecoverEdge(t *testing.T) {
	s := releaseScenario(t)
	s.integrate(t, s.treeB, s.a)
	readied := readinessRows(t, s.db, s.epic.ID)

	if _, err := releaseSet(t, s.epic.ID, "merging", s.a.ID); err != nil {
		t.Fatalf("first release: %v", err)
	}
	moveTo(t, s.epic.ID, "release")
	if got := statusOf(t, s.b.ID).Status; got != "backlog" {
		t.Fatalf("a member left out keeps its status through the release, got %q", got)
	}

	chdir(t, s.treeB)
	moveTo(t, s.a.ID, "done")
	s.integrate(t, s.treeB, s.b)

	if _, err := releaseSet(t, s.epic.ID, "merging", s.b.ID); err != nil {
		t.Fatalf("second release must take the recover edge release → merging: %v", err)
	}
	if got := statusOf(t, s.epic.ID).Status; got != "merging" {
		t.Fatalf("epic must be merging again, got %q", got)
	}
	if len(s.probe.seen) != 2 {
		t.Fatalf("the performer must run once per release, ran %d", len(s.probe.seen))
	}
	wantNames(t, s.probe.seen[1], []workitem.Item{s.b}, []workitem.Item{s.a})
	if got := readinessRows(t, s.db, s.epic.ID); got != readied {
		t.Errorf("no readiness round may run between the releases: %d readiness rows, had %d", got, readied)
	}
}

// AC2: after a partial release, entering merging with no --release once every
// member has discharged selects every member.
func TestReleaseFullEntryAfterAPartialOneNamesEveryMember(t *testing.T) {
	s := releaseScenario(t)
	s.integrate(t, s.treeB, s.a)
	if _, err := releaseSet(t, s.epic.ID, "merging", s.a.ID); err != nil {
		t.Fatalf("first release: %v", err)
	}
	moveTo(t, s.epic.ID, "release")

	_, err := dispatchRaw(t, "story-set", map[string]any{"id": s.epic.ID, "status": "merging"})
	wantContains(t, err, s.b.ID, `have not discharged "integrated"`)

	chdir(t, s.treeB)
	moveTo(t, s.a.ID, "done")
	s.integrate(t, s.treeB, s.b)
	moveTo(t, s.epic.ID, "merging")
	if len(s.probe.seen) != 2 {
		t.Fatalf("the performer must run once per entry, ran %d", len(s.probe.seen))
	}
	wantNames(t, s.probe.seen[1], []workitem.Item{s.a, s.b}, nil)
}

// AC3: after a partial release the epic does not close while a member is open.
func TestReleasePartialDoesNotCloseTheEpic(t *testing.T) {
	s := releaseScenario(t)
	s.integrate(t, s.treeB, s.a)
	if _, err := releaseSet(t, s.epic.ID, "merging", s.a.ID); err != nil {
		t.Fatalf("release: %v", err)
	}
	moveTo(t, s.epic.ID, "release")

	err := setStatus(t, s.epic.ID, "done")
	wantContains(t, err, s.b.ID, "must be done or cancelled")
	if got := statusOf(t, s.epic.ID).Status; got != "release" {
		t.Errorf("a refused close leaves the epic at release, got %q", got)
	}
}
