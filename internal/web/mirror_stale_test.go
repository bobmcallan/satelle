package web

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/mirror"
)

// TestStaleMirrorSaysSo proves AC3 of sty_e6e467fe: a partition the serving tier
// could not reconcile is rendered with a visible staleness flag naming its
// last-ingest time — so a stale view is never indistinguishable from a stuck
// story — while a freshly confirmed partition renders no such claim.
func TestStaleMirrorSaysSo(t *testing.T) {
	s, err := mirror.Open(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ctx := context.Background()

	stale := time.Now().UTC().Add(-mirror.StaleAfter - time.Hour)
	if _, err := s.TouchPartition(ctx, "rk-stale", "stale-repo", stale); err != nil {
		t.Fatal(err)
	}
	if _, err := s.TouchPartition(ctx, "rk-fresh", "fresh-repo", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(NewMirror(s).Handler)
	t.Cleanup(srv.Close)

	stamp := stale.Local().Format("2006-01-02 15:04")

	landing := get(t, srv.URL+"/")
	if !strings.Contains(landing, "stale-flag") {
		t.Fatalf("landing must flag the unreconciled partition:\n%s", landing)
	}
	if !strings.Contains(landing, stamp) {
		t.Fatalf("landing flag must name the last-ingest time %q:\n%s", stamp, landing)
	}
	if !strings.Contains(landing, "satelle workspace add") {
		t.Error("the flag must carry the re-seed remedy")
	}
	if strings.Count(landing, "stale-flag") != 1 {
		t.Errorf("only the stale partition may be flagged, got %d flags", strings.Count(landing, "stale-flag"))
	}

	staleProject := get(t, srv.URL+"/r/stale-repo/")
	if !strings.Contains(staleProject, "stale-flag") || !strings.Contains(staleProject, stamp) {
		t.Fatalf("project page must flag a stale mirror with its last-ingest time:\n%s", staleProject)
	}

	freshProject := get(t, srv.URL+"/r/fresh-repo/")
	if strings.Contains(freshProject, "stale-flag") {
		t.Fatalf("a just-confirmed partition must not be flagged:\n%s", freshProject)
	}
}

func get(t *testing.T, url string) string {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status %d", url, resp.StatusCode)
	}
	return string(raw)
}
