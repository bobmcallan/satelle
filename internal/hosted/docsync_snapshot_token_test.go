package hosted

import (
	"path/filepath"
	"testing"
)

func TestSnapshotTokenRoundTrip(t *testing.T) {
	DocumentSyncStatePathOverride = filepath.Join(t.TempDir(), "document-sync-state.json")
	t.Cleanup(func() { DocumentSyncStatePathOverride = "" })

	got, err := LoadSnapshotToken("https://s.example", "p", "/repo/a")
	if err != nil || got.Token != "" {
		t.Fatalf("empty store = %+v %v", got, err)
	}
	want := SnapshotToken{Token: "t1", Areas: "ledger,stories", ResumePageToken: "pg", ResumeSince: "t0", Pending: map[string]string{"sty_1": "done"}}
	if err := SaveSnapshotToken("https://s.example", "p", "/repo/a", want); err != nil {
		t.Fatal(err)
	}
	// Keyed per (server, project, repo): another repo or project sees nothing.
	if other, _ := LoadSnapshotToken("https://s.example", "p", "/repo/b"); other.Token != "" {
		t.Fatalf("other repo leaked %+v", other)
	}
	if other, _ := LoadSnapshotToken("https://s.example", "q", "/repo/a"); other.Token != "" {
		t.Fatalf("other project leaked %+v", other)
	}
	got, err = LoadSnapshotToken("https://s.example", "p", "/repo/a")
	if err != nil || got.Token != "t1" || got.Areas != "ledger,stories" || got.ResumePageToken != "pg" ||
		got.ResumeSince != "t0" || got.Pending["sty_1"] != "done" {
		t.Fatalf("load = %+v %v", got, err)
	}
	// Saving a snapshot token must not disturb the push cursor beside it.
	if err := SaveWorkstateCursor("https://s.example", "p", "/repo/a", WorkstateCursor{LedgerSeq: 7}); err != nil {
		t.Fatal(err)
	}
	if got, _ = LoadSnapshotToken("https://s.example", "p", "/repo/a"); got.Token != "t1" {
		t.Fatalf("cursor save clobbered token: %+v", got)
	}
	if err := ClearSnapshotToken("https://s.example", "p", "/repo/a"); err != nil {
		t.Fatal(err)
	}
	if got, _ = LoadSnapshotToken("https://s.example", "p", "/repo/a"); got.Token != "" {
		t.Fatalf("clear left %+v", got)
	}
	if c, _ := LoadWorkstateCursor("https://s.example", "p", "/repo/a"); c.LedgerSeq != 7 {
		t.Fatalf("clear removed the push cursor: %+v", c)
	}
}
