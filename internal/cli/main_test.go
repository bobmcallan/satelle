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
		initGateRun()
		root := NewRootCmd()
		root.SetArgs(os.Args[1:])
		c, err := root.ExecuteC()
		if c != nil {
			closeAppForCmd(c)
		}
		err, show := finishExecute(err)
		if err != nil && show {
			fmt.Fprintln(os.Stderr, err)
		}
		finishGateRun(err)
		if err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	// The suite runs with no terminal, which is an agent-facing caller: every
	// gated command would hand off to a detached copy of the test binary. The
	// hand-off has its own tests and turns itself on there (gatecaller_test.go);
	// everything else keeps the synchronous path it was written against.
	_ = os.Setenv(gateModeEnv, string(gateModeInteractive))
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
		config.RelayBindingEnv, config.RelayItemEnv, config.SpawnEnv,
	} {
		_ = os.Unsetenv(k)
	}
	os.Exit(runIsolated(m))
}

// runIsolated runs the package's tests with XDG_CONFIG_HOME pointing at a
// process-lifetime temp dir. Any command a test drives through runRoot reads the
// per-user credentials file (the assignee lookup in openAppForCmd), so a test
// that never isolates it would otherwise resolve the operator's real
// ~/.config/satelle/credentials.toml — or write it, if it saves a credential
// (sty_18403814). A test that needs its own credential store still sets
// XDG_CONFIG_HOME with t.Setenv, which overrides this backstop.
func runIsolated(m *testing.M) int {
	dir, err := os.MkdirTemp("", "satelle-cli-xdg-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "satelle cli tests: temp XDG_CONFIG_HOME:", err)
		return 1
	}
	defer os.RemoveAll(dir)
	_ = os.Setenv("XDG_CONFIG_HOME", dir)
	recordGateDelivered = restoringRecorder(recordGateDelivered)
	return m.Run()
}
