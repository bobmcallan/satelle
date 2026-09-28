package gatehandle

import (
	"os"
	"strings"
	"testing"
	"time"
)

// startedWith is a handle whose recorded process identity is start.
func startedWith(t *testing.T, s *Store, start string) Meta {
	t.Helper()
	stubProbe(t, func(int) (Liveness, string) { return Alive, start })
	m, _ := s.Create(Meta{Verb: "story-set", Story: "sty_o"})
	if err := s.SetPID(m.ID, 4242); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestObserve_AliveWithMatchingIdentityIsRunning(t *testing.T) {
	s := newStore(t)
	m := startedWith(t, s, "T1")
	if o := s.Observe(m.ID); o.State != Running || o.Terminal() {
		t.Fatalf("Observe = %+v, want running", o)
	}
}

// A pid another process now holds is a run that died, not one still going.
func TestObserve_ReusedPidIsDied(t *testing.T) {
	s := newStore(t)
	m := startedWith(t, s, "T1")
	stubProbe(t, func(int) (Liveness, string) { return Alive, "T2" })
	o := s.Observe(m.ID)
	if o.State != Died || !strings.Contains(o.Reason, "reused") || !o.Verdict.Died || o.Verdict.Result.Error == "" {
		t.Fatalf("Observe = %+v, want died: pid reused", o)
	}
	// Alive but unreadable identity, against a recorded one, is the same.
	stubProbe(t, func(int) (Liveness, string) { return Alive, "" })
	if o := s.Observe(m.ID); o.State != Died {
		t.Fatalf("Observe = %+v, want died", o)
	}
}

func TestObserve_ProcessGoneAfterAliveIsDied(t *testing.T) {
	s := newStore(t)
	m := startedWith(t, s, "T1")
	stubProbe(t, func(int) (Liveness, string) { return Gone, "" })
	if o := s.Observe(m.ID); o.State != Died || !strings.Contains(o.Reason, "exited") {
		t.Fatalf("Observe = %+v, want died: exited", o)
	}
}

// A platform that cannot say is never taken for a live run a session waits on.
func TestObserve_UnverifiedWhenPlatformCannotSay(t *testing.T) {
	s := newStore(t)
	m := startedWith(t, s, "T1")
	stubProbe(t, func(int) (Liveness, string) { return Unknown, "" })
	o := s.Observe(m.ID)
	if o.State != RunningUnverified || !strings.Contains(o.Reason, "liveness unavailable on ") {
		t.Fatalf("Observe = %+v, want running-unverified", o)
	}

	noID := startedWith(t, s, "") // identity unreadable when recorded
	if meta, _ := s.Meta(noID.ID); !meta.IdentityUnavailable {
		t.Errorf("a run recorded with no identity is not marked IdentityUnavailable: %+v", meta)
	}
	if o := s.Observe(noID.ID); o.State != RunningUnverified {
		t.Fatalf("Observe = %+v, want running-unverified", o)
	}
}

func TestProbe_TestKnobMakesLivenessUnavailable(t *testing.T) {
	t.Setenv(EnvLiveness, "unavailable")
	if live, start := probeProcess(os.Getpid()); live != Unknown || start != "" {
		t.Fatalf("probe = %v %q, want unknown with no identity", live, start)
	}
}

// One Observe asks the platform once: the answer it acts on is the answer it saw.
func TestObserve_OneLivenessDecisionPerCall(t *testing.T) {
	s := newStore(t)
	m := startedWith(t, s, "T1")
	calls := 0
	stubProbe(t, func(int) (Liveness, string) { calls++; return Alive, "T1" })
	s.Observe(m.ID)
	if calls != 1 {
		t.Fatalf("Observe probed %d times, want 1", calls)
	}
	calls = 0
	s.Load(m.ID)
	if calls != 1 {
		t.Fatalf("Load probed %d times, want 1", calls)
	}
}

// A run that finishes between the liveness answer and the outcome is finished.
func TestObserve_FinishedWhileReusedIsFinished(t *testing.T) {
	s := newStore(t)
	m := startedWith(t, s, "T1")
	stubProbe(t, func(int) (Liveness, string) {
		_ = s.Finish(m.ID, Result{})
		return Alive, "T2"
	})
	if got := s.State(m.ID); got != Finished {
		t.Fatalf("state = %s, want finished", got)
	}
}

// Age counts from when the run ended: a long gate that finishes just now is news.
func TestUndelivered_AgeCountsFromTheEnd(t *testing.T) {
	s := newStore(t)
	m, _ := s.Create(Meta{ID: "gw_longrun", Verb: "x", Started: time.Now().Add(-MaxDeliveryAge - time.Hour)})
	if got := s.Undelivered(); len(got) != 0 {
		t.Fatalf("a day-old unfinished run was listed: %v", got)
	}
	_ = s.Finish(m.ID, Result{})
	if got := s.Undelivered(); len(got) != 1 {
		t.Fatalf("a run that just finished was dropped for the age it began at: %v", got)
	}
}

// A run its session was told about is never dropped for age.
func TestUndelivered_NotifiedRunIsNeverFilteredByAge(t *testing.T) {
	s := newStore(t)
	m, _ := s.Create(Meta{ID: "gw_notified", Verb: "x", Started: time.Now().Add(-MaxDeliveryAge - time.Hour)})
	s.MarkNotified(m.ID, false, "")
	if got := s.Undelivered(); len(got) != 1 || got[0] != m.ID {
		t.Fatalf("Undelivered = %v, want the notified run", got)
	}
}

func TestMarkNotified_Unverified(t *testing.T) {
	s := newStore(t)
	m, _ := s.Create(Meta{Verb: "x"})
	if s.UnverifiedNotified(m.ID) {
		t.Fatal("unverified-notified before it was marked")
	}
	s.MarkNotified(m.ID, true, "liveness unavailable on test")
	s.MarkNotified(m.ID, true, "a second call is a no-op")
	if !s.UnverifiedNotified(m.ID) || !s.notified(m.ID) {
		t.Fatal("markers missing")
	}
	if b, _ := os.ReadFile(s.path(m.ID, "unverified-notified")); !strings.Contains(string(b), "liveness unavailable on test") {
		t.Fatalf("the reason was not recorded: %q", b)
	}
}

// A run still going, or one its session was told about, is not pruned however
// old it is.
func TestPrune_KeepsRunningAndNotifiedRuns(t *testing.T) {
	s := newStore(t)
	old := time.Now().Add(-3 * retention)
	stubProbe(t, func(int) (Liveness, string) { return Alive, "T1" })
	running, _ := s.Create(Meta{ID: "gw_running", Verb: "x", Started: old})
	_ = s.SetPID(running.ID, 4242)
	notified, _ := s.Create(Meta{ID: "gw_notified", Verb: "x", Started: old})
	s.MarkNotified(notified.ID, true, "r")
	stale, _ := s.Create(Meta{ID: "gw_stale", Verb: "x", Started: old})

	s.prune(time.Now())
	for _, id := range []string{running.ID, notified.ID} {
		if _, err := os.Stat(s.path(id)); err != nil {
			t.Errorf("%s was pruned while its verdict is still owed", id)
		}
	}
	if _, err := os.Stat(s.path(stale.ID)); err == nil {
		t.Error("an old never-started run was kept")
	}
}
