//go:build darwin

package gatehandle

import (
	"os"
	"os/exec"
	"testing"
)

// Runs only on a darwin host: there is no darwin CI runner, so cross-GOOS vet is
// the compile check elsewhere.
func TestOSProbe_DarwinChildLifecycle(t *testing.T) {
	c := exec.Command("sleep", "30")
	if err := c.Start(); err != nil {
		t.Skipf("cannot start sleep: %v", err)
	}
	pid := c.Process.Pid
	live, start := osProbe(pid)
	if live != Alive || start == "" {
		t.Fatalf("live child: probe = %v %q", live, start)
	}
	if _, again := osProbe(pid); again != start {
		t.Fatalf("identity is not stable: %q then %q", start, again)
	}
	_ = c.Process.Kill()
	_ = c.Wait()
	if live, _ := osProbe(pid); live != Gone {
		t.Errorf("a reaped child probes %v, want gone", live)
	}
	if live, start := osProbe(os.Getpid()); live != Alive || start == "" {
		t.Errorf("own process: probe = %v %q", live, start)
	}
}
