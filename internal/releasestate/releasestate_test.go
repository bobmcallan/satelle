package releasestate

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadMissingIsNoState(t *testing.T) {
	s, ok, err := Load(t.TempDir())
	if err != nil || ok || s != (State{}) {
		t.Fatalf("Load(missing) = %+v, %v, %v; want zero, false, nil", s, ok, err)
	}
}

func TestRecordRoundTripKeepsAttempt(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)
	if err := MarkAttempt(dir, at); err != nil {
		t.Fatal(err)
	}
	want := State{CLILatest: "v0.0.2", ServeLatest: "serve-v0.0.9", ServeRunning: "0.0.8", CheckedAt: at.Add(time.Minute)}
	if err := Record(dir, want); err != nil {
		t.Fatal(err)
	}
	got, ok, err := Load(dir)
	if err != nil || !ok {
		t.Fatalf("Load = %v, %v", ok, err)
	}
	if !got.CheckedAt.Equal(want.CheckedAt) || !got.CheckedAttempt.Equal(at) ||
		got.CLILatest != want.CLILatest || got.ServeLatest != want.ServeLatest || got.ServeRunning != want.ServeRunning {
		t.Fatalf("round trip = %+v, want %+v (attempt %v)", got, want, at)
	}
}

func TestMarkAttemptLeavesReleaseFields(t *testing.T) {
	dir := t.TempDir()
	checked := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	if err := Record(dir, State{CLILatest: "v1.0.0", CheckedAt: checked}); err != nil {
		t.Fatal(err)
	}
	if err := MarkAttempt(dir, checked.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	got, _, _ := Load(dir)
	if got.CLILatest != "v1.0.0" || !got.CheckedAt.Equal(checked) || !got.CheckedAttempt.Equal(checked.Add(time.Hour)) {
		t.Fatalf("MarkAttempt disturbed state: %+v", got)
	}
}

func TestTornFileIsNoState(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, fileName), []byte("{\"cli_lat"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := Load(dir); ok || err != nil {
		t.Fatalf("torn file: ok=%v err=%v; want false, nil", ok, err)
	}
	// A writer recovers over a torn file.
	if err := MarkAttempt(dir, time.Now()); err != nil {
		t.Fatalf("MarkAttempt over torn file: %v", err)
	}
}
