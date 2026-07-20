package web

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/mirror"
)

// TestLandingUniqueHrefsOnSlugCollision (sty_57d5ce25 AC2/AC3): legacy
// partitions that share a basename slug still render distinct landing hrefs
// (repo_key fallback) that each resolve to a project page.
func TestLandingUniqueHrefsOnSlugCollision(t *testing.T) {
	s, err := mirror.Open(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ctx := context.Background()
	now := time.Now().UTC()
	if _, err := s.TouchPartition(ctx, "002-aaaa", "002", now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.TouchPartition(ctx, "002-bbbb", "002", now); err != nil {
		t.Fatal(err)
	}

	ms := NewMirror(s)
	srv := httptest.NewServer(ms.Handler)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("landing status %d", resp.StatusCode)
	}
	body := string(raw)

	re := regexp.MustCompile(`href="/r/([^"]+)/"`)
	matches := re.FindAllStringSubmatch(body, -1)
	if len(matches) < 2 {
		t.Fatalf("expected ≥2 project hrefs, got %v\n%s", matches, body)
	}
	seen := map[string]int{}
	for _, m := range matches {
		seen[m[1]]++
	}
	for slug, n := range seen {
		if n > 1 {
			t.Errorf("duplicate landing href /r/%s/ appears %d times", slug, n)
		}
	}
	// Colliding basename should not appear as the sole shared card.
	if seen["002"] > 0 {
		t.Errorf("colliding basename slug still used as href: %v", seen)
	}
	for _, hrefSlug := range []string{"002-aaaa", "002-bbbb"} {
		if seen[hrefSlug] != 1 {
			t.Errorf("want href /r/%s/ once, got %d (map %v)", hrefSlug, seen[hrefSlug], seen)
		}
		pr, err := http.Get(srv.URL + "/r/" + hrefSlug + "/")
		if err != nil {
			t.Fatal(err)
		}
		pr.Body.Close()
		if pr.StatusCode != 200 {
			t.Errorf("GET /r/%s/ status %d", hrefSlug, pr.StatusCode)
		}
	}
}

func TestDisplaySlugsUniqueOnCollision(t *testing.T) {
	parts := []mirror.Partition{
		{RepoKey: "a-1", Slug: "app"},
		{RepoKey: "a-2", Slug: "app"},
		{RepoKey: "b-1", Slug: "other"},
		{RepoKey: "c-1", Slug: ""},
	}
	got := displaySlugs(parts)
	if got["a-1"] != "a-1" || got["a-2"] != "a-2" {
		t.Errorf("colliding app slugs = %q / %q, want repo_keys", got["a-1"], got["a-2"])
	}
	if got["b-1"] != "other" {
		t.Errorf("unique slug = %q, want other", got["b-1"])
	}
	if got["c-1"] != "c-1" {
		t.Errorf("empty slug = %q, want repo_key", got["c-1"])
	}
}
