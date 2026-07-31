package mirror

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestSnapshotIngestQuietWhenUnchanged proves the periodic reconcile is
// invisible when there is nothing to repair (sty_e6e467fe): re-posting an
// identical snapshot records freshness but rewrites no rows and rings no SSE
// doorbell, so a repair loop cannot make every open page re-render on a timer.
func TestSnapshotIngestQuietWhenUnchanged(t *testing.T) {
	s := openStore(t)
	var topics []string
	srv := httptest.NewServer(mountIngest(s, func(topic string) { topics = append(topics, topic) }))
	defer srv.Close()

	snap := Snapshot{
		RepoKey: "rk", Slug: "demo",
		Stories: []json.RawMessage{json.RawMessage(`{"id":"sty_1","status":"release"}`)},
	}
	if got := postSnapshot(t, srv.URL, snap); got["ok"] != true {
		t.Fatalf("first ingest: %v", got)
	}
	if len(topics) == 0 {
		t.Fatal("first ingest must ring the doorbell")
	}

	before := partitionOf(t, s, "rk")
	topics = nil
	time.Sleep(2 * time.Millisecond)

	got := postSnapshot(t, srv.URL, snap)
	if got["unchanged"] != true {
		t.Fatalf("identical snapshot must be reported unchanged: %v", got)
	}
	if len(topics) != 0 {
		t.Fatalf("identical snapshot must not ring the doorbell, got %v", topics)
	}
	after := partitionOf(t, s, "rk")
	if after.Seq != before.Seq {
		t.Errorf("seq moved on an unchanged snapshot: %d → %d", before.Seq, after.Seq)
	}
	if after.UpdatedAt == before.UpdatedAt {
		t.Error("freshness must move on an unchanged snapshot — that is what clears the stale badge")
	}
	if after.Stale(time.Now()) {
		t.Error("a just-confirmed partition must not read as stale")
	}
}

// TestSnapshotIngestAppliesChange proves a changed snapshot still replaces rows
// and rings the doorbell — the quiet path must never swallow a real update.
func TestSnapshotIngestAppliesChange(t *testing.T) {
	s := openStore(t)
	var topics []string
	srv := httptest.NewServer(mountIngest(s, func(topic string) { topics = append(topics, topic) }))
	defer srv.Close()

	postSnapshot(t, srv.URL, Snapshot{RepoKey: "rk", Slug: "demo",
		Stories: []json.RawMessage{json.RawMessage(`{"id":"sty_1","status":"release"}`)}})
	topics = nil

	got := postSnapshot(t, srv.URL, Snapshot{RepoKey: "rk", Slug: "demo",
		Stories: []json.RawMessage{json.RawMessage(`{"id":"sty_1","status":"done"}`)}})
	if _, quiet := got["unchanged"]; quiet {
		t.Fatalf("changed snapshot must not be reported unchanged: %v", got)
	}
	if len(topics) == 0 {
		t.Fatal("changed snapshot must ring the doorbell")
	}
	payload, err := s.GetItem(context.Background(), "rk", "story", "sty_1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(payload, `"done"`) {
		t.Fatalf("story not updated: %s", payload)
	}
}

// TestPartitionStaleness proves the threshold the UI reads: a partition that has
// gone longer than StaleAfter without a confirmed ingest is stale, a recent one
// is not, and an unparseable timestamp is treated as stale rather than current.
func TestPartitionStaleness(t *testing.T) {
	now := time.Now().UTC()
	fresh := Partition{UpdatedAt: now.Add(-time.Minute).Format(time.RFC3339Nano)}
	if fresh.Stale(now) {
		t.Error("a partition confirmed a minute ago is not stale")
	}
	if last, ok := fresh.LastIngest(); !ok || last.IsZero() {
		t.Error("LastIngest must parse a stored timestamp")
	}
	old := Partition{UpdatedAt: now.Add(-StaleAfter - time.Minute).Format(time.RFC3339Nano)}
	if !old.Stale(now) {
		t.Error("a partition past StaleAfter is stale")
	}
	if !(Partition{UpdatedAt: "not-a-time"}).Stale(now) {
		t.Error("an unreadable last-ingest time must read as stale, never as current")
	}
}

func openStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "mirror.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func mountIngest(s *Store, onChange func(string)) http.Handler {
	mux := http.NewServeMux()
	(&IngestHandler{Store: s, OnChange: onChange}).Mount(mux)
	return mux
}

func postSnapshot(t *testing.T, base string, snap Snapshot) map[string]any {
	t.Helper()
	body, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post(base+"/ingest/snapshot", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ingest status %d", resp.StatusCode)
	}
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

func partitionOf(t *testing.T, s *Store, repoKey string) Partition {
	t.Helper()
	p, ok, err := s.GetPartition(context.Background(), repoKey)
	if err != nil || !ok {
		t.Fatalf("partition %s: ok=%v err=%v", repoKey, ok, err)
	}
	return p
}
