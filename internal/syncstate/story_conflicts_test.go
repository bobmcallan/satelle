package syncstate

import (
	"os"
	"testing"
	"time"
)

func TestRecordStoryConflicts(t *testing.T) {
	home := t.TempDir()
	t0 := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Hour)

	// Nothing to say writes nothing.
	if err := RecordStoryConflicts(home, "/repo", nil, nil, t0); err != nil {
		t.Fatal(err)
	}
	if err := RecordStoryConflicts(home, "/repo", nil, []string{"sty_x"}, t0); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(pathFor(home, "/repo")); !os.IsNotExist(err) {
		t.Fatalf("an in-step repo must gain no state file: %v", err)
	}

	if err := RecordSnapshot(home, "/repo", true, "", "", t0); err != nil {
		t.Fatal(err)
	}
	if err := RecordStoryConflicts(home, "/repo", map[string]string{"sty_a": "local ahead", "sty_b": "pull refused"}, nil, t0); err != nil {
		t.Fatal(err)
	}
	s, _, _ := Load(home, "/repo")
	if len(s.StoryConflicts) != 2 || s.StoryConflicts["sty_a"].Reason != "local ahead" || !s.StoryConflicts["sty_a"].At.Equal(t0) {
		t.Fatalf("conflicts = %+v", s.StoryConflicts)
	}
	if !s.SnapshotLastSuccess.Equal(t0) {
		t.Fatalf("the snapshot group was clobbered: %+v", s)
	}

	// Seen again with the same reason keeps its first time; one is cleared.
	if err := RecordStoryConflicts(home, "/repo", map[string]string{"sty_a": "local ahead"}, []string{"sty_b"}, t1); err != nil {
		t.Fatal(err)
	}
	s, _, _ = Load(home, "/repo")
	if len(s.StoryConflicts) != 1 || !s.StoryConflicts["sty_a"].At.Equal(t0) {
		t.Fatalf("conflicts = %+v", s.StoryConflicts)
	}

	if err := RecordStoryConflicts(home, "/repo", nil, []string{"sty_a"}, t1); err != nil {
		t.Fatal(err)
	}
	if s, _, _ = Load(home, "/repo"); len(s.StoryConflicts) != 0 {
		t.Fatalf("cleared conflict still present: %+v", s.StoryConflicts)
	}
}

// A conflict records the holders and source that found it (sty_199ad82b); seen
// again it keeps its first time, and a reason-only record of it keeps its holders.
func TestRecordStoryConflictDetails(t *testing.T) {
	home := t.TempDir()
	t0 := time.Date(2026, 10, 30, 8, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Hour)
	c := StoryConflict{Reason: "forked", LocalHolder: "loc_a", RemoteHolder: "desk", Source: "conflict"}
	if err := RecordStoryConflictDetails(home, "/repo", map[string]StoryConflict{"sty_a": c}, nil, t0); err != nil {
		t.Fatal(err)
	}
	want := c
	want.At = t0
	if s, _, _ := Load(home, "/repo"); s.StoryConflicts["sty_a"] != want {
		t.Fatalf("conflict = %+v, want %+v", s.StoryConflicts["sty_a"], want)
	}
	if err := RecordStoryConflicts(home, "/repo", map[string]string{"sty_a": "forked"}, nil, t1); err != nil {
		t.Fatal(err)
	}
	if s, _, _ := Load(home, "/repo"); s.StoryConflicts["sty_a"] != want {
		t.Fatalf("a reason-only record changed it to %+v, want %+v", s.StoryConflicts["sty_a"], want)
	}
	moved := StoryConflict{Reason: "forked", LocalHolder: "loc_a", RemoteHolder: "laptop", Source: "conflict"}
	if err := RecordStoryConflictDetails(home, "/repo", map[string]StoryConflict{"sty_a": moved}, nil, t1); err != nil {
		t.Fatal(err)
	}
	moved.At = t0
	if s, _, _ := Load(home, "/repo"); s.StoryConflicts["sty_a"] != moved {
		t.Fatalf("conflict = %+v, want the new holders with the first time %+v", s.StoryConflicts["sty_a"], moved)
	}
}
