package syncstate

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestClearPushReasonIf(t *testing.T) {
	isRefusal := func(r string) bool { return strings.Contains(r, "refused") }
	failedAt := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)

	t.Run("a matching reason is cleared and the failure time kept", func(t *testing.T) {
		home, repo := t.TempDir(), t.TempDir()
		if err := RecordPush(home, repo, false, "push refused by the gate", "", failedAt); err != nil {
			t.Fatal(err)
		}
		if err := ClearPushReasonIf(home, repo, isRefusal); err != nil {
			t.Fatal(err)
		}
		s, _, _ := Load(home, repo)
		if s.PushReason != "" || s.PushPasses != 0 {
			t.Errorf("matching reason must be cleared: %+v", s)
		}
		if !s.PushLastFailure.Equal(failedAt) {
			t.Errorf("the failure time is history and must survive: %v", s.PushLastFailure)
		}
	})

	t.Run("a non-matching reason is kept", func(t *testing.T) {
		home, repo := t.TempDir(), t.TempDir()
		if err := RecordPush(home, repo, false, "hosted down", "", failedAt); err != nil {
			t.Fatal(err)
		}
		if err := ClearPushReasonIf(home, repo, isRefusal); err != nil {
			t.Fatal(err)
		}
		if s, _, _ := Load(home, repo); s.PushReason != "hosted down" || s.PushPasses != 1 {
			t.Errorf("a genuine failure must survive: %+v", s)
		}
	})

	t.Run("no state writes no file", func(t *testing.T) {
		home, repo := t.TempDir(), t.TempDir()
		if err := ClearPushReasonIf(home, repo, isRefusal); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(pathFor(home, repo)); !os.IsNotExist(err) {
			t.Errorf("a repo with no state must gain no file: %v", err)
		}
	})
}
