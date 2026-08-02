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

// TestFreshnessShownForEveryPartition replaces TestStaleMirrorSaysSo. That test
// asserted the red badge from sty_e6e467fe was PRESENT on an unreconciled
// partition and ABSENT on a fresh one — which is the behaviour sty_226a661e
// deliberately reverses, so it is inverted here rather than amended.
//
// What must still hold is the thing the badge existed for: a view that was not
// confirmed recently is never indistinguishable from one that was. The elapsed
// time now says it on EVERY row, continuously, instead of a binary flag that
// appeared only past StaleAfter.
func TestFreshnessShownForEveryPartition(t *testing.T) {
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

	// AC2: the badge is gone, from markup and from wording.
	for _, gone := range []string{"stale-flag", "stale · last update"} {
		if strings.Contains(landing, gone) {
			t.Errorf("the landing still renders %q:\n%s", gone, landing)
		}
	}
	// AC1: a column, and a stamp on EVERY row — not only the stale one. Two
	// partitions were seeded, so two rel-times; the old badge would have been 1.
	if !strings.Contains(landing, "<th>Updated</th>") {
		t.Error("the landing needs an Updated column header")
	}
	if n := strings.Count(landing, `class="rel-time"`); n != 2 {
		t.Errorf("every row carries its freshness: want 2 rel-times, got %d:\n%s", n, landing)
	}
	// AC3: the absolute stamp survives, on hover.
	if !strings.Contains(landing, `title="Last confirmed against the repository at `+stamp) {
		t.Errorf("the absolute last-ingest time %q must remain available on hover:\n%s", stamp, landing)
	}
	// AC4: the ticker needs a machine-readable instant, not a formatted string.
	if !strings.Contains(landing, `datetime="`+stale.UTC().Format(time.RFC3339)) {
		t.Errorf("the stale row needs an absolute datetime for the client ticker:\n%s", landing)
	}
	// AC1, the point of the change: the row reads as elapsed time, not a flag.
	// The fixture is StaleAfter (15m) + 1h, so floor-hours is exactly 1.
	if !strings.Contains(landing, ">1 hr ago</time>") {
		t.Errorf("the stale partition should read %q:\n%s", "1 hr ago", landing)
	}
	// …and the fresh one is distinguishable from it, which is what the binary
	// badge could not do below its threshold.
	if !strings.Contains(landing, ">just now</time>") {
		t.Errorf("the freshly confirmed partition should read %q:\n%s", "just now", landing)
	}

	// AC2, second surface: one fact, one presentation. Both project pages show
	// the freshness inline; neither shows a badge.
	for _, slug := range []string{"stale-repo", "fresh-repo"} {
		page := get(t, srv.URL+"/r/"+slug+"/")
		if strings.Contains(page, "stale-flag") {
			t.Errorf("%s project page still renders the badge:\n%s", slug, page)
		}
		if !strings.Contains(page, `class="rel-time"`) {
			t.Errorf("%s project page must show when it was last updated:\n%s", slug, page)
		}
	}
}

// TestFreshnessFragmentCarriesTimestamp proves AC6's precondition: the SSE
// soft-refresh swaps in /fragment/projects markup, so the fragment — not just
// the full page — has to carry the datetime the ticker re-renders from.
func TestFreshnessFragmentCarriesTimestamp(t *testing.T) {
	s, err := mirror.Open(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if _, err := s.TouchPartition(context.Background(), "rk", "repo", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(NewMirror(s).Handler)
	t.Cleanup(srv.Close)

	frag := get(t, srv.URL+"/fragment/projects")
	if !strings.Contains(frag, `class="updated-cell"`) || !strings.Contains(frag, `class="rel-time"`) {
		t.Errorf("the live fragment must carry the Updated cell:\n%s", frag)
	}
	if !strings.Contains(frag, `datetime="`) {
		t.Errorf("the live fragment must carry a machine-readable instant:\n%s", frag)
	}
}

// TestRelTimeBuckets pins the Go half of the wording contract. Its twin,
// TestAppJSMirrorsRelTimeWording, fences the JS half — together they are what
// makes AC7 checkable rather than merely intended.
func TestRelTimeBuckets(t *testing.T) {
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		ago  time.Duration
		want string
	}{
		{0, "just now"},
		{59 * time.Second, "just now"},
		{time.Minute, "1 min ago"},
		{119 * time.Second, "1 min ago"},
		{2 * time.Minute, "2 mins ago"},
		{59 * time.Minute, "59 mins ago"},
		{time.Hour, "1 hr ago"},
		{2 * time.Hour, "2 hrs ago"},
		{23*time.Hour + 59*time.Minute, "23 hrs ago"},
		{24 * time.Hour, "1 day ago"},
		{47 * time.Hour, "1 day ago"},
		{48 * time.Hour, "2 days ago"},
		{30 * 24 * time.Hour, "30 days ago"},
		// Clock skew between the ingesting repo and the serving tier: a stamp in
		// the future must never render as "in 3 minutes".
		{-5 * time.Minute, "just now"},
	} {
		if got := relTime(now.Add(-c.ago), now); got != c.want {
			t.Errorf("relTime(%s ago) = %q, want %q", c.ago, got, c.want)
		}
	}
	if got := relTime(time.Time{}, now); got != "never" {
		t.Errorf("a zero stamp renders %q, want %q", got, "never")
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
