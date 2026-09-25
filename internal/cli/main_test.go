package cli

import (
	"fmt"
	"os"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
)

// TestMain re-execs this test binary as the satelle CLI when
// SATELLE_CLI_REEXEC=1, so an end-to-end test can hand a child process a real
// `satelle` without building one. Every other run falls straight through to
// the package's tests.
func TestMain(m *testing.M) {
	if os.Getenv("SATELLE_CLI_REEXEC") == "1" {
		root := NewRootCmd()
		root.SetArgs(os.Args[1:])
		c, err := root.ExecuteC()
		if c != nil {
			closeAppForCmd(c)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	// Isolate gate/seat tests from the AMBIENT process environment: when this
	// test binary itself runs under a real satelle dispatch (a named-agent
	// step, or — as when this test was written — a rework-relay coder session
	// working on THIS story), SATELLE_DISPATCH_*/SATELLE_RELAY_* are already
	// set on the host process. hookDenyReason and friends read them via
	// os.Getenv with no test-injection seam, so an unrelated gate test that
	// never calls t.Setenv would otherwise silently pick up a REAL dispatch
	// marker and take the relay-deny branch instead of the plain one it
	// asserts on (sty_752c4ef2 rework-relay review round). Clear them once
	// here so every test starts from a clean marker state; a test that wants
	// one sets it itself via t.Setenv.
	for _, k := range []string{
		config.DispatchAgentEnv, config.DispatchStepEnv, config.DispatchItemEnv,
		config.RelayBindingEnv, config.RelayItemEnv,
	} {
		_ = os.Unsetenv(k)
	}
	os.Exit(m.Run())
}
