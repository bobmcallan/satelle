package remotedef

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// fakeStories is a story store: id -> story.
type fakeStories map[string]StoryRef

func stories(refs ...StoryRef) fakeStories {
	f := fakeStories{}
	for _, r := range refs {
		f[r.ID] = r
	}
	return f
}

func (f fakeStories) Story(_ context.Context, id string) (StoryRef, error) {
	s, ok := f[id]
	if !ok {
		return StoryRef{}, errors.New("no story " + id)
	}
	return s, nil
}

func (f fakeStories) At(_ context.Context, status string) ([]StoryRef, error) {
	var out []StoryRef
	for _, s := range f {
		if s.Status == status {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

type fakeRecorder struct {
	seen      map[string]Handoff
	handbacks []Handback
	runs      []RunRecord
}

func (r *fakeRecorder) RecordRun(_ context.Context, run RunRecord) error {
	r.runs = append(r.runs, run)
	return nil
}

func (r *fakeRecorder) Record(_ context.Context, h Handoff) (bool, error) {
	key := h.FireID + "/" + h.Story
	if _, dup := r.seen[key]; dup {
		return false, nil
	}
	if r.seen == nil {
		r.seen = map[string]Handoff{}
	}
	r.seen[key] = h
	return true, nil
}

func (r *fakeRecorder) RecordHandback(_ context.Context, b Handback) error {
	r.handbacks = append(r.handbacks, b)
	return nil
}

func wakeDef() *Definition {
	return &Definition{
		Segments: []Segment{{Entry: "in_progress", Exit: "done"}, {Entry: "plan", Exit: "in_progress"}},
		Triggers: []Trigger{{Name: "nightly", Kind: "schedule"}},
	}
}

func TestWakeSelection(t *testing.T) {
	st := stories(
		StoryRef{ID: "sty_a", Status: "in_progress"}, StoryRef{ID: "sty_b", Status: "plan"},
		StoryRef{ID: "sty_c", Status: "backlog"}, StoryRef{ID: "sty_d", Status: "done"})
	ctx := context.Background()
	for _, c := range []struct {
		name    string
		req     WakeRequest
		want    []string // story ids handed off, in order
		wantErr string
	}{
		{"named story at an entry", WakeRequest{TriggerID: "f1", Trigger: "nightly", StoryID: "sty_a"}, []string{"sty_a"}, ""},
		{"story at no entry", WakeRequest{TriggerID: "f2", Trigger: "nightly", StoryID: "sty_c"}, nil,
			`remote edge: story sty_c is at backlog with category "", which no segment accepts (accepts in_progress (any category); plan (any category))`},
		{"undeclared trigger", WakeRequest{TriggerID: "f3", Trigger: "ghost", StoryID: "sty_a"}, nil, "unknown trigger ghost for remote edge"},
		{"trigger id is not a name", WakeRequest{TriggerID: "f4", Trigger: "f4"}, nil, "unknown trigger f4"},
		{"sweep", WakeRequest{TriggerID: "f5", Trigger: "nightly"}, []string{"sty_a", "sty_b"}, ""},
		{"unnamed sweep", WakeRequest{TriggerID: "f6"}, []string{"sty_a", "sty_b"}, ""},
		{"missing story", WakeRequest{TriggerID: "f7", StoryID: "sty_zzz"}, nil, "no story sty_zzz"},
	} {
		t.Run(c.name, func(t *testing.T) {
			rec := &fakeRecorder{}
			w := Wiring{Stories: st, Recorder: rec}
			got, _, err := Wake(ctx, "edge", wakeDef(), c.req, w)
			if c.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("err = %v, want it to contain %q", err, c.wantErr)
				}
				if len(rec.seen) != 0 {
					t.Errorf("a refused wake must record nothing, recorded %v", rec.seen)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var ids []string
			for _, h := range got {
				ids = append(ids, h.Story)
				if h.Remote != "edge" || h.FireID != c.req.TriggerID || h.Trigger != c.req.Trigger {
					t.Errorf("hand-off = %+v, want remote edge, fire %s, trigger %q", h, c.req.TriggerID, c.req.Trigger)
				}
			}
			if fmt.Sprint(ids) != fmt.Sprint(c.want) {
				t.Errorf("handed off %v, want %v", ids, c.want)
			}
			if again, _, err := Wake(ctx, "edge", wakeDef(), c.req, w); err != nil || len(again) != 0 {
				t.Errorf("a redelivered wake must add nothing, got %v, %v", again, err)
			}
		})
	}
}

func TestWakeRecordsKindOfTheDeclaredTrigger(t *testing.T) {
	got, _, err := Wake(context.Background(), "edge", wakeDef(), WakeRequest{TriggerID: "f1", Trigger: "nightly", StoryID: "sty_a"},
		Wiring{Stories: stories(StoryRef{ID: "sty_a", Status: "in_progress"}), Recorder: &fakeRecorder{}})
	if err != nil || len(got) != 1 || got[0].Kind != "schedule" {
		t.Fatalf("got %+v, %v; want one hand-off of kind schedule", got, err)
	}
}

// categoryDef serves "merge" stories at plan and any category at in_progress.
func categoryDef() *Definition {
	return &Definition{
		Segments: []Segment{
			{Entry: "plan", Exit: "in_progress", Categories: []string{"feature"}},
			{Entry: "in_progress", Exit: "done"},
		},
	}
}

func TestWakeContractRefusesByCategory(t *testing.T) {
	st := stories(
		StoryRef{ID: "sty_f", Status: "plan", Category: "feature"},
		StoryRef{ID: "sty_b", Status: "plan", Category: "bug"},
		StoryRef{ID: "sty_n", Status: "plan"},
		StoryRef{ID: "sty_i", Status: "in_progress", Category: "bug"})
	ctx := context.Background()

	for _, id := range []string{"sty_b", "sty_n"} {
		rec := &fakeRecorder{}
		_, _, err := Wake(ctx, "edge", categoryDef(), WakeRequest{TriggerID: "f1", StoryID: id}, Wiring{Stories: st, Recorder: rec})
		cat := st[id].Category
		want := fmt.Sprintf("story %s is at plan with category %q, which no segment accepts (accepts in_progress (any category); plan (category feature))", id, cat)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want it to contain %q", id, err, want)
		}
		if len(rec.seen) != 0 {
			t.Errorf("%s: a refused hand-off must record nothing, recorded %v", id, rec.seen)
		}
	}

	rec := &fakeRecorder{}
	got, _, err := Wake(ctx, "edge", categoryDef(), WakeRequest{TriggerID: "f2"}, Wiring{Stories: st, Recorder: rec})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, h := range got {
		ids = append(ids, h.Story)
	}
	if fmt.Sprint(ids) != "[sty_f sty_i]" {
		t.Errorf("sweep handed off %v, want [sty_f sty_i]: a plan story outside the feature category is never handed off", ids)
	}
}

// stubRunner leaves each story at the status set for it, and records the calls.
type stubRunner struct {
	st       fakeStories
	leaveAt  map[string]string
	calls    []string
	commands []string
	err      error
}

func (r *stubRunner) Run(_ context.Context, h Handoff, command string) error {
	r.calls = append(r.calls, h.Story)
	r.commands = append(r.commands, command)
	if r.err != nil {
		return r.err
	}
	if s, ok := r.leaveAt[h.Story]; ok {
		ref := r.st[h.Story]
		ref.Status = s
		r.st[h.Story] = ref
	}
	return nil
}

// fakeLocal declares the statuses of a local route.
type fakeLocal map[string]bool

func (l fakeLocal) Declares(_ context.Context, _, status string) (bool, string, error) {
	return l[status], "default", nil
}

func (l fakeLocal) Engages(context.Context, string, string) (bool, string, error) {
	return false, "default", nil
}

func handbackDef() *Definition {
	d := wakeDef()
	d.Segments = []Segment{{Entry: "pushed", Exit: "merged"}, {Entry: "merged", Exit: "review"}}
	d.Host.StoryCommand = "agent drive {story} --to exit"
	return d
}

func TestWakeHandbackChecks(t *testing.T) {
	// The local workflow stops at pushed: it knows pushed and review, not merged.
	local := fakeLocal{"pushed": true, "review": true}
	for _, c := range []struct {
		name       string
		leaveAt    string
		wantReport bool
		inContract bool
		localKnown bool
		reason     string
	}{
		{"outside the contract exits", "pushed", true, false, true, "outside the contract's exit statuses (merged, review)"},
		{"a contract exit the local route lacks", "merged", true, true, false, "not a status of the local default route"},
		{"neither", "gone", true, false, false, "outside the contract's exit statuses (merged, review); not a status of the local default route"},
		{"a contract exit the local route declares", "review", false, true, true, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := stories(StoryRef{ID: "sty_a", Status: "pushed"})
			run := &stubRunner{st: st, leaveAt: map[string]string{"sty_a": c.leaveAt}}
			rec := &fakeRecorder{}
			_, backs, err := Wake(context.Background(), "edge", handbackDef(), WakeRequest{TriggerID: "f1"},
				Wiring{Stories: st, Recorder: rec, Runner: run, Local: local})
			if err != nil {
				t.Fatal(err)
			}
			if !c.wantReport {
				if len(backs) != 0 || len(rec.handbacks) != 0 {
					t.Fatalf("a status passing both checks must produce no report or row, got %+v / %+v", backs, rec.handbacks)
				}
				return
			}
			if len(backs) != 1 || len(rec.handbacks) != 1 {
				t.Fatalf("want one reported and one recorded hand-back, got %+v / %+v", backs, rec.handbacks)
			}
			b := rec.handbacks[0]
			if b.Story != "sty_a" || b.Status != c.leaveAt || b.InContract != c.inContract || b.LocalKnown != c.localKnown || b.Reason != c.reason {
				t.Errorf("hand-back = %+v, want status %s in_contract %v local_known %v reason %q", b, c.leaveAt, c.inContract, c.localKnown, c.reason)
			}
			if got, want := backs[0].Report(), "story sty_a left at "+c.leaveAt+": "+c.reason; got != want {
				t.Errorf("report = %q, want %q", got, want)
			}
		})
	}
}

func TestWakeCarriesSourceAndEvent(t *testing.T) {
	st := stories(StoryRef{ID: "sty_a", Status: "in_progress"}, StoryRef{ID: "sty_b", Status: "plan"}, StoryRef{ID: "sty_c", Status: "backlog"})
	ctx := context.Background()
	push := &WakeEvent{Kind: "push", Repo: "acme/edge", Branch: "feature/x", SHA: "abc123"}
	for _, c := range []struct {
		name string
		req  WakeRequest
		want []string // story ids handed off, in order
	}{
		{"story and event: only the story runs", WakeRequest{TriggerID: "f1", StoryID: "sty_a", Source: "push", Event: push}, []string{"sty_a"}},
		{"event without story: sweeps", WakeRequest{TriggerID: "f2", Source: "push", Event: push}, []string{"sty_a", "sty_b"}},
		{"no event", WakeRequest{TriggerID: "f3", Source: "schedule"}, []string{"sty_a", "sty_b"}},
		{"source and event kind are opaque", WakeRequest{TriggerID: "f4", Source: "other", Event: &WakeEvent{Kind: "elsewhere", Branch: "b", SHA: "s"}}, []string{"sty_a", "sty_b"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, _, err := Wake(ctx, "edge", wakeDef(), c.req, Wiring{Stories: st, Recorder: &fakeRecorder{}})
			if err != nil {
				t.Fatal(err)
			}
			var ids []string
			for _, h := range got {
				ids = append(ids, h.Story)
				if h.Source != c.req.Source {
					t.Errorf("source = %q, want %q", h.Source, c.req.Source)
				}
				if e := c.req.Event; e == nil {
					if h.EventKind != "" || h.Repo != "" || h.Branch != "" || h.SHA != "" {
						t.Errorf("a wake with no event must carry no event fields: %+v", h)
					}
				} else if h.EventKind != e.Kind || h.Repo != e.Repo || h.Branch != e.Branch || h.SHA != e.SHA {
					t.Errorf("hand-off %+v must carry the event %+v verbatim", h, *e)
				}
			}
			if fmt.Sprint(ids) != fmt.Sprint(c.want) {
				t.Errorf("handed off %v, want %v", ids, c.want)
			}
		})
	}
}

func TestWakeSweepRunsEachAcceptedStoryOnce(t *testing.T) {
	st := stories(
		StoryRef{ID: "sty_3", Status: "in_progress", Category: "bug"},
		StoryRef{ID: "sty_1", Status: "plan", Category: "feature"},
		StoryRef{ID: "sty_2", Status: "plan", Category: "bug"}, // plan accepts feature only
		StoryRef{ID: "sty_4", Status: "backlog"})
	def := categoryDef()
	def.Host.StoryCommand = "agent drive {story} --to exit"
	run := &stubRunner{st: st, leaveAt: map[string]string{"sty_1": "in_progress", "sty_3": "done"}}
	rec := &fakeRecorder{}
	w := Wiring{Stories: st, Recorder: rec, Runner: run, Local: fakeLocal{"in_progress": true, "done": true}}

	got, backs, err := Wake(context.Background(), "edge", def, WakeRequest{TriggerID: "f1"}, w)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(run.calls) != "[sty_1 sty_3]" || len(got) != 2 {
		t.Errorf("Run called for %v (handed off %d), want exactly [sty_1 sty_3] in id order", run.calls, len(got))
	}
	if fmt.Sprint(run.commands) != "[agent drive sty_1 --to exit agent drive sty_3 --to exit]" {
		t.Errorf("commands = %q", run.commands)
	}
	if len(backs) != 0 {
		t.Errorf("both stories were left at contract exits the local route declares: %+v", backs)
	}

	run.calls = nil
	if again, _, err := Wake(context.Background(), "edge", def, WakeRequest{TriggerID: "f1"}, w); err != nil || len(again) != 0 || len(run.calls) != 0 {
		t.Errorf("a redelivered wake must run nothing new, got %v, %v, ran %v", again, err, run.calls)
	}
}

func TestWakeRunErrorStopsTheSweep(t *testing.T) {
	st := stories(StoryRef{ID: "sty_1", Status: "in_progress"}, StoryRef{ID: "sty_2", Status: "in_progress"})
	run := &stubRunner{st: st, err: errors.New("host unreachable")}
	_, _, err := Wake(context.Background(), "edge", categoryDef(), WakeRequest{TriggerID: "f1"},
		Wiring{Stories: st, Recorder: &fakeRecorder{}, Runner: run})
	if err == nil || !strings.Contains(err.Error(), "host unreachable") || len(run.calls) != 1 {
		t.Errorf("err = %v after %v, want the run error after the first call", err, run.calls)
	}
}

// seatLocal is a local route with named engaging statuses, as the real route
// classifies them; every status it is asked about is declared.
type seatLocal struct{ engaging map[string]bool }

func (l seatLocal) Declares(context.Context, string, string) (bool, string, error) {
	return true, "default", nil
}

func (l seatLocal) Engages(_ context.Context, _, status string) (bool, string, error) {
	return l.engaging[status], "default", nil
}

func TestWakeRefusesSeatTakingHandoff(t *testing.T) {
	for _, c := range []struct {
		name     string
		segment  Segment
		engaging map[string]bool
	}{
		{"an engaging exit", Segment{Entry: "ready", Exit: "plan"}, map[string]bool{"plan": true}},
		{"an engaging entry with a seatless exit", Segment{Entry: "ready", Exit: "push"}, map[string]bool{"ready": true}},
	} {
		t.Run(c.name, func(t *testing.T) {
			def := &Definition{Segments: []Segment{c.segment}}
			def.Host.StoryCommand = "agent drive {story}"
			st := stories(StoryRef{ID: "sty_a", Status: "ready"})
			run := &stubRunner{st: st}
			rec := &fakeRecorder{}
			_, _, err := Wake(context.Background(), "edge", def, WakeRequest{TriggerID: "f1", StoryID: "sty_a"},
				Wiring{Stories: st, Recorder: rec, Runner: run, Local: seatLocal{c.engaging}})
			if err == nil || !strings.Contains(err.Error(), "engagement seat") {
				t.Fatalf("err = %v, want a refusal naming the engagement seat", err)
			}
			for name := range c.engaging {
				if !strings.Contains(err.Error(), "status "+name) {
					t.Errorf("err = %v, want it to name status %s", err, name)
				}
			}
			if len(rec.seen) != 0 || len(rec.handbacks) != 0 || len(run.calls) != 0 {
				t.Errorf("a refused hand-off records and runs nothing: recorded %v / %v, ran %v", rec.seen, rec.handbacks, run.calls)
			}
		})
	}
}

func TestWakeSweepSkipsSeatTakingStories(t *testing.T) {
	def := &Definition{Segments: []Segment{{Entry: "ready", Exit: "plan"}, {Entry: "push", Exit: "done"}}}
	def.Host.StoryCommand = "agent drive {story}"
	st := stories(StoryRef{ID: "sty_seat", Status: "ready"}, StoryRef{ID: "sty_free", Status: "push"})
	run := &stubRunner{st: st, leaveAt: map[string]string{"sty_free": "done"}}
	rec := &fakeRecorder{}
	got, backs, err := Wake(context.Background(), "edge", def, WakeRequest{TriggerID: "f1"},
		Wiring{Stories: st, Recorder: rec, Runner: run, Local: seatLocal{map[string]bool{"plan": true}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Story != "sty_free" || fmt.Sprint(run.calls) != "[sty_free]" {
		t.Errorf("handed off %+v, ran %v; want only sty_free", got, run.calls)
	}
	if len(rec.handbacks) != 1 || len(backs) != 1 {
		t.Fatalf("want the seat-taking story reported and recorded once, got %+v / %+v", backs, rec.handbacks)
	}
	b := rec.handbacks[0]
	if b.Story != "sty_seat" || !b.Refused || !b.HoldsSeat || !strings.HasPrefix(b.Reason, "refused before hand-off: ") || !strings.Contains(b.Reason, "engagement seat") {
		t.Errorf("hand-back = %+v, want sty_seat refused before hand-off for the engagement seat", b)
	}
	if r := backs[0].Report(); strings.Contains(r, "left at") || !strings.HasPrefix(r, "story sty_seat refused before hand-off: ") {
		t.Errorf("report = %q, want a refusal, never a status the remote left it at", r)
	}
}

func TestWakeHandbackAtEngagingStatus(t *testing.T) {
	// plan is a contract exit through the second segment, so the story left there
	// passes the contract check and fails only on the seat.
	def := &Definition{Segments: []Segment{{Entry: "push", Exit: "done"}, {Entry: "review", Exit: "plan"}}}
	def.Host.StoryCommand = "agent drive {story}"
	local := seatLocal{map[string]bool{"plan": true}}
	for _, c := range []struct {
		leaveAt  string
		wantFail bool
	}{{"plan", true}, {"done", false}} {
		t.Run(c.leaveAt, func(t *testing.T) {
			st := stories(StoryRef{ID: "sty_a", Status: "push"})
			run := &stubRunner{st: st, leaveAt: map[string]string{"sty_a": c.leaveAt}}
			rec := &fakeRecorder{}
			_, backs, err := Wake(context.Background(), "edge", def, WakeRequest{TriggerID: "f1", StoryID: "sty_a"},
				Wiring{Stories: st, Recorder: rec, Runner: run, Local: local})
			if err != nil {
				t.Fatal(err)
			}
			if !c.wantFail {
				if len(backs) != 0 || len(rec.handbacks) != 0 {
					t.Fatalf("a seatless status reports no failure, got %+v / %+v", backs, rec.handbacks)
				}
				return
			}
			if len(backs) != 1 || len(rec.handbacks) != 1 {
				t.Fatalf("want one failed hand-back reported and recorded, got %+v / %+v", backs, rec.handbacks)
			}
			if b := rec.handbacks[0]; b.Status != "plan" || !b.HoldsSeat || b.Refused || !strings.Contains(b.Reason, "engagement seat") {
				t.Errorf("hand-back = %+v, want status plan holding a seat, named in the reason", b)
			}
		})
	}
}

func TestWakeRequestParsesSourceAndEvent(t *testing.T) {
	for _, c := range []struct {
		name, body string
		want       WakeRequest
	}{
		{"push", `{"trigger_id":"f1","source":"push","event":{"kind":"push","repo":"acme/edge","branch":"main","sha":"abc"}}`,
			WakeRequest{TriggerID: "f1", Source: "push", Event: &WakeEvent{Kind: "push", Repo: "acme/edge", Branch: "main", SHA: "abc"}}},
		{"null event", `{"trigger_id":"f2","source":"api","event":null}`, WakeRequest{TriggerID: "f2", Source: "api"}},
		{"no source or event", `{"trigger_id":"f3"}`, WakeRequest{TriggerID: "f3"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			var got WakeRequest
			if err := json.Unmarshal([]byte(c.body), &got); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("got %+v, want %+v", got, c.want)
			}
		})
	}
}

func TestRenderStoryCommand(t *testing.T) {
	def := &Definition{Host: Host{StoryCommand: "agent drive {story} --label {label} --again {story}"}}
	got := RenderStoryCommand(def, "sty_12345678")
	if want := "agent drive sty_12345678 --label {label} --again sty_12345678"; got != want {
		t.Errorf("rendered %q, want %q: only {story} is resolved here", got, want)
	}
}
