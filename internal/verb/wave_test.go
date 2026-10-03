package verb_test

import (
	"context"
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// waveWF is one lane for every category plus an epic-parent lane that carries a
// container step (`ready`, waits_on_children) declaring the child schedule.
// schedule == "" leaves the key absent.
func waveWF(schedule string) map[string]string {
	line := ""
	if schedule != "" {
		line = `schedule = "` + schedule + `"` + "\n"
	}
	return routeHalves(
		`["*"]
obligations = ["raised", "coded", "closed"]
park = { state = "blocked" }
cancel = { state = "cancelled" }

[epic-parent]
obligations = ["raised", "ready", "coded", "closed"]
park = { state = "blocked" }
cancel = { state = "cancelled" }
`,
		`[raised]
status = "backlog"
start = true

[ready]
status = "ready"
waits_on_children = true
`+line+`requires = ["raised"]

[coded]
status = "in_progress"
agent = "executor"
requires = ["raised"]

[closed]
status = "done"
terminal = true
requires = ["coded"]
`)
}

func waveEpic(t *testing.T) workitem.Item {
	t.Helper()
	return mkEpicStory(t, map[string]any{"category": "epic-parent", "tags": []string{"epic:w"}})
}

func waveChild(t *testing.T, tags ...string) workitem.Item {
	t.Helper()
	return mkEpicStory(t, map[string]any{"category": "fix", "tags": append([]string{"epic:w"}, tags...)})
}

func cancelChild(t *testing.T, id string) {
	t.Helper()
	for _, s := range []string{"in_progress", "cancelled"} {
		if err := setStatus(t, id, s); err != nil {
			t.Fatalf("%s → %s: %v", id, s, err)
		}
	}
}

func wave(t *testing.T, id string) (verb.WaveResult, error) {
	t.Helper()
	raw, err := dispatchRaw(t, "story-wave", map[string]any{"id": id})
	var res verb.WaveResult
	if err == nil {
		if uerr := json.Unmarshal(raw, &res); uerr != nil {
			t.Fatal(uerr)
		}
	}
	return res, err
}

func omittedByID(res verb.WaveResult, id string) (verb.WaveOmitted, bool) {
	for _, o := range res.Omitted {
		if o.ID == id {
			return o, true
		}
	}
	return verb.WaveOmitted{}, false
}

// fourChildEpic builds the AC5 epic: two children with no dependency, one
// (c) depending on a fourth (d) that is already done.
func fourChildEpic(t *testing.T) (epic, a, b, c, d workitem.Item) {
	t.Helper()
	epic = waveEpic(t)
	d = waveChild(t)
	closeChild(t, d.ID)
	a = waveChild(t)
	b = waveChild(t)
	c = waveChild(t, "depends-on:"+d.ID)
	return
}

func TestWaveParallelFourChildEpic(t *testing.T) {
	wireWithWorkflows(t, waveWF("parallel"))
	epic, a, b, c, d := fourChildEpic(t)

	res, err := wave(t, epic.ID)
	if err != nil {
		t.Fatalf("wave: %v", err)
	}
	want := []string{a.ID, b.ID, c.ID}
	sort.Strings(want)
	if !reflect.DeepEqual(res.Runnable, want) {
		t.Errorf("runnable = %v, want %v (d is done, so not runnable)", res.Runnable, want)
	}
	if res.Schedule != "parallel" || res.Theme != "epic:w" {
		t.Errorf("schedule/theme = %q/%q", res.Schedule, res.Theme)
	}
	for _, id := range res.Runnable {
		if id == d.ID {
			t.Errorf("the done child %s must not be runnable", d.ID)
		}
	}
	if len(res.Omitted) != 0 {
		t.Errorf("nothing waits on an unfinished dependency: %+v", res.Omitted)
	}
}

func TestWaveSequentialRefusesMoreThanOneRunnable(t *testing.T) {
	wireWithWorkflows(t, waveWF("sequential"))
	epic, a, b, c, _ := fourChildEpic(t)

	res, err := wave(t, epic.ID)
	if err == nil {
		t.Fatalf("sequential with three runnable children must refuse, got %+v", res)
	}
	for _, want := range []string{a.ID, b.ID, c.ID, "depends-on"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal must contain %q: %v", want, err)
		}
	}
	if len(res.Runnable) != 0 {
		t.Errorf("no id may be emitted as runnable on a refusal: %v", res.Runnable)
	}
}

// Sequential never selects by order: or created_at: the decoys favour one child
// and the command still refuses.
func TestWaveSequentialDoesNotPickByOrder(t *testing.T) {
	wireWithWorkflows(t, waveWF("sequential"))
	epic := waveEpic(t)
	first := waveChild(t, "order:1")
	time.Sleep(2 * time.Millisecond)
	second := waveChild(t, "order:2")

	_, err := wave(t, epic.ID)
	if err == nil {
		t.Fatal("two runnable children under sequential must refuse; order: is not a tie-break")
	}
	if !strings.Contains(err.Error(), first.ID) || !strings.Contains(err.Error(), second.ID) {
		t.Errorf("refusal must name both runnable ids: %v", err)
	}
}

func TestWaveSequentialReturnsTheOneRunnable(t *testing.T) {
	wireWithWorkflows(t, waveWF("sequential"))
	epic := waveEpic(t)
	first := waveChild(t)
	second := waveChild(t, "depends-on:"+first.ID)

	res, err := wave(t, epic.ID)
	if err != nil {
		t.Fatalf("wave: %v", err)
	}
	if !reflect.DeepEqual(res.Runnable, []string{first.ID}) {
		t.Errorf("runnable = %v, want only %s", res.Runnable, first.ID)
	}
	o, ok := omittedByID(res, second.ID)
	if !ok || !strings.Contains(o.Reason, first.ID) {
		t.Errorf("the waiting child must be omitted naming %s: %+v", first.ID, res.Omitted)
	}

	// Once the first is done the second is the one runnable child.
	closeChild(t, first.ID)
	res, err = wave(t, epic.ID)
	if err != nil || !reflect.DeepEqual(res.Runnable, []string{second.ID}) {
		t.Errorf("after %s is done: runnable = %v err = %v, want [%s]", first.ID, res.Runnable, err, second.ID)
	}
}

