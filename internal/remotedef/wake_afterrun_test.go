package remotedef

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// AfterRun runs between Runner.Run and the hand-back check: the hand-back sees
// the state AfterRun brought home, not the state before it.
func TestWakeAfterRunSitsBetweenRunAndHandbackCheck(t *testing.T) {
	st := stories(StoryRef{ID: "sty_a", Status: "pushed"})
	run := &stubRunner{st: st} // the run leaves the story where it was
	rec := &fakeRecorder{}
	var order []string
	w := Wiring{Stories: st, Recorder: rec, Runner: run, Local: fakeLocal{"pushed": true, "merged": true, "review": true},
		AfterRun: func(_ context.Context, id string) error {
			order = append(order, fmt.Sprintf("after:%s:ran=%d", id, len(run.calls)))
			ref := st[id]
			ref.Status = "review" // the hosted side's change, pulled home
			st[id] = ref
			return nil
		}}
	_, backs, err := Wake(context.Background(), "edge", handbackDef(), WakeRequest{TriggerID: "f1"}, w)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(order) != "[after:sty_a:ran=1]" {
		t.Errorf("AfterRun order = %v, want one call after the run", order)
	}
	if len(backs) != 0 || len(rec.handbacks) != 0 {
		t.Errorf("the hand-back must see the pulled status, got %+v / %+v", backs, rec.handbacks)
	}
}

// A nil AfterRun is today's behaviour: the hand-back sees what the run left.
func TestWakeNilAfterRunChecksWhatTheRunLeft(t *testing.T) {
	st := stories(StoryRef{ID: "sty_a", Status: "pushed"})
	rec := &fakeRecorder{}
	_, backs, err := Wake(context.Background(), "edge", handbackDef(), WakeRequest{TriggerID: "f1"},
		Wiring{Stories: st, Recorder: rec, Runner: &stubRunner{st: st}, Local: fakeLocal{"pushed": true}})
	if err != nil || len(backs) != 1 || len(rec.handbacks) != 1 {
		t.Errorf("err = %v, backs = %v, rows = %v, want one hand-back for a story left at pushed", err, backs, rec.handbacks)
	}
}

func TestWakeAfterRunErrorFailsTheStory(t *testing.T) {
	st := stories(StoryRef{ID: "sty_a", Status: "pushed"})
	rec := &fakeRecorder{}
	w := Wiring{Stories: st, Recorder: rec, Runner: &stubRunner{st: st},
		AfterRun: func(context.Context, string) error { return errors.New("pull failed") }}
	_, _, err := Wake(context.Background(), "edge", handbackDef(), WakeRequest{TriggerID: "f1"}, w)
	if err == nil || !strings.Contains(err.Error(), "pull failed") || len(rec.handbacks) != 0 {
		t.Errorf("err = %v, handbacks = %v, want the AfterRun error and no hand-back", err, rec.handbacks)
	}
}

func TestWakeRefusalIsTyped(t *testing.T) {
	st := stories(StoryRef{ID: "sty_a", Status: "backlog"})
	w := Wiring{Stories: st, Recorder: &fakeRecorder{}}
	_, _, err := Wake(context.Background(), "edge", wakeDef(), WakeRequest{TriggerID: "f1", StoryID: "sty_a"}, w)
	var re *RefusedError
	if !errors.As(err, &re) || re.Story != "sty_a" {
		t.Fatalf("err = %v, want a *RefusedError for sty_a", err)
	}
	_, _, err = Wake(context.Background(), "edge", wakeDef(), WakeRequest{TriggerID: "f1", StoryID: "sty_gone"}, w)
	if err == nil || errors.As(err, &re) {
		t.Errorf("a missing story is not a contract refusal: %v", err)
	}
}
