package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/remotedef"
	"github.com/bobmcallan/satelle/internal/remotenode"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// stubHost is the remote's story runner: it records the stories it was asked to
// run and, on the hosted side only, moves each to the status set for it.
type stubHost struct {
	calls  []string
	hosted map[string]string // story -> status the hosted side leaves it at
	err    error
}

func (h *stubHost) Run(_ context.Context, ho remotedef.Handoff, _ string) error {
	h.calls = append(h.calls, ho.Story)
	return h.err
}

// stubSync is the workstate sync: a pull copies the hosted status of each id into
// the local store; a push is only counted.
type stubSync struct {
	items  *workitem.Store
	host   *stubHost
	pulls  [][]string
	pushes int
}

func (s *stubSync) Pull(ctx context.Context, _ *cobra.Command, ids []string) error {
	s.pulls = append(s.pulls, append([]string(nil), ids...))
	for _, id := range ids {
		if st, ok := s.host.hosted[id]; ok {
			if _, err := s.items.SetStatus(ctx, id, st, time.Now()); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *stubSync) Push(context.Context, *cobra.Command, []string) error {
	s.pushes++
	return nil
}

// stubNode answers each StoryReport with the next queued reply and records what
// it was asked.
type stubNode struct {
	replies []remotenode.StoryReportReply
	asked   []string // "story@since"
	err     error
}

func (n *stubNode) StoryReport(_ context.Context, story string, since int64) (remotenode.StoryReportReply, error) {
	n.asked = append(n.asked, fmt.Sprintf("%s@%d", story, since))
	if n.err != nil {
		return remotenode.StoryReportReply{}, n.err
	}
	if len(n.replies) == 0 {
		return remotenode.StoryReportReply{Now: since + 1}, nil
	}
	r := n.replies[0]
	n.replies = n.replies[1:]
	return r, nil
}

// stubLocal declares a fixed set of statuses for every story's local route.
type stubLocal map[string]bool

func (l stubLocal) Declares(_ context.Context, _, status string) (bool, string, error) {
	return l[status], "default", nil
}

// Engages: the stub route has no engaging states, so no segment holds a seat.
func (l stubLocal) Engages(context.Context, string, string) (bool, string, error) {
	return false, "default", nil
}

// runRig is a repo with a store and the seams of one harness run.
type runRig struct {
	t    *testing.T
	a    *app.App
	host *stubHost
	sync *stubSync
	node *stubNode
	out  *bytes.Buffer
	plan remoteRunPlan
}

// baselineNow is the node's clock at join in the rig's fixtures.
const baselineNow = int64(1_000_000_000_000)

func newRunRig(t *testing.T, def *remotedef.Definition) *runRig {
	t.Helper()
	t.Chdir(tempRepo(t))
	a, err := app.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	r := &runRig{t: t, a: a, host: &stubHost{hosted: map[string]string{}}, node: &stubNode{}, out: &bytes.Buffer{}}
	r.sync = &stubSync{items: a.Store.Stories, host: r.host}
	rec := r.story("backlog")
	r.plan = remoteRunPlan{
		Name: "edge", RunID: "run_test", Record: rec, Def: def,
		Baseline: remotenode.Report{Now: baselineNow}, Started: time.Now(), Node: r.node,
		Stories:  storyReader{a.Store.Stories},
		Recorder: ledgerRecorder{a.Store.Ledger},
		Runner:   r.host,
		Local:    stubLocal{"backlog": true, "plan": true, "in_progress": true, "done": true, "ready": true, "review": true},
		Items:    a.Store.Stories, Ledger: a.Store.Ledger, Out: r.out,
	}
	r.plan.Pull = func(ctx context.Context, ids []string) error { return r.sync.Pull(ctx, nil, ids) }
	return r
}

func (r *runRig) story(status string) string {
	r.t.Helper()
	it, err := r.a.Store.Stories.Create(context.Background(), workitem.CreateInput{
		Kind: workitem.KindStory, Title: "run " + status, Status: status,
	}, time.Now().UTC())
	if err != nil {
		r.t.Fatal(err)
	}
	return it.ID
}

func (r *runRig) run(ids ...string) (remoteRunSummary, error) {
	r.t.Helper()
	r.plan.IDs = ids
	return runRemoteStories(context.Background(), r.plan)
}

// append writes one ledger row for story at the node-clock offset after join.
func (r *runRig) append(story, kind string, payload map[string]any, after time.Duration) {
	r.t.Helper()
	b, err := json.Marshal(payload)
	if err != nil {
		r.t.Fatal(err)
	}
	at := time.Unix(0, baselineNow).Add(after)
	if _, err := r.a.Store.Ledger.Append(context.Background(), ledger.AppendInput{StoryID: story, Kind: kind, Payload: b}, at); err != nil {
		r.t.Fatal(err)
	}
}

func segmentDef(entry, exit string) *remotedef.Definition {
	return &remotedef.Definition{Segments: []remotedef.Segment{{Entry: entry, Exit: exit}}}
}

func remoteRunRows(t *testing.T, story string) []ledger.Entry {
	t.Helper()
	var out []ledger.Entry
	for _, e := range ledgerRows(t, ledger.KindRemoteRun) {
		if e.StoryID == story {
			out = append(out, e)
		}
	}
	return out
}

// fireRequests is the fire calls the stub server saw.
func fireRequests(s *stubServer) []seenRequest {
	var out []seenRequest
	for _, r := range s.requests() {
		if r.Method == http.MethodPost && strings.HasSuffix(r.Path, "/fire") {
			out = append(out, r)
		}
	}
	return out
}

// AC2/AC3: the selection is exactly the ids given; the definition's contract
// decides each; a selected story outside it is reported refused and not run, and
// an unselected story at the entry status is not run. The same command works for
// any segment: nothing here names a status.
func TestRemoteRunOffersEachSelectedStoryThroughTheContract(t *testing.T) {
	for _, seg := range []struct{ entry, exit string }{{"backlog", "plan"}, {"ready", "review"}} {
		t.Run(seg.entry+"→"+seg.exit, func(t *testing.T) {
			r := newRunRig(t, segmentDef(seg.entry, seg.exit))
			a, b := r.story(seg.entry), r.story(seg.entry)
			outside := r.story("done") // selected, but not at the segment's entry
			unselected := r.story(seg.entry)
			r.host.hosted[a], r.host.hosted[b] = seg.exit, seg.exit

			sum, err := r.run(a, outside, b)
			if err != nil {
				t.Fatalf("run: %v\n%s", err, r.out)
			}
			if want := []string{a, b}; !reflect.DeepEqual(r.host.calls, want) {
				t.Errorf("stories run = %v, want %v (never %s)", r.host.calls, want, unselected)
			}
			if !strings.Contains(r.out.String(), "story "+outside+": refused:") {
				t.Errorf("the out-of-contract story is not reported refused:\n%s", r.out)
			}
			if len(sum.Stories) != 3 || sum.Stories[1].ID != outside || sum.Stories[1].Refused == "" {
				t.Errorf("summary = %+v, want the second story refused", sum.Stories)
			}
			for _, id := range []string{a, b} {
				if !strings.Contains(r.out.String(), fmt.Sprintf("story %s: left at %s; hand-back ok", id, seg.exit)) {
					t.Errorf("no hand-back line for %s:\n%s", id, r.out)
				}
			}
		})
	}
}

// storySnapshot is everything a refused run must leave alone: the story's record
// and its ledger.
func storySnapshot(t *testing.T, id string) string {
	t.Helper()
	story, err := runRoot(t, "story", "get", id)
	if err != nil {
		t.Fatalf("story get: %v\n%s", err, story)
	}
	led, err := runRoot(t, "ledger", "list", "--story", id)
	if err != nil {
		t.Fatalf("ledger list: %v\n%s", err, led)
	}
	return story + "\n--\n" + led
}

// createRemoteEnteringAt scaffolds a remote whose segment enters at entry.
func createRemoteEnteringAt(t *testing.T, name, entry string) {
	t.Helper()
	if out, err := runRoot(t, "remote", "create", name); err != nil {
		t.Fatalf("create: %v\n%s", err, out)
	}
	path := filepath.Join(remotedef.Dir(remoteDataDir(), name), "remote.toml")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	old := "entry = \"backlog\"\nexit = \"done\""
	if !strings.Contains(string(b), old) {
		t.Fatalf("scaffold lacks the default segment:\n%s", b)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(string(b), old, "entry = \""+entry+"\"\nexit = \"done\"", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestScaffoldedRemoteIsNotRefusedBySeatRule: a remote exactly as `remote create`
// writes it, against the repo's default local route, accepts a story at its
// segment's entry and the seat rule leaves its hand-off alone.
func TestScaffoldedRemoteIsNotRefusedBySeatRule(t *testing.T) {
	repo := tempRepo(t)
	t.Chdir(repo)
	if out, err := runRoot(t, "remote", "create", "fresh"); err != nil {
		t.Fatalf("create: %v\n%s", err, out)
	}
	def, err := loadRemoteDef("fresh")
	if err != nil {
		t.Fatal(err)
	}
	if len(def.Segments) != 1 {
		t.Fatalf("want the scaffold's one default segment, got %+v", def.Segments)
	}
	seg := def.Segments[0]

	id := newStory(t, seg.Entry)
	ctx := context.Background()
	a, err := app.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()

	if _, err := def.Accepts(seg.Entry, ""); err != nil {
		t.Errorf("the scaffold's segment refuses a story at its own entry %s: %v", seg.Entry, err)
	}
	local := localRoute{a.Store.Stories, a.Store.DocIndex}
	reason, err := remotedef.SeatReason(ctx, local, id, seg, seg.Entry)
	if err != nil || reason != "" {
		t.Errorf("SeatReason = %q, %v; the scaffold's default segment (%s → %s) must hold no seat on the default local route", reason, err, seg.Entry, seg.Exit)
	}
}

// TestRemoteRunSelectsAcceptingRemote: of two defined remotes only the one whose
// segment takes the story's status is selected and printed.
func TestRemoteRunSelectsAcceptingRemote(t *testing.T) {
	remoteWithTriggers(t) // edge enters at backlog
	createRemoteEnteringAt(t, "other", "in_progress")
	srv := newStubServer(t, "")
	id := newStory(t, "backlog")

	out, err := runRoot(t, "remote", "run", id, "--server", srv.ts.URL)
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	if !strings.Contains(out, "remote edge: segment backlog → done") {
		t.Errorf("the accepting remote is not printed:\n%s", out)
	}
	if strings.Contains(out, "remote other") {
		t.Errorf("a remote whose segment does not take the story was selected:\n%s", out)
	}
	if n := len(fireRequests(srv)); n != 0 {
		t.Errorf("without --remote-id nothing is fired, got %d fire request(s)", n)
	}
}

// TestRemoteRunNoRemote: no remote defined, or none accepting the story, is
// answered and changes nothing.
func TestRemoteRunNoRemote(t *testing.T) {
	for _, c := range []struct {
		name   string
		define bool
	}{{"no remotes defined", false}, {"no remote accepts the story", true}} {
		t.Run(c.name, func(t *testing.T) {
			if c.define {
				remoteWithTriggers(t) // edge enters at backlog; the story is at plan
			} else {
				repo := tempRepo(t)
				t.Chdir(repo)
				t.Setenv(hosted.SessionTokenEnv, "stub-token")
			}
			srv := newStubServer(t, "")
			id := newStory(t, "plan")
			before := storySnapshot(t, id)

			out, err := runRoot(t, "remote", "run", id, "--server", srv.ts.URL)
			if err == nil || !strings.Contains(err.Error(), "No remote; either configure one, or stop") {
				t.Fatalf("want the no-remote answer, got %v\n%s", err, out)
			}
			if after := storySnapshot(t, id); after != before {
				t.Errorf("the story changed:\nbefore: %s\nafter: %s", before, after)
			}
			if n := len(fireRequests(srv)); n != 0 {
				t.Errorf("got %d fire request(s)", n)
			}
		})
	}
}

// AC4: only the hosted side changes the story. The hand-back sees the pulled
// status, so no remote_handback row is written; without the pull it would be.
func TestRemoteRunPullsBeforeTheHandbackCheck(t *testing.T) {
	r := newRunRig(t, segmentDef("backlog", "plan"))
	id := r.story("backlog")
	r.host.hosted[id] = "plan"
	if _, err := r.run(id); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.out.String(), "story "+id+": left at plan; hand-back ok") {
		t.Errorf("the hand-back did not see the pulled status:\n%s", r.out)
	}
	if n := len(handbackRows(t, id)); n != 0 {
		t.Errorf("a false remote_handback row was written (%d)", n)
	}

	// The control: the same run with nothing brought home reads the stale status.
	r2 := newRunRig(t, segmentDef("backlog", "plan"))
	id2 := r2.story("backlog")
	r2.host.hosted[id2] = "plan"
	r2.plan.Pull = func(context.Context, []string) error { return nil }
	if _, err := r2.run(id2); err != nil {
		t.Fatal(err)
	}
	if n := len(handbackRows(t, id2)); n != 1 {
		t.Errorf("without the pull the story reads as left at backlog: want one remote_handback row, got %d\n%s", n, r2.out)
	}
}

// AC8: the harness pulls exactly the run's stories, one scoped pull each, and
// pushes nothing.
func TestRemoteRunPullsTheRunsStoriesAndPushesNothing(t *testing.T) {
	r := newRunRig(t, segmentDef("backlog", "plan"))
	a, b := r.story("backlog"), r.story("backlog")
	if _, err := r.run(a, b); err != nil {
		t.Fatal(err)
	}
	if want := [][]string{{a}, {b}}; !reflect.DeepEqual(r.sync.pulls, want) {
		t.Errorf("pulls = %v, want %v", r.sync.pulls, want)
	}
	if r.sync.pushes != 0 {
		t.Errorf("the harness pushed %d time(s) onto the node", r.sync.pushes)
	}
}

// AC5/AC6: per story the report names the status, the hand-back, the reviewers'
// verdicts since the node joined and the session location the node returned; one
// remote_run row on the story of record carries the same.
func TestRemoteRunReportsAndRecordsEachStory(t *testing.T) {
	r := newRunRig(t, segmentDef("backlog", "plan"))
	id := r.story("backlog")
	r.host.hosted[id] = "plan"
	verdict := func(kind, skill string, after time.Duration) {
		r.append(id, kind, map[string]any{"from": "backlog", "to": "plan", "skill": skill}, after)
	}
	verdict(ledger.KindReviewReject, "before-the-run", -time.Hour) // outside the window
	verdict(ledger.KindReviewReject, "story-plan-review", time.Second)
	verdict(ledger.KindReviewAccept, "story-architecture-review", 2*time.Second)
	session := "/node/logs/dispatch/dispatch-planner-1000000000001-" + id + ".log"
	r.node.replies = []remotenode.StoryReportReply{{Now: baselineNow + 10, Sessions: []string{session}}}

	sum, err := r.run(id)
	if err != nil {
		t.Fatal(err)
	}
	out := r.out.String()
	for _, want := range []string{
		"story " + id + ": left at plan; hand-back ok",
		"story " + id + ": verdict review_reject story-plan-review (backlog→plan)",
		"story " + id + ": verdict review_accept story-architecture-review (backlog→plan)",
		"session " + id + ": " + session,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "before-the-run") {
		t.Errorf("a verdict from before the node joined is reported:\n%s", out)
	}

	if err := recordRemoteRun(context.Background(), r.a.Store.Ledger, r.plan.Record, sum); err != nil {
		t.Fatal(err)
	}
	rows := remoteRunRows(t, r.plan.Record)
	if len(rows) != 1 {
		t.Fatalf("remote_run rows on the story of record = %d, want 1", len(rows))
	}
	var got remoteRunSummary
	if err := json.Unmarshal(rows[0].Payload, &got); err != nil {
		t.Fatal(err)
	}
	st := got.Stories[0]
	if st.ID != id || st.Status != "plan" || !st.InContract || !st.LocalKnown || len(st.Verdicts) != 2 ||
		st.Verdicts[0].Kind != ledger.KindReviewReject || st.Verdicts[1].Kind != ledger.KindReviewAccept {
		t.Errorf("row story = %+v, want %s left at plan in contract and route with the two in-window verdicts", st, id)
	}
	if !reflect.DeepEqual(st.Sessions, []string{session}) {
		t.Errorf("row sessions = %v, want exactly the path the node returned", st.Sessions)
	}
}

// AC6: each story's StoryReport is asked from the node's clock — the join
// report's for the first, the previous reply's for the next.
func TestRemoteRunTakesSinceFromTheNodeClock(t *testing.T) {
	r := newRunRig(t, segmentDef("backlog", "plan"))
	a, b := r.story("backlog"), r.story("backlog")
	r.node.replies = []remotenode.StoryReportReply{{Now: baselineNow + 500}, {Now: baselineNow + 900}}
	if _, err := r.run(a, b); err != nil {
		t.Fatal(err)
	}
	want := []string{fmt.Sprintf("%s@%d", a, baselineNow), fmt.Sprintf("%s@%d", b, baselineNow+500)}
	if !reflect.DeepEqual(r.node.asked, want) {
		t.Errorf("story reports asked = %v, want %v", r.node.asked, want)
	}
}

// AC9: the join report's hashes are the baseline; a changed, an added and a
// removed path are named for the story during which they happened, and the next
// story is measured from the new map.
func TestRemoteRunReportsAChangeToTheStartConfiguration(t *testing.T) {
	r := newRunRig(t, segmentDef("backlog", "plan"))
	r.plan.Baseline.Hashes = map[string]string{"skills/a.md": "1", "skills/gone.md": "1", "workflows/w.toml": "1"}
	a, b := r.story("backlog"), r.story("backlog")
	after := map[string]string{"skills/a.md": "2", "skills/new.md": "1", "workflows/w.toml": "1"}
	r.node.replies = []remotenode.StoryReportReply{{Now: baselineNow + 1, Hashes: after}, {Now: baselineNow + 2, Hashes: after}}

	sum, err := r.run(a, b)
	if err != nil {
		t.Fatal(err)
	}
	want := "gap: start configuration changed during " + a + ": skills/a.md, skills/gone.md, skills/new.md"
	if !strings.Contains(r.out.String(), want) {
		t.Errorf("output lacks %q:\n%s", want, r.out)
	}
	if strings.Count(r.out.String(), "gap:") != 1 {
		t.Errorf("the second story changed nothing and must report no gap:\n%s", r.out)
	}
	if !reflect.DeepEqual(sum.Stories[0].Gap, []string{"skills/a.md", "skills/gone.md", "skills/new.md"}) || len(sum.Stories[1].Gap) != 0 {
		t.Errorf("gap[] = %v / %v", sum.Stories[0].Gap, sum.Stories[1].Gap)
	}
}

// AC9: a run whose node changed nothing — with a baseline taken after the
// preflight — reports no gap.
func TestRemoteRunWithIdenticalHashesReportsNoGap(t *testing.T) {
	r := newRunRig(t, segmentDef("backlog", "plan"))
	same := map[string]string{"skills/a.md": "1"}
	r.plan.Baseline.Hashes = same
	id := r.story("backlog")
	r.node.replies = []remotenode.StoryReportReply{{Now: baselineNow + 1, Hashes: same}}
	sum, err := r.run(id)
	if err != nil || strings.Contains(r.out.String(), "gap:") || len(sum.Stories[0].Gap) != 0 {
		t.Errorf("err = %v, gap = %v, want none:\n%s", err, sum.Stories[0].Gap, r.out)
	}
}

// AC7: spend is summed from the pulled ledger's usage rows for the run's stories
// since the node joined, printed against the cap, and the run stops before the
// next story once the cap is reached.
func TestRemoteRunSpendCap(t *testing.T) {
	cost := func(r *runRig, id string, usd float64, after time.Duration) {
		r.append(id, ledger.KindAgentInvocation, map[string]any{
			"from": "backlog", "to": "plan", "agent": "planner", "skill": "plan", "cost_usd": usd,
			"usage_available": true, "tokens_in": 10, "tokens_out": 5, "tokens_total": 15,
		}, after)
	}
	capped := func(limit float64) *remotedef.Definition {
		d := segmentDef("backlog", "plan")
		d.SpendCapUSD = &limit
		return d
	}

	t.Run("under the cap", func(t *testing.T) {
		r := newRunRig(t, capped(5))
		a, b := r.story("backlog"), r.story("backlog")
		cost(r, a, 0.5, time.Second)
		cost(r, b, 0.25, time.Second)
		cost(r, a, 9, -time.Hour) // before the node joined: not this run's
		sum, err := r.run(a, b)
		if err != nil {
			t.Fatal(err)
		}
		if len(r.host.calls) != 2 || sum.Capped || sum.SpendUSD != 0.75 {
			t.Errorf("calls = %v, capped = %v, spend = %v, want both stories and $0.75", r.host.calls, sum.Capped, sum.SpendUSD)
		}
		if !strings.Contains(r.out.String(), "spend $0.7500 of $5.00 cap") {
			t.Errorf("spend against the cap is not printed:\n%s", r.out)
		}
	})

	t.Run("cap reached mid-run", func(t *testing.T) {
		r := newRunRig(t, capped(1))
		a, b, c := r.story("backlog"), r.story("backlog"), r.story("backlog")
		cost(r, a, 0.6, time.Second)
		cost(r, b, 0.6, time.Second)
		sum, err := r.run(a, b, c)
		if err != nil {
			t.Fatal(err)
		}
		if want := []string{a, b}; !reflect.DeepEqual(r.host.calls, want) {
			t.Errorf("stories run = %v, want %v: the cap is reached after the second", r.host.calls, want)
		}
		if !sum.Capped || sum.SpendUSD != 1.2 || sum.Stories[2].Skipped == "" {
			t.Errorf("summary = %+v, want capped at $1.20 with the third story skipped", sum)
		}
		if !strings.Contains(r.out.String(), "cap reached: $1.2000 of $1.00") {
			t.Errorf("the cap is not reported:\n%s", r.out)
		}
	})

	t.Run("uncapped", func(t *testing.T) {
		r := newRunRig(t, segmentDef("backlog", "plan"))
		a := r.story("backlog")
		cost(r, a, 0.5, time.Second)
		if _, err := r.run(a); err != nil || !strings.Contains(r.out.String(), "spend $0.5000 uncapped") {
			t.Errorf("err = %v; want the spend reported as uncapped:\n%s", err, r.out)
		}
	})
}

// A run error ends the run after recording what was found; it is not a refusal.
func TestRemoteRunErrorEndsTheRunButIsRecorded(t *testing.T) {
	r := newRunRig(t, segmentDef("backlog", "plan"))
	a, b := r.story("backlog"), r.story("backlog")
	r.host.err = fmt.Errorf("host unreachable")
	sum, err := r.run(a, b)
	if err == nil || !strings.Contains(err.Error(), "host unreachable") || len(r.host.calls) != 1 {
		t.Fatalf("err = %v after %v, want the run error after the first story", err, r.host.calls)
	}
	if len(sum.Stories) != 1 || sum.Stories[0].Error == "" || sum.Error == "" {
		t.Errorf("summary = %+v, want the error recorded against the first story", sum)
	}
}

// AC3: the harness names no status, workflow or repo.
func TestRemoteRunSourceNamesNoStatus(t *testing.T) {
	src, err := os.ReadFile("cmd_remote_run.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, lit := range []string{"backlog", "plan", "in_progress", "integration", "release", "done", "blocked", "cancelled"} {
		if strings.Contains(string(src), `"`+lit+`"`) {
			t.Errorf("cmd_remote_run.go names the status %q", lit)
		}
	}
}

// TestRemoteRunSignedOut: with no stored credential the answer names satelle
// login before any remote is selected, and nothing is sent.
func TestRemoteRunSignedOut(t *testing.T) {
	remoteWithTriggers(t)
	t.Setenv(hosted.SessionTokenEnv, "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	srv := newStubServer(t, "")
	id := newStory(t, "backlog")
	before := storySnapshot(t, id)

	out, err := runRoot(t, "remote", "run", id, "--remote-id", stubRemoteID, "--server", srv.ts.URL)
	if err == nil || !strings.Contains(err.Error(), "satelle login") {
		t.Fatalf("want an answer naming satelle login, got %v\n%s", err, out)
	}
	if strings.Contains(out, "remote edge") {
		t.Errorf("a remote was selected while signed out:\n%s", out)
	}
	if after := storySnapshot(t, id); after != before {
		t.Errorf("the story changed:\nbefore: %s\nafter: %s", before, after)
	}
	if n := len(srv.requests()); n != 0 {
		t.Errorf("signed out, nothing may be sent, got %d request(s)", n)
	}
}

// TestRemoteRunRefusesSeatTakingSegment: a story whose status would hold an
// engagement seat locally is refused before anything is sent.
func TestRemoteRunRefusesSeatTakingSegment(t *testing.T) {
	remoteWithTriggers(t)
	createRemoteEnteringAt(t, "other", "in_progress")
	srv := newStubServer(t, "")
	id := newStory(t, "in_progress") // other enters at in_progress, an engaging state
	before := storySnapshot(t, id)

	out, err := runRoot(t, "remote", "run", id, "--remote-id", stubRemoteID, "--server", srv.ts.URL)
	if err == nil || !strings.Contains(err.Error(), "engagement seat") {
		t.Fatalf("want a refusal naming the engagement seat, got %v\n%s", err, out)
	}
	if after := storySnapshot(t, id); after != before {
		t.Errorf("the story changed:\nbefore: %s\nafter: %s", before, after)
	}
	if n := len(fireRequests(srv)); n != 0 {
		t.Errorf("got %d fire request(s)", n)
	}
}

// TestRemoteRunFires: with --remote-id the story goes through the fire path.
func TestRemoteRunFires(t *testing.T) {
	remoteWithTriggers(t)
	srv := newStubServer(t, "")
	id := newStory(t, "backlog")

	out, err := runRoot(t, "remote", "run", id, "--remote-id", stubRemoteID, "--server", srv.ts.URL)
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	fires := fireRequests(srv)
	if len(fires) != 1 || fires[0].Body != `{"story_id":"`+id+`"}` {
		t.Fatalf("want one fire naming the story, got %+v", fires)
	}
	if !strings.Contains(out, "fired remote "+stubRemoteID) || !strings.Contains(out, "fire_1") {
		t.Errorf("the fire is not reported:\n%s", out)
	}
}

// TestRemoteRunPrintsCommand: without --remote-id the matching remote and the
// exact fire command are printed, and nothing is fired.
func TestRemoteRunPrintsCommand(t *testing.T) {
	remoteWithTriggers(t)
	srv := newStubServer(t, "")
	id := newStory(t, "backlog")

	out, err := runRoot(t, "remote", "run", id, "--server", srv.ts.URL)
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	for _, want := range []string{"remote edge: segment backlog → done", "satelle remote trigger fire <remote-id> --story " + id} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if n := len(fireRequests(srv)); n != 0 {
		t.Errorf("got %d fire request(s)", n)
	}
}

// TestRemoteRunModeIsChosenByItsArguments: --story or --tag makes the first
// argument a remote name (the harness); without either it is a story id (hand
// one story). Mixes that fit neither are refused before anything runs.
func TestRemoteRunModeIsChosenByItsArguments(t *testing.T) {
	remoteWithTriggers(t)
	srv := newStubServer(t, "")
	id := newStory(t, "backlog")

	// Harness: "absent" is read as a remote name, so the story-id answers do not appear.
	out, err := runRoot(t, "remote", "run", "absent", "--story", id, "--story-of-record", id, "--server", srv.ts.URL)
	if err == nil || !strings.Contains(err.Error(), `no remote "absent"`) {
		t.Errorf("--story: want the harness to look up the remote name, got %v\n%s", err, out)
	}
	out, err = runRoot(t, "remote", "run", "absent", "--tag", "x", "--story-of-record", id, "--server", srv.ts.URL)
	if err == nil || !strings.Contains(err.Error(), `no remote "absent"`) {
		t.Errorf("--tag: want the harness to look up the remote name, got %v\n%s", err, out)
	}

	// Hand one story: the argument is a story id and the harness is not entered.
	out, err = runRoot(t, "remote", "run", id, "--server", srv.ts.URL)
	if err != nil || !strings.Contains(out, "remote edge: segment backlog → done") {
		t.Errorf("without --story/--tag: want the story handed to the accepting remote, got %v\n%s", err, out)
	}
}

// TestRemoteRunRefusesAmbiguousMixes: --remote-id with --story or --tag, and
// --story-of-record without either, are refused with a message naming the flags,
// and nothing is sent.
func TestRemoteRunRefusesAmbiguousMixes(t *testing.T) {
	remoteWithTriggers(t)
	srv := newStubServer(t, "")
	id := newStory(t, "backlog")
	for _, c := range []struct {
		name string
		args []string
		want string
	}{
		{"remote-id with story", []string{"edge", "--story", id, "--story-of-record", id, "--remote-id", stubRemoteID}, "--remote-id"},
		{"remote-id with tag", []string{"edge", "--tag", "x", "--story-of-record", id, "--remote-id", stubRemoteID}, "--remote-id"},
		{"story-of-record alone", []string{id, "--story-of-record", id}, "--story-of-record"},
		{"harness without story-of-record", []string{"edge", "--story", id}, "--story-of-record"},
	} {
		t.Run(c.name, func(t *testing.T) {
			out, err := runRoot(t, append([]string{"remote", "run", "--server", srv.ts.URL}, c.args...)...)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("want a refusal naming %s, got %v\n%s", c.want, err, out)
			}
			if n := len(srv.requests()); n != 0 {
				t.Errorf("a refused mix sent %d request(s)", n)
			}
		})
	}
}
