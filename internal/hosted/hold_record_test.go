package hosted

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func isolateDocState(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "document-sync-state.json")
	DocumentSyncStatePathOverride = path
	t.Cleanup(func() { DocumentSyncStatePathOverride = "" })
	return path
}

// A registry written before the record grew label and last-seen held the bare
// holder location; it must still load, and the next write upgrades it.
func TestLoadHoldsReadsLegacyStringValue(t *testing.T) {
	path := isolateDocState(t)
	key := documentCursorKey("https://s.example", "p", "/repo")
	legacy := `{"cursors":{},"holds":{"` + key + `":{"sty_a":"loc_other"}}}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadHolds("https://s.example", "p", "/repo")
	if err != nil {
		t.Fatal(err)
	}
	if got["sty_a"].Location != "loc_other" || got["sty_a"].Label != "" {
		t.Fatalf("legacy value = %+v", got["sty_a"])
	}
	now := time.Date(2026, 10, 14, 1, 2, 3, 0, time.UTC)
	rec := HoldRecordFrom(HoldState{LocationID: "loc_other", Label: "laptop", LastSeenAt: "2026-10-14T00:00:00Z"}, now)
	if err := RecordHold("https://s.example", "p", "/repo", "sty_a", rec); err != nil {
		t.Fatal(err)
	}
	got, _ = LoadHolds("https://s.example", "p", "/repo")
	if got["sty_a"] != rec {
		t.Fatalf("upgraded record = %+v, want %+v", got["sty_a"], rec)
	}
}

func TestReconcileHolds(t *testing.T) {
	isolateDocState(t)
	const srv, proj, repo, self = "https://s.example", "p", "/repo", "loc_self"
	now := time.Now().UTC()
	mine := HoldRecord{Location: self, CheckedAt: now}
	theirs := HoldRecord{Location: "loc_other", Label: "laptop", LastSeen: "2026-10-14T00:00:00Z", CheckedAt: now}
	if err := RecordHold(srv, proj, repo, "sty_mine", mine); err != nil {
		t.Fatal(err)
	}
	if err := ReconcileHolds(srv, proj, repo, self, map[string]HoldRecord{"sty_a": theirs, "sty_b": theirs}, []string{"sty_a", "sty_b"}); err != nil {
		t.Fatal(err)
	}
	got, _ := LoadHolds(srv, proj, repo)
	if got["sty_a"] != theirs || got["sty_b"] != theirs {
		t.Fatalf("found holds not recorded: %+v", got)
	}
	// sty_a released; sty_mine was checked and reported unheld, but it is this
	// checkout's own, which the release path owns.
	if err := ReconcileHolds(srv, proj, repo, self, map[string]HoldRecord{"sty_b": theirs}, []string{"sty_a", "sty_b", "sty_mine"}); err != nil {
		t.Fatal(err)
	}
	got, _ = LoadHolds(srv, proj, repo)
	if _, ok := got["sty_a"]; ok {
		t.Fatalf("a released hold must be cleared: %+v", got)
	}
	if got["sty_b"] != theirs || got["sty_mine"] != mine {
		t.Fatalf("held and own entries must stay: %+v", got)
	}
	// An id not checked is left alone.
	if err := ReconcileHolds(srv, proj, repo, self, nil, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ = LoadHolds(srv, proj, repo); got["sty_b"] != theirs {
		t.Fatalf("an unchecked hold must stay: %+v", got)
	}
}

func TestHoldPresumedDead(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name, seen string
		dead       bool
		age        time.Duration
	}{
		{"empty", "", false, 0},
		{"unparsable", "yesterday", false, 0},
		{"fresh", "2026-10-14T11:30:00Z", false, 30 * time.Minute},
		{"stale", "2026-10-14T09:00:00Z", true, 3 * time.Hour},
		{"exactly at the threshold", "2026-10-14T10:00:00Z", false, 2 * time.Hour},
		{"in the future", "2026-10-14T13:00:00Z", false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dead, age := HoldPresumedDead(tc.seen, 2*time.Hour, now)
			if dead != tc.dead || age != tc.age {
				t.Errorf("HoldPresumedDead(%q) = %v, %v; want %v, %v", tc.seen, dead, age, tc.dead, tc.age)
			}
		})
	}
}