// A dependency that is in progress or cancelled does not satisfy the edge; a
// cancelled one is named as such.
func TestWaveOmitsChildWhoseDependencyIsNotDone(t *testing.T) {
	wireWithWorkflows(t, waveWF("parallel"))
	epic := waveEpic(t)
	cancelled := waveChild(t)
	cancelChild(t, cancelled.ID)
	running := waveChild(t)
	if err := setStatus(t, running.ID, "in_progress"); err != nil {
		t.Fatal(err)
	}
	onRunning := waveChild(t, "depends-on:"+running.ID)
	onCancelled := waveChild(t, "depends-on:"+cancelled.ID)
	free := waveChild(t)

	res, err := wave(t, epic.ID)
	if err != nil {
		t.Fatalf("wave: %v", err)
	}
	want := []string{running.ID, free.ID}
	sort.Strings(want)
	if !reflect.DeepEqual(res.Runnable, want) {
		t.Errorf("runnable = %v, want %v (a cancelled child is not runnable)", res.Runnable, want)
	}
	if o, ok := omittedByID(res, onRunning.ID); !ok || !strings.Contains(o.Reason, running.ID) || !strings.Contains(o.Reason, "in_progress") {
		t.Errorf("child waiting on an in-progress dependency: %+v", res.Omitted)
	}
	o, ok := omittedByID(res, onCancelled.ID)
	if !ok || !strings.Contains(o.Reason, cancelled.ID) || !strings.Contains(o.Reason, "cancelled") {
		t.Fatalf("child waiting on a cancelled dependency must name it: %+v", res.Omitted)
	}
	if len(o.WaitingOn) != 1 || o.WaitingOn[0].ID != cancelled.ID || !o.WaitingOn[0].Cancelled {
		t.Errorf("waiting_on must carry the cancelled dependency: %+v", o.WaitingOn)
	}
}

// Membership is the epic: set, not parent_id: a story linked only by parent_id
// is not a candidate, and a tag-only story is.
func TestWaveMembershipIsTheEpicSet(t *testing.T) {
	wireWithWorkflows(t, waveWF("parallel"))
	epic := waveEpic(t)
	tagged := waveChild(t)
	linked := mkEpicStory(t, map[string]any{"category": "fix", "parent_id": epic.ID})

	res, err := wave(t, epic.ID)
	if err != nil {
		t.Fatalf("wave: %v", err)
	}
	if !reflect.DeepEqual(res.Runnable, []string{tagged.ID}) {
		t.Errorf("runnable = %v, want only the tagged child (not %s)", res.Runnable, linked.ID)
	}
}

// AC4: every refusal is an error that names the gap and emits no runnable id.
func TestWaveRefusals(t *testing.T) {
	cases := map[string]struct {
		wf    map[string]string
		setup func(t *testing.T) workitem.Item
		want  string
	}{
		"no declared schedule": {
			wf: waveWF(""),
			setup: func(t *testing.T) workitem.Item {
				e := waveEpic(t)
				waveChild(t)
				return e
			},
			want: "no schedule is declared",
		},
		"two epic-parents on one tag": {
			wf: waveWF("parallel"),
			setup: func(t *testing.T) workitem.Item {
				waveEpic(t)
				e := waveEpic(t)
				waveChild(t)
				return e
			},
			want: "2 epic-parents carry epic:w",
		},
		"no epic tag": {
			wf: waveWF("parallel"),
			setup: func(t *testing.T) workitem.Item {
				return mkEpicStory(t, map[string]any{"category": "epic-parent"})
			},
			want: "no epic:<theme> tag",
		},
		"not an epic-parent": {
			wf: waveWF("parallel"),
			setup: func(t *testing.T) workitem.Item {
				return waveChild(t)
			},
			want: "not an epic-parent",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			wireWithWorkflows(t, tc.wf)
			container := tc.setup(t)
			raw, err := dispatchRaw(t, "story-wave", map[string]any{"id": container.ID})
			if err == nil {
				t.Fatalf("must refuse, got %s", raw)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("refusal must name the gap %q: %v", tc.want, err)
			}
			if len(raw) != 0 {
				t.Errorf("a refusal must emit no response body, got %s", raw)
			}
		})
	}
}

// AC1: the assessment writes nothing — every story and the ledger are unchanged.
func TestWaveWritesNothing(t *testing.T) {
	db := wireWithWorkflowsStore(t, waveWF("parallel"))
	epic, a, b, c, d := fourChildEpic(t)
	snap := func() string {
		out := []string{string(call(t, "story-list", map[string]any{"tag": "epic:w"}))}
		for _, id := range []string{epic.ID, a.ID, b.ID, c.ID, d.ID} {
			out = append(out, string(call(t, "ledger-list", map[string]any{"story_id": id, "limit": 1000})))
		}
		leases, err := db.Leases.List(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		lb, _ := json.Marshal(leases)
		return strings.Join(append(out, string(lb)), "\n")
	}
	before := snap()
	if _, err := wave(t, epic.ID); err != nil {
		t.Fatalf("wave: %v", err)
	}
	if after := snap(); after != before {
		t.Errorf("story wave changed the store (stories, ledger or leases):\nbefore %s\nafter  %s", before, after)
	}
}
