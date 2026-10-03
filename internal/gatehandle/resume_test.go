package gatehandle

import (
	"os"
	"testing"
	"time"
)

func TestStopCountCountsAndResetsPerSession(t *testing.T) {
	s := New(t.TempDir())
	if got := s.StopCount("a"); got != 0 {
		t.Fatalf("fresh count = %d", got)
	}
	s.AddStopCount("a")
	s.AddStopCount("a")
	s.AddStopCount("b")
	if s.StopCount("a") != 2 || s.StopCount("b") != 1 {
		t.Fatalf("a=%d b=%d, want 2 and 1", s.StopCount("a"), s.StopCount("b"))
	}
	s.ResetStopCount("a")
	if s.StopCount("a") != 0 || s.StopCount("b") != 1 {
		t.Fatalf("reset touched the wrong session: a=%d b=%d", s.StopCount("a"), s.StopCount("b"))
	}
}

func TestStopCountDirIsNotAHandle(t *testing.T) {
	s := New(t.TempDir())
	m, err := s.Create(Meta{Verb: "v", Argv: []string{"x"}})
	if err != nil {
		t.Fatal(err)
	}
	s.AddStopCount("a")
	if ids := s.Undelivered(); len(ids) != 1 || ids[0] != m.ID {
		t.Fatalf("Undelivered = %v, want just %s", ids, m.ID)
	}
}

func TestArmResumeIsExclusiveAndKeepsTheRun(t *testing.T) {
	s := New(t.TempDir())
	m, _ := s.Create(Meta{Verb: "v", Argv: []string{"x"}})
	if !s.ArmResume(m.ID) {
		t.Fatal("first arm refused")
	}
	if s.ArmResume(m.ID) {
		t.Fatal("a second watcher was allowed for one handle")
	}
	if !s.ResumeArmed(m.ID) || !s.notified(m.ID) {
		t.Error("an armed handle is not marked, so it could age out before its verdict")
	}
	s.Disarm(m.ID)
	if !s.ArmResume(m.ID) {
		t.Error("a disarmed handle could not be armed again")
	}
}

func TestReleaseGivesAClaimBack(t *testing.T) {
	s := New(t.TempDir())
	m, _ := s.Create(Meta{Verb: "v", Argv: []string{"x"}})
	if !s.Claim(m.ID) || s.Claim(m.ID) {
		t.Fatal("claim is not exactly-once")
	}
	s.Release(m.ID)
	if s.Delivered(m.ID) || !s.Claim(m.ID) {
		t.Error("a released claim could not be taken again")
	}
}

func TestLockSessionIsExclusiveAndTakesOverADeadHolder(t *testing.T) {
	s := New(t.TempDir())
	unlock, ok := s.LockSession("a", 0)
	if !ok {
		t.Fatal("first lock refused")
	}
	if _, ok := s.LockSession("a", 0); ok {
		t.Fatal("two resumes of one session at once")
	}
	if _, ok := s.LockSession("b", 0); !ok {
		t.Fatal("a lock on one session blocked another")
	}
	unlock()
	unlock2, ok := s.LockSession("a", 0)
	if !ok {
		t.Fatal("the lock was not released")
	}
	unlock2()

	// A holder that no longer exists is taken over, not waited on.
	old := probe
	probe = func(int) (Liveness, string) { return Gone, "" }
	t.Cleanup(func() { probe = old })
	if err := os.WriteFile(s.turnPath("a", "lock"), []byte("2147483646\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if _, ok := s.LockSession("a", 5*time.Second); !ok || time.Since(start) > time.Second {
		t.Fatalf("a dead holder's lock was waited on (ok=%v, %s)", ok, time.Since(start))
	}
}

func ageTurn(t *testing.T, s *Store, session string, by time.Duration) {
	t.Helper()
	old := time.Now().Add(-by)
	if err := os.Chtimes(s.turnPath(session, "turn"), old, old); err != nil {
		t.Fatal(err)
	}
}

func TestTurnIdle(t *testing.T) {
	const stopCap, quiet = 8, time.Minute
	spend := func(s *Store, n int) {
		for i := 0; i < n; i++ {
			s.AddStopCount("a")
		}
	}

	t.Run("no record means no hook has spoken, so idle", func(t *testing.T) {
		if !New(t.TempDir()).TurnIdle("a", stopCap, quiet) {
			t.Error("a session nothing recorded was held back")
		}
	})
	t.Run("a closed turn is idle", func(t *testing.T) {
		s := New(t.TempDir())
		s.OpenTurn("a")
		s.CloseTurn("a")
		if !s.TurnIdle("a", stopCap, quiet) {
			t.Error("a closed turn was not idle")
		}
	})
	t.Run("an open turn with budget left is not idle however quiet", func(t *testing.T) {
		s := New(t.TempDir())
		s.OpenTurn("a")
		spend(s, stopCap-1)
		ageTurn(t, s, "a", time.Hour)
		if s.TurnIdle("a", stopCap, quiet) {
			t.Error("a turn that can still be woken in-turn was resumed into")
		}
	})
	t.Run("a spent turn that is still active is not idle", func(t *testing.T) {
		s := New(t.TempDir())
		s.OpenTurn("a")
		spend(s, stopCap)
		if s.TurnIdle("a", stopCap, quiet) {
			t.Error("a spent turn with a fresh tool call was treated as ended")
		}
	})
	t.Run("a spent turn gone quiet is idle", func(t *testing.T) {
		s := New(t.TempDir())
		s.OpenTurn("a")
		spend(s, stopCap)
		ageTurn(t, s, "a", 2*quiet)
		if !s.TurnIdle("a", stopCap, quiet) {
			t.Error("a spent turn that went quiet was never resumed")
		}
	})
	t.Run("a new prompt reopens a closed turn", func(t *testing.T) {
		s := New(t.TempDir())
		s.CloseTurn("a")
		s.OpenTurn("a")
		if s.TurnIdle("a", stopCap, quiet) {
			t.Error("an open turn was idle")
		}
	})
	t.Run("sessions are independent", func(t *testing.T) {
		s := New(t.TempDir())
		s.OpenTurn("a")
		if !s.TurnIdle("b", stopCap, quiet) {
			t.Error("another session's open turn held this one back")
		}
	})
}

func TestNoteSessionRoundTripsPerOwner(t *testing.T) {
	s := New(t.TempDir())
	if _, ok := s.SessionFor("o"); ok {
		t.Fatal("a session was found before any was noted")
	}
	hs := HarnessSession{Harness: "h", Session: "sid", Cwd: "/w", Mode: "m"}
	s.NoteSession("o", hs)
	if got, ok := s.SessionFor("o"); !ok || got != hs {
		t.Fatalf("SessionFor = %+v, %v; want %+v", got, ok, hs)
	}
	hs2 := HarnessSession{Harness: "h", Session: "sid2"}
	s.NoteSession("o", hs2)
	if got, _ := s.SessionFor("o"); got != hs2 {
		t.Errorf("a changed session was not re-recorded: %+v", got)
	}
	if _, ok := s.SessionFor("other"); ok {
		t.Error("one owner's session leaked to another")
	}
}
