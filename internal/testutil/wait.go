package testutil

import (
	"testing"
	"time"
)

// WaitBudget bounds a wait on work the test does not control (a goroutine, a
// child process, a server). Every wait built on it exits the moment its
// condition holds, so a fast machine never pays the budget; it only has to
// outlast the slowest CI runner so a test's verdict never depends on machine
// speed (sty_05017a43). Do not use it for a budget whose length is itself the
// subject of the test (a watchdog, a TTL).
const WaitBudget = 10 * time.Second

// PollTick is the pause between polls of a condition inside Eventually.
const PollTick = 2 * time.Millisecond

// Eventually polls cond until it holds and fails the test with msg if it has
// not within budget. The exit is the observable condition, never the clock.
func Eventually(t testing.TB, budget time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(budget)
	for {
		if cond() {
			return
		}
		if !time.Now().Before(deadline) {
			t.Fatalf("%s (not met within %s)", msg, budget)
		}
		time.Sleep(PollTick) // poll tick
	}
}
