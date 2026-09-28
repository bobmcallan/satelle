//go:build linux

package gatehandle

import (
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestParseProcStat(t *testing.T) {
	const tail = " S 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 12345 20 21"
	for name, comm := range map[string]string{
		"plain":       "(sleep)",
		"with spaces": "(my prog)",
		"with parens": "(a) b (c)",
	} {
		state, start, ok := parseProcStat("77 " + comm + tail)
		if !ok || state != "S" || start != "12345" {
			t.Errorf("%s: parseProcStat = %q %q %v", name, state, start, ok)
		}
	}
	if _, _, ok := parseProcStat("77 (short) S 1 2"); ok {
		t.Error("a truncated stat line parsed")
	}
	if _, _, ok := parseProcStat("garbage"); ok {
		t.Error("garbage parsed")
	}
}

func TestOSProbe_LinuxChildLifecycle(t *testing.T) {
	c := exec.Command("sleep", "30")
	if err := c.Start(); err != nil {
		t.Skipf("cannot start sleep: %v", err)
	}
	pid := c.Process.Pid
	live, start := osProbe(pid)
	if live != Alive || start == "" {
		t.Fatalf("live child: probe = %v %q", live, start)
	}
	if live2, start2 := osProbe(pid); live2 != Alive || start2 != start {
		t.Fatalf("identity is not stable: %q then %q", start, start2)
	}

	s := newStore(t)
	m, _ := s.Create(Meta{Verb: "x"})
	if err := s.SetPID(m.ID, pid); err != nil {
		t.Fatal(err)
	}
	if meta, _ := s.Meta(m.ID); meta.PIDStart != start || meta.IdentityUnavailable {
		t.Fatalf("SetPID did not record the identity: %+v", meta)
	}
	if got := s.State(m.ID); got != Running {
		t.Fatalf("state = %s, want running", got)
	}

	_ = c.Process.Kill()
	// Killed, not yet reaped: a zombie, which can never record a result.
	// Signal delivery is asynchronous, so poll to a deadline rather than probe once.
	live = Alive
	for deadline := time.Now().Add(5 * time.Second); live != Gone && time.Now().Before(deadline); {
		if live, _ = osProbe(pid); live != Gone {
			time.Sleep(10 * time.Millisecond)
		}
	}
	if live != Gone {
		t.Errorf("an unreaped killed child probes %v after 5s, want gone", live)
	}
	_ = c.Wait()
	if live, _ := osProbe(pid); live != Gone {
		t.Errorf("a reaped child probes %v, want gone", live)
	}
	if got := s.State(m.ID); got != Died {
		t.Fatalf("state = %s, want died", got)
	}
}

func TestOSProbe_LinuxOwnProcess(t *testing.T) {
	live, start := osProbe(os.Getpid())
	if live != Alive || start == "" {
		t.Fatalf("own process: probe = %v %q", live, start)
	}
	if live, _ := osProbe(0x7ffffff0); live != Gone {
		t.Fatalf("a pid nothing holds probes %v, want gone", live)
	}
}
