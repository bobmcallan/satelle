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
