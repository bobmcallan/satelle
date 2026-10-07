package hosted

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"testing"
	"time"
)

// A story the server has never ingested is a typed not-found, distinct from any
// other non-200 answer, so engage can tell "never pushed" from "broken"
// (sty_52eb8c2f).
func TestHoldNotFoundIsTyped(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/projects/{project}/workstate/items/{id}", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("id") == "sty_broken" {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		http.NotFound(w, r)
	})
	mux.HandleFunc("POST /api/v1/projects/{project}/workstate/items/{id}/checkout", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	c := holdTestClient(t, mux)

	if _, err := c.ItemHold(context.Background(), "p", "sty_new"); !errors.Is(err, ErrItemNotFound) {
		t.Fatalf("ItemHold 404 = %v, want ErrItemNotFound", err)
	}
	if _, err := c.Checkout(context.Background(), "p", "sty_new"); !errors.Is(err, ErrItemNotFound) {
		t.Fatalf("Checkout 404 = %v, want ErrItemNotFound", err)
	}
	_, err := c.ItemHold(context.Background(), "p", "sty_broken")
	if err == nil || errors.Is(err, ErrItemNotFound) {
		t.Fatalf("ItemHold 500 = %v, must be an error that is not ErrItemNotFound", err)
	}
}

func TestHoldSince(t *testing.T) {
	entry := func(action, loc, at string) map[string]any {
		return map[string]any{"action": action, "location_id": loc, "at": at}
	}
	cases := []struct {
		name   string
		log    []map[string]any
		holder string
		want   string
		ok     bool
	}{
		{"empty log", nil, "loc_a", "", false},
		{"checkout by holder", []map[string]any{entry("checkout", "loc_a", "2026-10-01T00:00:00Z")}, "loc_a", "2026-10-01T00:00:00Z", true},
		{"takeover counts", []map[string]any{entry("takeover", "loc_a", "2026-10-02T00:00:00Z")}, "loc_a", "2026-10-02T00:00:00Z", true},
		{"other location only", []map[string]any{entry("checkout", "loc_b", "2026-10-01T00:00:00Z")}, "loc_a", "", false},
		{"earliest since last release", []map[string]any{
			entry("checkout", "loc_a", "2026-09-01T00:00:00Z"),
			entry("release", "loc_a", "2026-09-02T00:00:00Z"),
			entry("checkout", "loc_a", "2026-10-03T00:00:00Z"),
			entry("checkout", "loc_a", "2026-10-04T00:00:00Z"),
		}, "loc_a", "2026-10-03T00:00:00Z", true},
		{"released and never re-held", []map[string]any{
			entry("checkout", "loc_a", "2026-09-01T00:00:00Z"),
			entry("release", "loc_a", "2026-09-02T00:00:00Z"),
		}, "loc_a", "", false},
		{"unparseable time", []map[string]any{entry("checkout", "loc_a", "yesterday")}, "loc_a", "", false},
		{"created_at spelling", []map[string]any{{"action": "checkout", "location_id": "loc_a", "created_at": "2026-10-05T00:00:00Z"}}, "loc_a", "2026-10-05T00:00:00Z", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := HoldSince(c.log, c.holder)
			if got != c.want || ok != c.ok {
				t.Fatalf("HoldSince = %q,%v want %q,%v", got, ok, c.want, c.ok)
			}
		})
	}
}

func TestPendingClaimsRoundTrip(t *testing.T) {
	DocumentSyncStatePathOverride = filepath.Join(t.TempDir(), "document-sync-state.json")
	t.Cleanup(func() { DocumentSyncStatePathOverride = "" })
	at := time.Date(2026, 10, 7, 1, 2, 3, 0, time.UTC)
	if err := RecordPendingClaim("https://s.example", "p", "/repo", "sty_a", at); err != nil {
		t.Fatal(err)
	}
	// A second record keeps the first engage time.
	if err := RecordPendingClaim("https://s.example", "p", "/repo", "sty_a", at.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	got, err := PendingClaims("https://s.example", "p", "/repo")
	if err != nil || got["sty_a"] != "2026-10-07T01:02:03Z" {
		t.Fatalf("pending = %v %v", got, err)
	}
	if other, _ := PendingClaims("https://s.example", "p", "/other"); len(other) != 0 {
		t.Fatalf("pending leaked across repos: %v", other)
	}
	if err := ClearPendingClaim("https://s.example", "p", "/repo", "sty_a"); err != nil {
		t.Fatal(err)
	}
	if got, _ = PendingClaims("https://s.example", "p", "/repo"); len(got) != 0 {
		t.Fatalf("cleared claim still pending: %v", got)
	}
}
