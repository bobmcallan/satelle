//go:build integration

package tests

import (
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

// TestBrowserOrderTagSort drives the real page to prove the Stories list sorts by
// the numeric order:<N> tag when `order:order` is requested — ascending 1, 2, 10
// (NOT lexicographic) with a no-order story LAST — fixing the bug where the sort
// silently fell back to updated-desc because `order` was not a known sort field
// (sty_283f9f1e).
func TestBrowserOrderTagSort(t *testing.T) {
	ctx := newChrome(t)
	base, repo := serveRepo(t, "8811")

	// Seed OUT of order so a correct sort must reorder them. order:10 guards against
	// a lexicographic regression (10 must follow 2, not precede it).
	mk := func(title, tags string) string {
		out := mustRun(t, testBin, repo, "story", "create",
			"--title", title, "--body", "the goal", "--acceptance", "1. it does X", "--tags", tags)
		return extractID(out, "sty_")
	}
	id2 := mk("Second", "ordtest,order:2")
	id10 := mk("Tenth", "ordtest,order:10")
	id1 := mk("First", "ordtest,order:1")
	// Two stories share order:3 — the TIE case. The tie-break is the row id
	// (data-expand-url) ascending, so their relative order is deterministic.
	id3a := mk("ThreeA", "ordtest,order:3")
	id3b := mk("ThreeB", "ordtest,order:3")
	idN := mk("NoOrder", "ordtest")
	tie := []string{id3a, id3b}
	sort.Strings(tie) // expand-url shares a constant prefix, so id order == url order

	if err := chromedp.Run(ctx,
		chromedp.Navigate(base),
		chromedp.WaitVisible(`#panel-stories table.panel-table`, chromedp.ByQuery),
		setInput(`#panel-stories .filterbar input`, "tags:ordtest order:order status:all"),
	); err != nil {
		t.Fatal(err)
	}
	if !waitCond(t, ctx,
		`[...document.querySelectorAll('#panel-stories tr.row')].filter(r=>r.offsetParent!==null).length===6`,
		5*time.Second) {
		t.Fatal("expected exactly 6 filtered rows for tags:ordtest")
	}
	var urls []string
	if err := chromedp.Run(ctx, chromedp.Evaluate(
		`[...document.querySelectorAll('#panel-stories tr.row')].filter(r=>r.offsetParent!==null).map(r=>r.dataset.expandUrl)`,
		&urls)); err != nil {
		t.Fatal(err)
	}
	// ascending 1, 2, then the order:3 tie (id-ascending), then 10, then no-order last.
	want := []string{id1, id2, tie[0], tie[1], id10, idN}
	if len(urls) != 6 {
		t.Fatalf("want 6 rows, got %d: %v", len(urls), urls)
	}
	for i, id := range want {
		if !strings.HasSuffix(urls[i], id) {
			t.Errorf("row %d = %q, want story %s — order must be 1,2,10,no-order, got order: %v", i, urls[i], id, urls)
		}
	}
}
