package syncstate

import (
	"os"
	"testing"
	"time"
)

func TestLoadMissingIsNotAnError(t *testing.T) {
	s, ok, err := Load(t.TempDir(), "/repo")
	if err != nil || ok {
		t.Fatalf("missing = (%+v, %v, %v)", s, ok, err)
	}
}

func TestRoundTrip(t *testing.T) {
	home := t.TempDir()
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	if err := RecordPush(home, "/repo", true, "", "personal", now); err != nil {
		t.Fatal(err)
	}
	got, ok, err := Load(home, "/repo")
	if err != nil || !ok {
		t.Fatalf("load: ok=%v err=%v", ok, err)
	}
	if !got.PushLastSuccess.Equal(now) || got.Scope != "personal" || got.PushReason != "" {
		t.Fatalf("got %+v", got)
	}
}

func TestPushMergeDoesNotClobberSnapshot(t *testing.T) {
	home := t.TempDir()
	snapAt := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	pushAt := time.Date(2026, 8, 1, 11, 0, 0, 0, time.UTC)
	if err := RecordSnapshot(home, "/repo", false, "snap fail", "", snapAt); err != nil {
		t.Fatal(err)
	}
	if err := RecordPush(home, "/repo", true, "", "", pushAt); err != nil {
		t.Fatal(err)
	}
	got, ok, err := Load(home, "/repo")
	if err != nil || !ok {
		t.Fatal(err)
	}
	if got.SnapshotReason != "snap fail" || !got.SnapshotLastFailure.Equal(snapAt) {
		t.Fatalf("snapshot clobbered: %+v", got)
	}
	if !got.PushLastSuccess.Equal(pushAt) {
		t.Fatalf("push missing: %+v", got)
	}
}

func TestTornFileIsNoState(t *testing.T) {
	home := t.TempDir()
	repo := "/repo"
	if err := RecordPush(home, repo, true, "", "", time.Now()); err != nil {
		t.Fatal(err)
	}
	p := pathFor(home, repo)
	if err := os.WriteFile(p, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, ok, err := Load(home, repo)
	if err != nil {
		t.Fatalf("torn must not error: %v", err)
	}
	if ok {
		t.Fatal("torn file must be ok=false")
	}
}

func TestRepeatedFailureIncrementsPasses(t *testing.T) {
	home := t.TempDir()
	now := time.Now()
	if err := RecordPush(home, "/r", false, "boom", "", now); err != nil {
		t.Fatal(err)
	}
	if err := RecordPush(home, "/r", false, "boom", "", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	got, _, err := Load(home, "/r")
	if err != nil {
		t.Fatal(err)
	}
	if got.PushPasses != 2 || got.PushReason != "boom" {
		t.Fatalf("got %+v", got)
	}
}
