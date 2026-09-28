//go:build windows

package gatehandle

import (
	"os/exec"
	"testing"
)

// Runs only on a windows host: there is no windows CI runner, so cross-GOOS vet
// is the compile check elsewhere.
func TestOSProbe_WindowsChildLifecycle(t *testing.T) {
	c := exec.Command("cmd", "/c", "ping -n 30 127.0.0.1")
	if err := c.Start(); err != nil {
		t.Skipf("cannot start a child: %v", err)
	}
	pid := c.Process.Pid
	live, start := osProbe(pid)
	if live != Alive || start == "" {
		t.Fatalf("live child: probe = %v %q", live, start)
	}
	_ = c.Process.Kill()
	_ = c.Wait()
	if live, _ := osProbe(pid); live != Gone {
		t.Errorf("a reaped child probes %v, want gone", live)
	}
	if live, _ := probeProcess(0); live != Gone {
		t.Errorf("pid 0 probes %v, want gone", live)
	}
}
