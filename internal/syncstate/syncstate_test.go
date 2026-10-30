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

func TestRecordSubstrateMergesOnlyItsGroupAndTheGivenAreas(t *testing.T) {
	home := t.TempDir()
	pushAt := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	first := time.Date(2026, 8, 1, 11, 0, 0, 0, time.UTC)
	second := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	if err := RecordPush(home, "/repo", true, "", "personal", pushAt); err != nil {
		t.Fatal(err)
	}
	if err := RecordSubstrate(home, "/repo", map[string]string{"skills": "behind", "documents": "in step"}, first); err != nil {
		t.Fatal(err)
	}
	if err := RecordSubstrate(home, "/repo", map[string]string{"skills": "in step"}, second); err != nil {
		t.Fatal(err)
	}
	if err := RecordSubstrate(home, "/repo", nil, second); err != nil {
		t.Fatal(err)
	}
	got, ok, err := Load(home, "/repo")
	if err != nil || !ok {
		t.Fatalf("load: ok=%v err=%v", ok, err)
	}
	if !got.PushLastSuccess.Equal(pushAt) {
		t.Fatalf("push group clobbered: %+v", got)
	}
	if a := got.SubstrateAreas["skills"]; a.State != "in step" || !a.CheckedAt.Equal(second) {
		t.Fatalf("skills = %+v, want in step at %v", a, second)
	}
	if a := got.SubstrateAreas["documents"]; a.State != "in step" || !a.CheckedAt.Equal(first) {
		t.Fatalf("documents must keep its own record: %+v", a)
	}
}

// RecordScope writes only Scope: a local pass neither clears an older failure
// nor records a success, and an empty scope writes nothing (sty_3df5f95a).
func TestRecordScopeSetsOnlyScope(t *testing.T) {
	home := t.TempDir()
	okAt := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	failAt := time.Date(2026, 8, 1, 11, 0, 0, 0, time.UTC)
	if err := RecordPush(home, "/repo", true, "", "personal", okAt); err != nil {
		t.Fatal(err)
	}
	if err := RecordPush(home, "/repo", false, "hosted 505", "", failAt); err != nil {
		t.Fatal(err)
	}
	if err := RecordScope(home, "/repo", "local"); err != nil {
		t.Fatal(err)
	}
	got, ok, err := Load(home, "/repo")
	if err != nil || !ok {
		t.Fatalf("load: ok=%v err=%v", ok, err)
	}
	if got.Scope != "local" {
		t.Fatalf("Scope = %q, want local", got.Scope)
	}
	if got.PushReason != "hosted 505" || !got.PushLastFailure.Equal(failAt) || !got.PushLastSuccess.Equal(okAt) || got.PushPasses != 1 {
		t.Fatalf("push leg changed: %+v", got)
	}

	if err := RecordScope(home, "/fresh", ""); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := Load(home, "/fresh"); err != nil || ok {
		t.Fatalf("empty scope wrote state: ok=%v err=%v", ok, err)
	}
}
