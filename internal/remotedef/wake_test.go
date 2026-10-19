package remotedef

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
)

type fakeStories map[string]string // id -> status

func (f fakeStories) Status(_ context.Context, id string) (string, error) {
	s, ok := f[id]
	if !ok {
		return "", errors.New("no story " + id)
	}
	return s, nil
}

func (f fakeStories) IDsAt(_ context.Context, status string) ([]string, error) {
	var out []string
	for id, s := range f {
		if s == status {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out, nil
}

type fakeRecorder struct{ seen map[string]Handoff }

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

func wakeDef() *Definition {
	return &Definition{
		Segments: []Segment{{Entry: "in_progress", Exit: "done"}, {Entry: "plan", Exit: "in_progress"}},
		Triggers: []Trigger{{Name: "nightly", Kind: "schedule"}},
	}
}

func TestWakeSelection(t *testing.T) {
	stories := fakeStories{"sty_a": "in_progress", "sty_b": "plan", "sty_c": "backlog", "sty_d": "done"}
	ctx := context.Background()
	for _, c := range []struct {
		name    string
		req     WakeRequest
		want    []string // story ids handed off, in order
		wantErr string
	}{
		{"named story at an entry", WakeRequest{TriggerID: "f1", Trigger: "nightly", StoryID: "sty_a"}, []string{"sty_a"}, ""},
		{"story at no entry", WakeRequest{TriggerID: "f2", Trigger: "nightly", StoryID: "sty_c"}, nil,
			"story sty_c is at backlog; remote edge accepts in_progress, plan"},
		{"undeclared trigger", WakeRequest{TriggerID: "f3", Trigger: "ghost", StoryID: "sty_a"}, nil, "unknown trigger ghost for remote edge"},
		{"trigger id is not a name", WakeRequest{TriggerID: "f4", Trigger: "f4"}, nil, "unknown trigger f4"},
		{"sweep", WakeRequest{TriggerID: "f5", Trigger: "nightly"}, []string{"sty_a", "sty_b"}, ""},
		{"unnamed sweep", WakeRequest{TriggerID: "f6"}, []string{"sty_a", "sty_b"}, ""},
		{"missing story", WakeRequest{TriggerID: "f7", StoryID: "sty_zzz"}, nil, "no story sty_zzz"},
	} {
		t.Run(c.name, func(t *testing.T) {
			rec := &fakeRecorder{}
			got, err := Wake(ctx, "edge", wakeDef(), c.req, stories, rec)
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
			if again, err := Wake(ctx, "edge", wakeDef(), c.req, stories, rec); err != nil || len(again) != 0 {
				t.Errorf("a redelivered wake must add nothing, got %v, %v", again, err)
			}
		})
	}
}

func TestWakeRecordsKindOfTheDeclaredTrigger(t *testing.T) {
	rec := &fakeRecorder{}
	got, err := Wake(context.Background(), "edge", wakeDef(), WakeRequest{TriggerID: "f1", Trigger: "nightly", StoryID: "sty_a"},
		fakeStories{"sty_a": "in_progress"}, rec)
	if err != nil || len(got) != 1 || got[0].Kind != "schedule" {
		t.Fatalf("got %+v, %v; want one hand-off of kind schedule", got, err)
	}
}
