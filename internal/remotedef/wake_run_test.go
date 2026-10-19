package remotedef

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestWakeRecordsEachRun: every run is recorded once, on success and on failure,
// with where and as whom it ran, the step it was handed over at, a minted run id
// that the runner saw too, and times that bracket the run.
func TestWakeRecordsEachRun(t *testing.T) {
	def := categoryDef()
	def.SatelleVersion = "1.2.3"
	def.Host.Name = "box-7"
	def.Identity.Label = "edge-label"
	def.Host.StoryCommand = "drive {story}"

	t.Run("success", func(t *testing.T) {
		st := stories(StoryRef{ID: "sty_1", Status: "in_progress"}, StoryRef{ID: "sty_2", Status: "in_progress"})
		rec := &fakeRecorder{}
		run := &idRunner{stubRunner: stubRunner{st: st}}
		tick := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
		w := Wiring{Stories: st, Recorder: rec, Runner: run, Now: func() time.Time { tick = tick.Add(time.Second); return tick }}
		if _, _, err := Wake(context.Background(), "edge", def, WakeRequest{TriggerID: "f1"}, w); err != nil {
			t.Fatal(err)
		}
		if len(rec.runs) != 2 {
			t.Fatalf("want one run record per story, got %d: %+v", len(rec.runs), rec.runs)
		}
		ids := map[string]bool{}
		for i, r := range rec.runs {
			if r.Remote != "edge" || r.Story != fmt.Sprintf("sty_%d", i+1) || r.Step != "in_progress" ||
				r.Version != "1.2.3" || r.Host != "box-7" || r.Label != "edge-label" || r.Error != "" {
				t.Errorf("run %d = %+v", i, r)
			}
			if !strings.HasPrefix(r.RunID, "run_") || ids[r.RunID] {
				t.Errorf("run id %q is not a fresh run_ id", r.RunID)
			}
			ids[r.RunID] = true
			if run.seen[r.Story] != r.RunID {
				t.Errorf("runner saw run id %q for %s, recorded %q", run.seen[r.Story], r.Story, r.RunID)
			}
			if !r.Started.Before(r.Ended) {
				t.Errorf("run %d started %v, ended %v", i, r.Started, r.Ended)
			}
		}
	})

	t.Run("failure is recorded before the error returns", func(t *testing.T) {
		st := stories(StoryRef{ID: "sty_1", Status: "in_progress"})
		rec := &fakeRecorder{}
		run := &stubRunner{st: st, err: errors.New("host.run: exit status 3")}
		_, _, err := Wake(context.Background(), "edge", def, WakeRequest{TriggerID: "f1"}, Wiring{Stories: st, Recorder: rec, Runner: run})
		if err == nil || len(rec.runs) != 1 || rec.runs[0].Error != "host.run: exit status 3" || rec.runs[0].Ended.IsZero() {
			t.Errorf("err = %v, runs = %+v", err, rec.runs)
		}
	})

	t.Run("record-only wake runs and records no run", func(t *testing.T) {
		st := stories(StoryRef{ID: "sty_1", Status: "in_progress"})
		rec := &fakeRecorder{}
		if _, _, err := Wake(context.Background(), "edge", def, WakeRequest{TriggerID: "f1"}, Wiring{Stories: st, Recorder: rec}); err != nil || len(rec.runs) != 0 {
			t.Errorf("err = %v, runs = %+v", err, rec.runs)
		}
	})
}

// idRunner is a stubRunner that also notes the run id each hand-off carried.
type idRunner struct {
	stubRunner
	seen map[string]string
}

func (r *idRunner) Run(ctx context.Context, h Handoff, command string) error {
	if r.seen == nil {
		r.seen = map[string]string{}
	}
	r.seen[h.Story] = h.RunID
	return r.stubRunner.Run(ctx, h, command)
}
