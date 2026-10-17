package verb

import (
	"context"
	"testing"
	"time"
)

// StubSiblingRegistry stands a workspace registry in for the real one: registry
// maps a repo name to the roots registered under it, and current is the invoking
// repo's root. The real lookups return on cleanup.
func StubSiblingRegistry(t testing.TB, registry map[string][]string, current string) {
	t.Helper()
	prevFind, prevCurrent := findRegistered, currentRepoRoot
	findRegistered = func(name string) ([]string, error) { return registry[name], nil }
	currentRepoRoot = func() string { return current }
	t.Cleanup(func() { findRegistered, currentRepoRoot = prevFind, prevCurrent })
}

// ForceStatus moves a story's status behind a writer's back, as a racing writer
// would, so a compare-and-set write against the status it read must fail.
func ForceStatus(ctx context.Context, id, status string) error {
	s, err := requireWorkItem()
	if err != nil {
		return err
	}
	_, err = s.SetStatus(ctx, id, status, time.Now())
	return err
}
