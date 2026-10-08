package gatehandle

import (
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	return New(t.TempDir())
}

// stubProbe stands in for the platform's process probe for one test.
func stubProbe(t *testing.T, f func(int) (Liveness, string)) {
	t.Helper()
	old := probe
	probe = f
	t.Cleanup(func() { probe = old })
}

func TestLifecycle_RunningThenFinished(t *testing.T) {
	s := newStore(t)
	m, err := s.Create(Meta{Verb: "story-set", Story: "sty_x", Argv: []string{"story", "set"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetPID(m.ID, os.Getpid()); err != nil {
		t.Fatal(err)
	}
	if got := s.State(m.ID); got != Running {
		t.Fatalf("state = %s, want running", got)
	}
	if _, ok := s.Load(m.ID); ok {
		t.Fatal("a running handle loaded as a finished verdict")
	}
	_ = os.WriteFile(s.OutPath(m.ID), []byte(`{"ok":true}`), 0o644)
	_ = os.WriteFile(s.ErrPath(m.ID), []byte("accepted plan→in_progress\n"), 0o644)
	if err := s.Finish(m.ID, Result{ExitCode: 0}); err != nil {
		t.Fatal(err)
	}
	v, ok := s.Load(m.ID)
	if !ok || v.Died || v.Result.ExitCode != 0 || v.Stdout != `{"ok":true}` || v.Stderr != "accepted plan→in_progress\n" {
		t.Fatalf("Load = %+v ok=%v", v, ok)
	}
}

// Finish is written once: a delivery that noticed a dead process must not
// overwrite the run's own result, and a second Finish is a no-op.
func TestFinish_WritesOnce(t *testing.T) {
	s := newStore(t)
	m, _ := s.Create(Meta{Verb: "story-set"})
	if err := s.Finish(m.ID, Result{ExitCode: 3, Error: "rejected"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Finish(m.ID, Result{ExitCode: 0}); err != nil {
		t.Fatal(err)
	}
	if r, _ := s.Result(m.ID); r.ExitCode != 3 || r.Error != "rejected" {
		t.Fatalf("second Finish overwrote the first: %+v", r)
	}
}

// A process that vanished without recording a result is delivered as a
// failure, not left running forever.
func TestState_DiedWhenProcessGoneWithoutResult(t *testing.T) {
	stubProbe(t, func(int) (Liveness, string) { return Gone, "" })

	s := newStore(t)
	m, _ := s.Create(Meta{Verb: "story-set"})
	_ = s.SetPID(m.ID, 424242)
	if got := s.State(m.ID); got != Died {
		t.Fatalf("state = %s, want died", got)
	}
	v, ok := s.Load(m.ID)
	if !ok || !v.Died || v.Result.ExitCode == 0 || v.Result.Error == "" {
		t.Fatalf("a died handle must load as a failure: %+v ok=%v", v, ok)
	}
}

// Claim is the exactly-once seam: many racing claimers, one winner.
func TestClaim_ExactlyOnceUnderRace(t *testing.T) {
	s := newStore(t)
	m, _ := s.Create(Meta{Verb: "story-set"})
	_ = s.Finish(m.ID, Result{})
	var wins atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if s.Claim(m.ID) {
				wins.Add(1)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("%d claimers won, want exactly 1", wins.Load())
	}
	if !s.Delivered(m.ID) {
		t.Fatal("claimed handle not marked delivered")
	}
	if got := s.Undelivered(); len(got) != 0 {
		t.Fatalf("delivered handle still listed: %v", got)
	}
}

func TestUndelivered_OldestFirstAndSkipsStrangers(t *testing.T) {
	s := newStore(t)
	a, _ := s.Create(Meta{ID: "gw_aaaa", Verb: "x"})
	b, _ := s.Create(Meta{ID: "gw_bbbb", Verb: "x", Started: a.Started.Add(-1e9)})
	if err := os.MkdirAll(s.dir+"/not-a-handle", 0o755); err != nil {
		t.Fatal(err)
	}
	got := s.Undelivered()
	if len(got) != 2 || got[0] != b.ID || got[1] != a.ID {
		t.Fatalf("Undelivered = %v, want [%s %s]", got, b.ID, a.ID)
	}
}

// A parent that lost its way between Create and SetPID must not leave a session
// waiting forever on a run that was never started.
func TestState_NeverStartedIsDeadAfterGrace(t *testing.T) {
	s := newStore(t)
	fresh, _ := s.Create(Meta{ID: "gw_fresh", Verb: "x"})
	if got := s.State(fresh.ID); got != Running {
		t.Errorf("a just-created handle = %s, want running (the parent is still starting it)", got)
	}
	// time-subject: the start grace is the subject, crossed by backdating Started.
	old, _ := s.Create(Meta{ID: "gw_old", Verb: "x", Started: time.Now().Add(-2 * startGrace)})
	if got := s.State(old.ID); got != Died {
		t.Errorf("a handle that never got a process = %s, want died", got)
	}
}

// A run that finishes between the result check and the liveness check is
// finished, not dead.
func TestState_FinishedBetweenChecksIsNotDead(t *testing.T) {
	s := newStore(t)
	m, _ := s.Create(Meta{Verb: "x"})
	_ = s.SetPID(m.ID, 424242)
	stubProbe(t, func(int) (Liveness, string) {
		_ = s.Finish(m.ID, Result{}) // the run records its result as its process exits
		return Gone, ""
	})
	if got := s.State(m.ID); got != Finished {
		t.Fatalf("state = %s, want finished", got)
	}
}

func TestPrune_RemovesOldDeliveredHandlesOnly(t *testing.T) {
	s := newStore(t)
	oldDelivered, _ := s.Create(Meta{ID: "gw_olddel", Verb: "x"})
	_ = s.Finish(oldDelivered.ID, Result{})
	s.Claim(oldDelivered.ID)
	past := time.Now().Add(-retention - time.Hour) // time-subject: retention age is the subject, set by backdating
	_ = os.Chtimes(s.path(oldDelivered.ID, "delivered"), past, past)
	recent, _ := s.Create(Meta{ID: "gw_recent", Verb: "x"})
	_ = s.Finish(recent.ID, Result{})
	s.Claim(recent.ID)
	pending, _ := s.Create(Meta{ID: "gw_pending", Verb: "x"})

	s.prune(time.Now())
	if _, err := os.Stat(s.path(oldDelivered.ID)); err == nil {
		t.Error("a handle delivered past retention was kept")
	}
	for _, id := range []string{recent.ID, pending.ID} {
		if _, err := os.Stat(s.path(id)); err != nil {
			t.Errorf("%s was pruned inside its retention", id)
		}
	}
}

func TestUndelivered_SkipsRunsTooOldToBeANotification(t *testing.T) {
	s := newStore(t)
	// time-subject: delivery age is the subject, set by backdating Started, never slept for.
	_, _ = s.Create(Meta{ID: "gw_stale", Verb: "x", Started: time.Now().Add(-MaxDeliveryAge - time.Hour)})
	fresh, _ := s.Create(Meta{ID: "gw_fresh", Verb: "x"})
	if got := s.Undelivered(); len(got) != 1 || got[0] != fresh.ID {
		t.Fatalf("Undelivered = %v, want only the fresh run", got)
	}
}

func TestUndelivered_EmptyWhenNoDir(t *testing.T) {
	if got := newStore(t).Undelivered(); len(got) != 0 {
		t.Fatalf("got %v", got)
	}
}
