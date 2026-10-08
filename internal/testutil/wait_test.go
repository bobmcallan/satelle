package testutil

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestEventuallyReturnsWhenConditionHolds(t *testing.T) {
	var flag atomic.Bool
	go func() { flag.Store(true) }()
	Eventually(t, WaitBudget, flag.Load, "flag never set")
}

func TestEventuallyReturnsImmediatelyWhenAlreadyTrue(t *testing.T) {
	start := time.Now()
	Eventually(t, WaitBudget, func() bool { return true }, "never")
	if time.Since(start) >= WaitBudget {
		t.Fatal("an already-true condition must not wait out the budget")
	}
}
