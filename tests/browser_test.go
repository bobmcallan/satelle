//go:build integration

// Browser-driven end-to-end tests: they launch the real satelle binary's web
// server and drive it in headless Chrome (chromedp), exercising the actual
// rendered page + JavaScript the user sees — tab switching, inline expand on
// click, live filtering, and realtime updates pushed from a separate CLI
// process. This is the front end under automation, not eyeballing.
//
// Requires a Chrome/Chromium binary; the test skips with a clear message if
// none is found (CI installs one). Part of the `integration` tag.
package tests

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

// findBrowser returns a Chrome/Chromium executable path, or "".
func findBrowser() string {
	if p := os.Getenv("SATELLE_CHROME"); p != "" {
		return p
	}
	for _, c := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser"} {
		if p, err := exec.LookPath(c); err == nil {
			return p
		}
	}
	return ""
}

// serveRepo inits a temp repo, seeds it, starts `satelle serve` on a free-ish
// port, waits until healthy, and returns the project-page base URL + repo path.
// Cleanup stops the server.
//
// serve is always adaptive: the root (/) is the connected-projects landing and
// EVERY repo — including a lone one — is served under its own /<slug>/. So the
// returned base is host+/<slug> (slug == the tempdir basename), making every
// base+"/…" path target this repo's child consistently (project page, detail
// pages, fragments, SSE), with the prefixed <base href> the page itself uses.
func serveRepo(t *testing.T, port string) (string, string) {
	t.Helper()
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	stubReviewerAccept(t, repo) // baseline gates are active (sty_5b8bd8b2) — keep hermetic
	cmd := exec.Command(testBin, "serve", "--port", port)
	cmd.Dir = repo
	// Isolate the machine-wide registry so `serve` doesn't pick up unrelated repos
	// and spawn extra child servers during these single-repo tests.
	cmd.Env = append(os.Environ(), "SATELLE_HOME="+t.TempDir())
	if err := cmd.Start(); err != nil {
		t.Fatalf("start serve: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})
	host := "http://127.0.0.1:" + port
	if !waitHealthy(t, host+"/healthz", 5*time.Second) {
		t.Fatal("server did not become healthy")
	}
	return host + "/" + filepath.Base(repo), repo
}

// newChrome returns a chromedp context (and overall timeout) for the suite.
func newChrome(t *testing.T) context.Context {
	t.Helper()
	browser := findBrowser()
	if browser == "" {
		t.Skip("no Chrome/Chromium found (set SATELLE_CHROME or install google-chrome); skipping browser e2e")
	}
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(browser),
		chromedp.NoSandbox,
		chromedp.DisableGPU,
		chromedp.Flag("disable-dev-shm-usage", true),
		// Chrome 132+ removed the legacy headless mode chromedp defaults to;
		// the new mode is required or the connection hangs.
		chromedp.Flag("headless", "new"),
	)
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(), opts...)
	t.Cleanup(cancelAlloc)
	ctx, cancelCtx := chromedp.NewContext(allocCtx)
	t.Cleanup(cancelCtx)
	ctx, cancelTimeout := context.WithTimeout(ctx, 60*time.Second)
	t.Cleanup(cancelTimeout)
	return ctx
}

func TestBrowserProjectPageInteractions(t *testing.T) {
	base, repo := serveRepo(t, "8801")

	// Seed: one open story, one done story (so the default status:open filter is
	// observable), one task, and an authored doc.
	openID := createStory(t, repo, "Keep Me Open", "")
	doneID := createStory(t, repo, "Already Done", "done")
	mustRun(t, testBin, repo, "task", "create", "--title", "A task to do")
	if err := os.WriteFile(filepath.Join(repo, ".satelle", "documents", "guide.md"), []byte("# Guide\n\nhello"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Seed an on-disk workflow so the Workflow panel has a row: embedded defaults are
	// not listed (sty_94da9ac9), so a fresh repo's panel would otherwise be empty.
	wfBody := "---\nname: wf-x\ntype: workflow\nscope: project\napplies_to: [\"*\"]\n---\n" +
		"```dot\n" + "digraph w {\n  backlog [shape=Mdiamond]\n  done [shape=Msquare, agent=reviewer, prompt=\"@skill:satelle-story-done-review\"]\n  backlog -> done\n}\n" + "```\n"
	if err := os.WriteFile(filepath.Join(repo, ".satelle", "workflows", "wf-x.md"), []byte(wfBody), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, testBin, repo, "index")
	_ = doneID

	ctx := newChrome(t)
	// Wait on a signal that the page loaded AND app.js initialized (it sets
	// aria-selected on the active tab) — not on a specific row, which the
	// default status:open filter may have hidden.
	if err := chromedp.Run(ctx,
		chromedp.Navigate(base+"/"),
		chromedp.WaitVisible(`.tab[data-panel="stories"][aria-selected="true"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`#panel-stories table.panel-table`, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("load page: %v", err)
	}

	t.Run("default_filter_hides_terminal", func(t *testing.T) {
		// The done story must be hidden under the default status:active filter (it
		// hides terminal rows); the non-terminal story visible.
		if visibleRow(t, ctx, openID) != true {
			t.Errorf("active story %s should be visible by default", openID)
		}
		if visibleRow(t, ctx, doneID) != false {
			t.Errorf("done story %s should be hidden by default (status:active)", doneID)
		}
		// A default status:active chip is rendered.
		if !hasChip(t, ctx, "stories", "status:active") {
			t.Error("expected default status:active chip")
		}
		// The default sort is surfaced the same way: an order:updated chip.
		if !hasChip(t, ctx, "stories", "order:updated") {
			t.Error("expected default order:updated chip")
		}
	})

	t.Run("status_all_reveals_terminal", func(t *testing.T) {
		if err := chromedp.Run(ctx, setInput(`#panel-stories .filterbar input`, "status:all")); err != nil {
			t.Fatal(err)
		}
		if !waitCond(t, ctx, jsRowVisible(doneID), 3*time.Second) {
			t.Errorf("status:all should reveal the terminal story %s", doneID)
		}
		if !hasChip(t, ctx, "stories", "status:all") {
			t.Error("expected a status:all chip")
		}
		// Reset to the default for following subtests.
		if err := chromedp.Run(ctx, setInput(`#panel-stories .filterbar input`, "")); err != nil {
			t.Fatal(err)
		}
		waitCond(t, ctx, jsRowVisible(openID), 3*time.Second)
	})

	t.Run("clear_all_resets_filters", func(t *testing.T) {
		sel := `#panel-stories .chips .fchip-clear`
		// Absent when the filter input is empty (nothing to clear).
		if !waitCond(t, ctx, "!document.querySelector('"+sel+"')", 3*time.Second) {
			t.Error("clear-all should be absent on an empty filter input")
		}
		// An explicit filter makes the clear-all control appear.
		if err := chromedp.Run(ctx, setInput(`#panel-stories .filterbar input`, "status:all")); err != nil {
			t.Fatal(err)
		}
		if !waitCond(t, ctx, "!!document.querySelector('"+sel+"')", 3*time.Second) {
			t.Error("clear-all should appear when an explicit filter is set")
		}
		// Clicking it empties the input and returns to defaults (control gone again).
		clickJS(t, ctx, sel)
		if !waitCond(t, ctx, `document.querySelector('#panel-stories .filterbar input').value === ''`, 3*time.Second) {
			t.Error("clear-all should empty the filter input")
		}
		if !waitCond(t, ctx, "!document.querySelector('"+sel+"')", 3*time.Second) {
			t.Error("clear-all should disappear after clearing (back to defaults)")
		}
		waitCond(t, ctx, jsRowVisible(openID), 3*time.Second)
	})

	t.Run("progress_column_lights", func(t *testing.T) {
		// A fresh open story (still at its initial state, no transitions) shows NO
		// progress light — the initial state is not step 1.
		light := fmt.Sprintf(`document.querySelector('#panel-stories tr.row[data-expand-url$="%s"] .col-reviews .review-light')`, openID)
		if !waitCond(t, ctx, "!"+light, 3*time.Second) {
			t.Error("a fresh open story should have no progress light (no phantom current ①)")
		}
		// After a REAL transition (ungated in this fresh repo), a light appears —
		// pushed live to the page over the realtime bus.
		mustRun(t, testBin, repo, "story", "set", openID, "--status", "in_progress")
		if !waitCond(t, ctx, "!!"+light, 8*time.Second) {
			t.Error("a transitioned story should show a progress light, pushed live")
		}
	})

	t.Run("tab_switching", func(t *testing.T) {
		clickJS(t, ctx, `.tab[data-panel="tasks"]`)
		if !waitCond(t, ctx, `getComputedStyle(document.querySelector('#panel-tasks')).display === 'block'`, 5*time.Second) {
			t.Fatal("tasks panel did not become visible after clicking its tab")
		}
		var hash, storiesDisplay string
		if err := chromedp.Run(ctx,
			chromedp.Evaluate(`location.hash`, &hash),
			chromedp.Evaluate(`getComputedStyle(document.querySelector('#panel-stories')).display`, &storiesDisplay),
		); err != nil {
			t.Fatal(err)
		}
		if hash != "#tasks" {
			t.Errorf("hash = %q, want #tasks", hash)
		}
		if storiesDisplay != "none" {
			t.Errorf("stories panel display = %q, want none while tasks active", storiesDisplay)
		}
		// Documents tab shows the indexed doc card.
		clickJS(t, ctx, `.tab[data-panel="docs"]`)
		if !waitCond(t, ctx, `!!document.querySelector('#panel-docs .doc') && getComputedStyle(document.querySelector('#panel-docs')).display === 'block'`, 5*time.Second) {
			t.Error("documents panel/card not visible after clicking its tab")
		}
		// Workflow tab lists the on-disk workflow and expands to its
		// state/transition diagram — read-only.
		clickJS(t, ctx, `.tab[data-panel="workflow"]`)
		if !waitCond(t, ctx, `!!document.querySelector('#panel-workflow tr.row[data-expand-url^="fragment/workflow/"]') && getComputedStyle(document.querySelector('#panel-workflow')).display === 'block'`, 5*time.Second) {
			t.Error("workflow panel/row not visible after clicking its tab")
		}
		// The workflow table carries an Updated column (the Applies-to column was
		// replaced; scope/applies_to render as inline tag chips in the Name cell).
		var hasUpdated, hasAppliesCol bool
		if err := chromedp.Run(ctx,
			chromedp.Evaluate(`[...document.querySelectorAll('#panel-workflow thead th')].some(t=>t.textContent.trim()==='Updated')`, &hasUpdated),
			chromedp.Evaluate(`[...document.querySelectorAll('#panel-workflow thead th')].some(t=>t.textContent.trim()==='Applies to')`, &hasAppliesCol),
		); err != nil {
			t.Fatal(err)
		}
		if !hasUpdated || hasAppliesCol {
			t.Errorf("workflow table headers wrong: hasUpdated=%v hasAppliesCol=%v", hasUpdated, hasAppliesCol)
		}
		clickJS(t, ctx, `#panel-workflow tr.row[data-expand-url^="fragment/workflow/"]`)
		if !waitCond(t, ctx, `(function(){var e=document.querySelector('#panel-workflow tr.expansion .expbody');return !!e && e.textContent.includes('Transitions') && !!document.querySelector('#panel-workflow .wf-node');})()`, 5*time.Second) {
			t.Error("workflow diagram (states/transitions) did not appear on row click")
		}
		// The SVG flow diagram renders nodes and at least one edge (no mermaid).
		if !waitCond(t, ctx, `!!document.querySelector('#panel-workflow svg.wf-diagram .wf-dnode') && !!document.querySelector('#panel-workflow svg.wf-diagram .wf-edge-path')`, 5*time.Second) {
			t.Error("workflow flow diagram (svg nodes + edges) did not render")
		}
		// Back to stories for the remaining checks.
		clickJS(t, ctx, `.tab[data-panel="stories"]`)
		if !waitCond(t, ctx, `getComputedStyle(document.querySelector('#panel-stories')).display === 'block'`, 5*time.Second) {
			t.Fatal("could not return to stories tab")
		}
	})

	t.Run("expand_on_click", func(t *testing.T) {
		// Click the open story's row → an inline expansion with its ledger
		// timeline appears.
		rowSel := fmt.Sprintf(`#panel-stories tr.row[data-expand-url$="%s"]`, openID)
		clickJS(t, ctx, rowSel)
		if !waitCond(t, ctx, `(function(){var e=document.querySelector('#panel-stories tr.expansion .expbody');return !!e && e.textContent.includes('Timeline');})()`, 5*time.Second) {
			t.Fatal("inline expansion with timeline did not appear on row click")
		}
		// Click again → collapses (expansion removed).
		clickJS(t, ctx, rowSel)
		if !waitCond(t, ctx, `!document.querySelector('#panel-stories tr.expansion')`, 5*time.Second) {
			t.Error("row did not collapse on second click")
		}
	})

	t.Run("live_filter", func(t *testing.T) {
		// Type status:done → only the done story shows.
		if err := chromedp.Run(ctx, setInput(`#panel-stories .filterbar input`, "status:done")); err != nil {
			t.Fatalf("set filter: %v", err)
		}
		if !waitCond(t, ctx, jsRowVisible(doneID), 3*time.Second) {
			t.Error("done story should be visible under status:done")
		}
		if visibleRow(t, ctx, openID) != false {
			t.Error("open story should be hidden under status:done")
		}
		if !hasChip(t, ctx, "stories", "status:done") {
			t.Error("expected status:done chip")
		}
		// Clear the filter; the default status:open returns.
		if err := chromedp.Run(ctx, setInput(`#panel-stories .filterbar input`, "")); err != nil {
			t.Fatal(err)
		}
		if !waitCond(t, ctx, jsRowVisible(openID), 3*time.Second) {
			t.Error("open story should be visible again after clearing the filter")
		}
	})

	t.Run("realtime_update_no_reload", func(t *testing.T) {
		// Mark the page so we can prove no full reload happened.
		if err := chromedp.Run(ctx, chromedp.Evaluate(`window.__noReload = true`, nil)); err != nil {
			t.Fatal(err)
		}
		// Mutate from a SEPARATE process (the CLI), as a user would.
		newID := createStory(t, repo, "Pushed Live RT", "")

		// The open page must show the new row within a few seconds, with no reload.
		deadline := time.Now().Add(8 * time.Second)
		seen := false
		for time.Now().Before(deadline) {
			var present bool
			if err := chromedp.Run(ctx, chromedp.Evaluate(
				fmt.Sprintf(`[...document.querySelectorAll('#panel-stories .row')].some(r => r.getAttribute('data-expand-url')||''.includes('%s')) || document.body.innerHTML.includes('Pushed Live RT')`, newID),
				&present)); err != nil {
				t.Fatal(err)
			}
			if present {
				seen = true
				break
			}
			time.Sleep(300 * time.Millisecond)
		}
		if !seen {
			t.Fatal("new story did not appear on the open page via realtime within 8s")
		}
		var noReload bool
		if err := chromedp.Run(ctx, chromedp.Evaluate(`window.__noReload === true`, &noReload)); err != nil {
			t.Fatal(err)
		}
		if !noReload {
			t.Error("page appears to have reloaded — realtime should update in place")
		}
	})
}

// TestBrowserTagChipFiltering drives the click-a-tag-chip path: clicking a tag
// chip on a row adds its token to the panel filter (not expand the row), and
// clicking the same chip again is a deduped no-op.
func TestBrowserTagChipFiltering(t *testing.T) {
	base, repo := serveRepo(t, "8809")
	mustRun(t, testBin, repo, "story", "create", "--title", "Tagged Story", "--tags", "demo")
	mustRun(t, testBin, repo, "story", "create", "--title", "Untagged Story")

	ctx := newChrome(t)
	if err := chromedp.Run(ctx,
		chromedp.Navigate(base+"/"),
		chromedp.WaitVisible(`#panel-stories table.panel-table`, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("load page: %v", err)
	}

	chipSel := `#panel-stories tr.row .tagchip[data-filter="tags:demo"]`
	if !waitCond(t, ctx, fmt.Sprintf(`!!document.querySelector('%s')`, chipSel), 5*time.Second) {
		t.Fatal("demo tag chip did not render")
	}

	// Click the chip → the filter input gains the token and the matching chip
	// shows; the row must NOT have expanded.
	clickJS(t, ctx, chipSel)
	if !waitCond(t, ctx,
		`document.querySelector('#panel-stories .filterbar input').value.trim() === 'tags:demo'`,
		3*time.Second) {
		t.Error("clicking the tag chip should set the filter input to tags:demo")
	}
	if !hasChip(t, ctx, "stories", "tags:demo") {
		t.Error("expected a tags:demo removable chip after clicking the tag")
	}
	if c := countExpansions(t, ctx); c != 0 {
		t.Errorf("clicking a tag chip must not expand a row; got %d expansions", c)
	}

	// Click the chip again → deduped: still a single token, no duplication.
	clickJS(t, ctx, chipSel)
	if !waitCond(t, ctx,
		`document.querySelector('#panel-stories .filterbar input').value.trim() === 'tags:demo'`,
		3*time.Second) {
		t.Error("clicking the chip again should be a deduped no-op (no duplicate token)")
	}
}

// TestBrowserStatusBadgesOutlined asserts the badge restyle (sty_970dbef3) at the
// computed-style level, in BOTH themes: a status badge is an UPPERCASE, OUTLINED
// pill (a real border + matching coloured text, not the old filled light pill), the
// backlog and done badges carry DISTINCT hues, and the backlog text stays legible in
// dark mode (the per-status hue subsuming the earlier sty_173e49a7 dark-only fix).
func TestBrowserStatusBadgesOutlined(t *testing.T) {
	base, repo := serveRepo(t, "8812")
	createStory(t, repo, "Backlog Item", "") // defaults to backlog
	createStory(t, repo, "Finished Item", "done")

	ctx := newChrome(t)
	if err := chromedp.Run(ctx,
		chromedp.Navigate(base+"/"),
		// status:all so the terminal 'done' row is visible alongside backlog.
		chromedp.WaitVisible(`#panel-stories table.panel-table`, chromedp.ByQuery),
		setInput(`#panel-stories .filterbar input`, "status:all"),
		chromedp.WaitVisible(`#panel-stories .badge.s-backlog`, chromedp.ByQuery),
		chromedp.WaitVisible(`#panel-stories .badge.s-done`, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("load page: %v", err)
	}

	read := func(sel, prop string) string {
		var v string
		js := fmt.Sprintf(`getComputedStyle(document.querySelector('%s')).%s`, sel, prop)
		if err := chromedp.Run(ctx, chromedp.Evaluate(js, &v)); err != nil {
			t.Fatalf("read %s.%s: %v", sel, prop, err)
		}
		return v
	}
	setTheme := func(mode string) {
		if err := chromedp.Run(ctx, chromedp.Evaluate(
			fmt.Sprintf(`document.documentElement.setAttribute('data-theme','%s')`, mode), nil)); err != nil {
			t.Fatalf("set theme %s: %v", mode, err)
		}
	}

	for _, mode := range []string{"light", "dark"} {
		setTheme(mode)
		// Outlined: a real border whose colour matches the text (transparent-ish fill).
		if w := read(`#panel-stories .badge.s-backlog`, "borderTopWidth"); w == "0px" || w == "" {
			t.Errorf("[%s] backlog badge should have a visible border (outlined pill); got %q", mode, w)
		}
		// Uppercase.
		if tt := read(`#panel-stories .badge.s-backlog`, "textTransform"); tt != "uppercase" {
			t.Errorf("[%s] backlog badge should be uppercase; got %q", mode, tt)
		}
		// Border colour matches the text colour (the outlined treatment).
		bc := read(`#panel-stories .badge.s-backlog`, "borderTopColor")
		tc := read(`#panel-stories .badge.s-backlog`, "color")
		if bc != tc {
			t.Errorf("[%s] backlog badge border (%q) should match its text colour (%q)", mode, bc, tc)
		}
		// backlog (#2ecc71) and done (#16a34a) are distinct hues.
		if doneC := read(`#panel-stories .badge.s-done`, "color"); doneC == tc {
			t.Errorf("[%s] backlog and done badges must be distinct colours; both %q", mode, tc)
		}
		// Legible in dark: the backlog hue is light/saturated enough to read.
		if mode == "dark" {
			r, g, b := parseRGB(t, tc)
			if r+g+b < 200 {
				t.Errorf("[dark] backlog badge text too dark to read; got %q", tc)
			}
		}
	}
}

// parseRGB pulls the r,g,b channels out of a CSS "rgb(r, g, b)" / "rgba(...)"
// computed-style string.
func parseRGB(t *testing.T, s string) (int, int, int) {
	t.Helper()
	var r, g, b int
	if _, err := fmt.Sscanf(s, "rgb(%d, %d, %d)", &r, &g, &b); err != nil {
		if _, err2 := fmt.Sscanf(s, "rgba(%d, %d, %d", &r, &g, &b); err2 != nil {
			t.Fatalf("parseRGB %q: %v", s, err)
		}
	}
	return r, g, b
}

// TestBrowserTimelineDotsByOutcome asserts at the computed-colour level that the
// story-detail timeline dots are coloured by event outcome (sty_f19d2ec4): a
// review_reject dot is the fail red and a review_accept dot the pass green, matching
// the process-light palette, while a neutral event keeps the default accent dot.
// Checked on the standalone detail page (one of the two surfaces the shared
// template feeds), in both light and dark themes.
func TestBrowserTimelineDotsByOutcome(t *testing.T) {
	base, repo := serveRepo(t, "8813")
	id := createStory(t, repo, "Timeline Story", "")
	// Seed outcome-bearing + neutral ledger events on this story.
	mustRun(t, testBin, repo, "ledger", "append", "--kind", "review_reject", "--actor", "reviewer", "--story", id, "--body", "rejected a->b")
	mustRun(t, testBin, repo, "ledger", "append", "--kind", "review_accept", "--actor", "reviewer", "--story", id, "--body", "accepted a->b")

	ctx := newChrome(t)
	if err := chromedp.Run(ctx,
		chromedp.Navigate(base+"/story/"+id),
		chromedp.WaitVisible(`ol.timeline li`, chromedp.ByQuery),
		chromedp.WaitVisible(`ol.timeline li.tl-fail`, chromedp.ByQuery),
		chromedp.WaitVisible(`ol.timeline li.tl-pass`, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("load detail page: %v", err)
	}

	dotColour := func(sel string) string {
		var v string
		js := fmt.Sprintf(`getComputedStyle(document.querySelector('%s'), '::before').backgroundColor`, sel)
		if err := chromedp.Run(ctx, chromedp.Evaluate(js, &v)); err != nil {
			t.Fatalf("read %s ::before: %v", sel, err)
		}
		return v
	}
	setTheme := func(mode string) {
		if err := chromedp.Run(ctx, chromedp.Evaluate(
			fmt.Sprintf(`document.documentElement.setAttribute('data-theme','%s')`, mode), nil)); err != nil {
			t.Fatalf("set theme %s: %v", mode, err)
		}
	}

	for _, mode := range []string{"light", "dark"} {
		setTheme(mode)
		if c := dotColour(`ol.timeline li.tl-fail`); c != "rgb(231, 76, 60)" {
			t.Errorf("[%s] review_reject dot should be fail red #e74c3c (rgb(231, 76, 60)); got %q", mode, c)
		}
		if c := dotColour(`ol.timeline li.tl-pass`); c != "rgb(46, 204, 113)" {
			t.Errorf("[%s] review_accept dot should be pass green #2ecc71 (rgb(46, 204, 113)); got %q", mode, c)
		}
		// A neutral event (the story_created li, un-classed) keeps the accent dot —
		// neither the fail red nor the pass green.
		if c := dotColour(`ol.timeline li:not(.tl-pass):not(.tl-fail)`); c == "rgb(231, 76, 60)" || c == "rgb(46, 204, 113)" {
			t.Errorf("[%s] a neutral event dot must not be an outcome colour; got %q", mode, c)
		}
	}
}

// TestBrowserBacklogBadgeLiveOnRefetch asserts the Stories tab 'N backlog' badge
// stays live across a realtime (SSE-driven) refetch (sty_af09a484): creating a
// backlog story from a SEPARATE CLI process bumps the badge without a reload, and
// the badge is removed when the live backlog count reaches zero.
func TestBrowserBacklogBadgeLiveOnRefetch(t *testing.T) {
	base, repo := serveRepo(t, "8814")
	id1 := createStory(t, repo, "First Backlog", "") // defaults to backlog

	ctx := newChrome(t)
	if err := chromedp.Run(ctx,
		chromedp.Navigate(base+"/"),
		chromedp.WaitVisible(`.tab[data-panel="stories"] .n-backlog`, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("load page: %v", err)
	}
	badgeText := `(document.querySelector('.tab[data-panel="stories"] .n-backlog')||{}).textContent`
	noBadge := `!document.querySelector('.tab[data-panel="stories"] .n-backlog')`

	// Server-rendered initial value.
	if !waitCond(t, ctx, badgeText+" === '1 backlog'", 3*time.Second) {
		t.Error("initial badge should read '1 backlog'")
	}
	// Sentinel to prove no full reload happens across the live update.
	if err := chromedp.Run(ctx, chromedp.Evaluate(`window.__t4 = true`, nil)); err != nil {
		t.Fatal(err)
	}

	// A SECOND backlog story created by a separate CLI process must bump the badge
	// live (the bug: it stayed frozen at the page-load value).
	id2 := createStory(t, repo, "Second Backlog", "")
	if !waitCond(t, ctx, badgeText+" === '2 backlog'", 6*time.Second) {
		t.Error("badge should update live to '2 backlog' after a backlog story is created via CLI")
	}

	// And it must disappear when the live backlog count drops to zero.
	mustRun(t, testBin, repo, "story", "set", id1, "--status", "in_progress")
	mustRun(t, testBin, repo, "story", "set", id2, "--status", "in_progress")
	if !waitCond(t, ctx, noBadge, 6*time.Second) {
		t.Error("badge should be removed when the live backlog count reaches 0")
	}

	// No full-page reload occurred — the update was the SSE refetch path.
	var noReload bool
	if err := chromedp.Run(ctx, chromedp.Evaluate(`window.__t4 === true`, &noReload)); err != nil {
		t.Fatal(err)
	}
	if !noReload {
		t.Error("the badge update must come from the live refetch, not a page reload")
	}
}

// TestBrowserUptimeBorderTracksConnection validates the uptime indicator's two
// fused signals end-to-end (sty_efeb2a69): the TEXT is the "up …" snapshot, and the
// green border ('on' class) tracks the LIVE SSE connection — it turns on once the
// /events stream opens. This confirms the documented finding that the border means
// "live updates connected", not the elapsed duration.
func TestBrowserUptimeBorderTracksConnection(t *testing.T) {
	base, _ := serveRepo(t, "8815")
	ctx := newChrome(t)
	if err := chromedp.Run(ctx,
		chromedp.Navigate(base+"/"),
		chromedp.WaitVisible(`.uptime`, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("load page: %v", err)
	}
	// The text is the snapshot "up …".
	var txt string
	if err := chromedp.Run(ctx, chromedp.Evaluate(`document.querySelector('.uptime').textContent`, &txt)); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(txt, "up ") {
		t.Errorf("uptime text should be the 'up …' snapshot; got %q", txt)
	}
	// The green border ('on' class) appears once the SSE connection opens — the
	// connection signal, distinct from the text.
	if !waitCond(t, ctx, `document.querySelector('.uptime').classList.contains('on')`, 5*time.Second) {
		t.Error("uptime border should turn on when the live SSE connection opens")
	}
	// The tooltip names both signals (reconciled, not misleading).
	var title string
	if err := chromedp.Run(ctx, chromedp.Evaluate(`document.querySelector('.uptime').title`, &title)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(title, "at page load") || !strings.Contains(title, "live updates connected") {
		t.Errorf("uptime tooltip not reconciled; got %q", title)
	}
}

// TestBrowserWorkflowDiagramInteractive exercises sty_19b2107a end-to-end: the
// dependency-free SVG diagram is enhanced in vanilla JS so focusing a node
// highlights it and its incident edges (and dims the rest), and activating a node
// correlates the transition rows below. No graph library is loaded.
func TestBrowserWorkflowDiagramInteractive(t *testing.T) {
	base, repo := serveRepo(t, "8816")
	// A workflow with a node (in_progress) carrying both an inbound and an outbound
	// edge, plus an off-node edge (commit->done) that must DIM when in_progress is
	// active.
	wf := "---\nname: wf-int\ntype: workflow\nscope: project\napplies_to: [\"*\"]\n---\n" +
		"```dot\n" + `digraph w {
  backlog     [shape=Mdiamond]
  in_progress [agent=executor]
  commit      [agent=executor]
  done        [shape=Msquare]
  rev         [agent=reviewer, prompt="@skill:satelle-story-done-review"]
  backlog -> in_progress -> commit -> rev -> done
}` + "\n```\n"
	if err := os.WriteFile(filepath.Join(repo, ".satelle", "workflows", "wf-int.md"), []byte(wf), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, testBin, repo, "index")

	ctx := newChrome(t)
	if err := chromedp.Run(ctx,
		chromedp.Navigate(base+"/"),
		chromedp.WaitVisible(`#panel-stories table.panel-table`, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("load page: %v", err)
	}
	clickJS(t, ctx, `.tab[data-panel="workflow"]`)
	if !waitCond(t, ctx, `!!document.querySelector('#panel-workflow tr.row[data-expand-url^="fragment/workflow/"]')`, 5*time.Second) {
		t.Fatal("workflow row did not list")
	}
	clickJS(t, ctx, `#panel-workflow tr.row[data-expand-url^="fragment/workflow/"]`)
	if !waitCond(t, ctx, `!!document.querySelector('#panel-workflow svg.wf-diagram .wf-dnode[data-state="in_progress"]')`, 5*time.Second) {
		t.Fatal("diagram did not render with identifiers")
	}

	// No graph library: only our app.js script tag is present.
	var scriptSrcs []string
	if err := chromedp.Run(ctx, chromedp.Evaluate(
		`[...document.querySelectorAll('script[src]')].map(s=>s.getAttribute('src'))`, &scriptSrcs)); err != nil {
		t.Fatal(err)
	}
	for _, s := range scriptSrcs {
		if strings.Contains(strings.ToLower(s), "mermaid") || strings.Contains(strings.ToLower(s), "d3") || strings.Contains(strings.ToLower(s), "cytoscape") {
			t.Errorf("a graph library was loaded (%q) — the diagram must stay dependency-free", s)
		}
	}

	// Hovering OR focusing the in_progress node highlights it + its incident edges
	// and dims a non-incident edge (rev->done, which does not touch in_progress).
	// Both trigger paths are exercised (mouseenter, then a focus event); leaving
	// clears the state. (SVG <g>.focus() is unreliable in headless Chrome, so the
	// focus path is driven by dispatching the event the handler listens for.)
	for _, trigger := range []string{"mouseenter", "focus"} {
		readState := `(function(){
			var n=document.querySelector('#panel-workflow .wf-dnode[data-state="in_progress"]');
			n.dispatchEvent(new Event("` + trigger + `"));
			var inc=document.querySelector('#panel-workflow .wf-edge-path[data-from="in_progress"][data-to="commit"]');
			var off=document.querySelector('#panel-workflow .wf-edge-path[data-from="rev"][data-to="done"]');
			return JSON.stringify({
				node: n.classList.contains("wf-hi"),
				inc: inc && inc.classList.contains("wf-hi"),
				off: off && off.classList.contains("wf-dim")
			});
		})()`
		var got string
		if err := chromedp.Run(ctx, chromedp.Evaluate(readState, &got)); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(got, `"node":true`) || !strings.Contains(got, `"inc":true`) || !strings.Contains(got, `"off":true`) {
			t.Errorf("%s on a node should highlight it + incident edges and dim the rest; got %s", trigger, got)
		}
		// Leaving restores the default (no highlight/dim).
		var cleared bool
		clearEv := "mouseleave"
		if trigger == "focus" {
			clearEv = "blur"
		}
		if err := chromedp.Run(ctx, chromedp.Evaluate(`(function(){
			document.querySelector('#panel-workflow .wf-dnode[data-state="in_progress"]').dispatchEvent(new Event("`+clearEv+`"));
			return !document.querySelector('#panel-workflow .wf-diagram .wf-hi') && !document.querySelector('#panel-workflow .wf-diagram .wf-dim');
		})()`, &cleared)); err != nil {
			t.Fatal(err)
		}
		if !cleared {
			t.Errorf("%s leave should clear the highlight/dim state", trigger)
		}
	}

	// Activating (click) the in_progress node correlates the transition rows below.
	clickAndRead := `(function(){
		document.querySelector('#panel-workflow .wf-dnode[data-state="in_progress"]').dispatchEvent(new MouseEvent('click',{bubbles:true}));
		var hi=[...document.querySelectorAll('#panel-workflow .wf-edge.wf-edge-hi')].map(function(li){return li.dataset.from+"->"+li.dataset.to;});
		return JSON.stringify(hi);
	})()`
	var rows string
	if err := chromedp.Run(ctx, chromedp.Evaluate(clickAndRead, &rows)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rows, "backlog->in_progress") || !strings.Contains(rows, "in_progress->commit") {
		t.Errorf("clicking a node should highlight its incident transition rows; got %s", rows)
	}
}

// countExpansions returns how many inline expansion rows are open in the stories
// panel.
func countExpansions(t *testing.T, ctx context.Context) int {
	t.Helper()
	var n int
	if err := chromedp.Run(ctx, chromedp.Evaluate(
		`document.querySelectorAll('#panel-stories tr.expansion').length`, &n)); err != nil {
		t.Fatalf("countExpansions: %v", err)
	}
	return n
}

// TestBrowserUserPath walks a realistic session: a user opens the project page
// and expands a story while the agent (a separate CLI process) progresses that
// story — asserting the open expansion's timeline grows LIVE without collapsing,
// then breadcrumb-navigates to the detail page (which also live-updates) and
// back, and sorts with order:. This is the "live, navigable" requirement under
// automation.
func TestBrowserUserPath(t *testing.T) {
	base, repo := serveRepo(t, "8803")
	slugPath := "/" + filepath.Base(repo) + "/" // the project page's own path/<base href>
	// Two open stories so order: is observable; the first gets progressed live.
	betaID := createStory(t, repo, "Beta story", "")
	alphaID := createStory(t, repo, "Alpha story", "")
	_ = alphaID

	ctx := newChrome(t)
	if err := chromedp.Run(ctx,
		chromedp.Navigate(base+"/"),
		chromedp.WaitVisible(`.tab[data-panel="stories"][aria-selected="true"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`#panel-stories table.panel-table`, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("load: %v", err)
	}

	t.Run("expand_then_live_progress", func(t *testing.T) {
		rowSel := fmt.Sprintf(`#panel-stories tr.row[data-expand-url$="%s"]`, betaID)
		clickJS(t, ctx, rowSel)
		if !waitCond(t, ctx, `(function(){var e=document.querySelector('#panel-stories tr.expansion .expbody');return !!e && e.textContent.includes('story_created');})()`, 5*time.Second) {
			t.Fatal("expansion timeline did not show story_created")
		}
		before := evalInt(t, ctx, `document.querySelectorAll('#panel-stories tr.expansion .timeline li').length`)

		// The agent progresses the story from ANOTHER process.
		mustRun(t, testBin, repo, "story", "set", betaID, "--status", "in_progress")

		// The OPEN expansion must gain the transition event live, without collapsing.
		grew := waitCond(t, ctx, fmt.Sprintf(
			`(function(){var e=document.querySelector('#panel-stories tr.expansion .timeline');return !!e && e.querySelectorAll('li').length > %d && e.textContent.includes('status_transition');})()`, before),
			8*time.Second)
		if !grew {
			t.Fatal("open expansion timeline did not grow live on status change")
		}
		var expanded bool
		if err := chromedp.Run(ctx, chromedp.Evaluate(
			fmt.Sprintf(`document.querySelector('tr.row[data-expand-url$="%s"]').getAttribute('aria-expanded')==='true'`, betaID), &expanded)); err != nil {
			t.Fatal(err)
		}
		if !expanded {
			t.Error("row collapsed during live refresh — expansion should persist")
		}
	})

	t.Run("order_sort", func(t *testing.T) {
		if err := chromedp.Run(ctx, setInput(`#panel-stories .filterbar input`, "order:title")); err != nil {
			t.Fatal(err)
		}
		ok := waitCond(t, ctx, `(function(){
			var titles=[...document.querySelectorAll('#panel-stories tr.row')].filter(r=>r.style.display!=='none').map(r=>r.dataset.title);
			return titles.length>=2 && titles[0]==='alpha story' && titles.indexOf('beta story')>0;
		})()`, 3*time.Second)
		if !ok {
			t.Error("order:title did not sort Alpha before Beta")
		}
		if !hasChip(t, ctx, "stories", "order:title") {
			t.Error("expected order:title chip")
		}
		if err := chromedp.Run(ctx, setInput(`#panel-stories .filterbar input`, "")); err != nil {
			t.Fatal(err)
		}
		waitCond(t, ctx, jsRowVisible(betaID), 3*time.Second)
	})

	t.Run("id_copy_does_not_toggle_or_navigate", func(t *testing.T) {
		// Clicking the id copies it (shows "copied ✓" feedback) and must NOT toggle
		// the row or navigate the page — stop-propagation.
		before := evalInt(t, ctx, `document.querySelectorAll('#panel-stories tr.expansion').length`)
		clickJS(t, ctx, fmt.Sprintf(`#panel-stories tr.row[data-expand-url$="%s"] .id-copy`, betaID))
		if !waitCond(t, ctx, `[...document.querySelectorAll('#panel-stories .id-copy')].some(function(e){return e.classList.contains('copied')||e.textContent.indexOf('copied')>=0;})`, 3*time.Second) {
			t.Error("id-copy did not show 'copied' feedback")
		}
		after := evalInt(t, ctx, `document.querySelectorAll('#panel-stories tr.expansion').length`)
		if after != before {
			t.Errorf("clicking the id changed expansion count %d→%d (should not toggle the row)", before, after)
		}
		var path string
		if err := chromedp.Run(ctx, chromedp.Evaluate(`location.pathname`, &path)); err != nil {
			t.Fatal(err)
		}
		if path != slugPath {
			t.Errorf("clicking the id navigated to %q (should stay on the project page %q)", path, slugPath)
		}
	})

	t.Run("breadcrumb_to_detail_live_and_back", func(t *testing.T) {
		// The id is a copy control now; navigation moved to the panel's Open story
		// link. Expand the row, then click it.
		clickJS(t, ctx, fmt.Sprintf(`#panel-stories tr.row[data-expand-url$="%s"]`, betaID))
		if !waitCond(t, ctx, `!!document.querySelector('#panel-stories tr.expansion a.open-story')`, 5*time.Second) {
			t.Fatal("Open story link not present after expanding the row")
		}
		clickJS(t, ctx, `#panel-stories tr.expansion a.open-story`)
		if !waitCond(t, ctx, `!!document.querySelector('#detail-live') && !!document.querySelector('.crumbs')`, 8*time.Second) {
			t.Fatal("did not land on the detail page with a breadcrumb")
		}
		// The standalone detail page hides its own "Open story →" self-link — it is
		// present on the expanded project-page card (clicked just above) but redundant
		// here.
		var hasSelfLink bool
		if err := chromedp.Run(ctx, chromedp.Evaluate(`!!document.querySelector('#detail-live a.open-story')`, &hasSelfLink)); err != nil {
			t.Fatal(err)
		}
		if hasSelfLink {
			t.Error("standalone detail page should not render its own Open story self-link")
		}
		beforeLi := evalInt(t, ctx, `document.querySelectorAll('#detail-live .timeline li').length`)
		// Mutate the story from ANOTHER process — a priority change records a ledger
		// row without depending on a particular workflow's edges — and the open
		// detail page must gain it live.
		mustRun(t, testBin, repo, "story", "set", betaID, "--priority", "high")
		if !waitCond(t, ctx, fmt.Sprintf(`document.querySelectorAll('#detail-live .timeline li').length > %d`, beforeLi), 8*time.Second) {
			t.Error("detail page timeline did not live-update")
		}
		clickJS(t, ctx, fmt.Sprintf(`.crumbs a[href=%q]`, slugPath))
		if !waitCond(t, ctx, `!!document.querySelector('.tabs') && !!document.querySelector('#panel-stories')`, 8*time.Second) {
			t.Fatal("breadcrumb 'project' did not return to the project page")
		}
	})
}

// --- chromedp helpers ---

// evalInt evaluates a JS expression to an int.
func evalInt(t *testing.T, ctx context.Context, js string) int {
	t.Helper()
	var n int
	if err := chromedp.Run(ctx, chromedp.Evaluate(js, &n)); err != nil {
		t.Fatalf("evalInt: %v", err)
	}
	return n
}

// clickJS clicks an element via element.click() — robust against chromedp's
// position/visibility heuristics for elements in just-shown panels.
func clickJS(t *testing.T, ctx context.Context, sel string) {
	t.Helper()
	js := fmt.Sprintf(`(function(){var e=document.querySelector(%q);if(e)e.click();return !!e;})()`, sel)
	var ok bool
	if err := chromedp.Run(ctx, chromedp.Evaluate(js, &ok)); err != nil {
		t.Fatalf("clickJS %s: %v", sel, err)
	}
	if !ok {
		t.Fatalf("clickJS: element not found: %s", sel)
	}
}

// waitCond polls a JS boolean expression until true or the timeout elapses.
func waitCond(t *testing.T, ctx context.Context, js string, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var ok bool
		if err := chromedp.Run(ctx, chromedp.Evaluate(js, &ok)); err == nil && ok {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

// jsRowVisible is a JS expression: is the row for id visible (not display:none)?
func jsRowVisible(id string) string {
	return fmt.Sprintf(`(function(){var r=document.querySelector('tr.row[data-expand-url$="%s"]');return !!r && getComputedStyle(r).display!=='none';})()`, id)
}

// createStory creates a story via the CLI and returns its id.
func createStory(t *testing.T, repo, title, status string) string {
	t.Helper()
	args := []string{"story", "create", "--title", title}
	if status != "" {
		args = append(args, "--status", status)
	}
	out := mustRun(t, testBin, repo, args...)
	return extractID(out, "sty_")
}

// visibleRow reports whether the story/task row for id is visible (not
// display:none) in the DOM.
func visibleRow(t *testing.T, ctx context.Context, id string) bool {
	t.Helper()
	var vis bool
	js := fmt.Sprintf(`(function(){
		var r = document.querySelector('tr.row[data-expand-url$="%s"]');
		if (!r) return false;
		return getComputedStyle(r).display !== 'none';
	})()`, id)
	if err := chromedp.Run(ctx, chromedp.Evaluate(js, &vis)); err != nil {
		t.Fatalf("visibleRow(%s): %v", id, err)
	}
	return vis
}

// TestBrowserDocRendersMarkdown opens a document from the Documents tab and
// asserts its markdown was rendered to HTML server-side (a heading element
// exists), not shown as raw text.
func TestBrowserDocRendersMarkdown(t *testing.T) {
	base, _ := serveRepo(t, "8807")
	ctx := newChrome(t)
	if err := chromedp.Run(ctx,
		chromedp.Navigate(base+"/"),
		chromedp.WaitVisible(`.tab[data-panel="docs"]`, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("load: %v", err)
	}
	clickJS(t, ctx, `.tab[data-panel="docs"]`)
	if !waitCond(t, ctx, `!!document.querySelector('#panel-docs a.doc[href^="doc/"]')`, 5*time.Second) {
		t.Fatal("no clickable doc card in the Documents tab")
	}
	var href string
	if err := chromedp.Run(ctx, chromedp.Evaluate(`document.querySelector('#panel-docs a.doc[href^="doc/"]').getAttribute('href')`, &href)); err != nil {
		t.Fatal(err)
	}
	if err := chromedp.Run(ctx,
		chromedp.Navigate(base+"/"+href),
		chromedp.WaitVisible(`article.doc-article`, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("doc page %q: %v", href, err)
	}
	var hasHeading bool
	if err := chromedp.Run(ctx, chromedp.Evaluate(
		`!!document.querySelector('article.doc-article h1, article.doc-article h2, article.doc-article h3')`, &hasHeading)); err != nil {
		t.Fatal(err)
	}
	if !hasHeading {
		t.Error("doc viewer did not render markdown headings — body shown as raw text?")
	}
}

// TestBrowserStoryDocTabs attaches a document to a story and asserts it appears
// as a tab on the detail page with its markdown rendered.
func TestBrowserStoryDocTabs(t *testing.T) {
	base, repo := serveRepo(t, "8808")
	id := createStory(t, repo, "Doc tabs story", "")
	mustRun(t, testBin, repo, "story", "attach", id, "--name", "plan", "--type", "plan",
		"--body", "# Plan\n\n- step one\n- step two")

	ctx := newChrome(t)
	if err := chromedp.Run(ctx,
		chromedp.Navigate(base+"/story/"+id),
		chromedp.WaitVisible(`.doc-tabs .doc-tab`, chromedp.ByQuery),
		chromedp.WaitVisible(`.doc-pane.active .doc-article`, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("story doc tab not rendered: %v", err)
	}
	var rendered bool
	if err := chromedp.Run(ctx, chromedp.Evaluate(
		`!!document.querySelector('.doc-pane.active .doc-article h1') && !!document.querySelector('.doc-pane.active .doc-article li')`, &rendered)); err != nil {
		t.Fatal(err)
	}
	if !rendered {
		t.Error("attached-document tab did not render its markdown (heading + list)")
	}
}

// hasChip reports whether the named panel shows a filter chip with the label.
func hasChip(t *testing.T, ctx context.Context, panel, label string) bool {
	t.Helper()
	var has bool
	js := fmt.Sprintf(`[...document.querySelectorAll('#panel-%s .chips .fchip')].some(c => c.textContent.replace('×','').trim() === '%s')`, panel, label)
	if err := chromedp.Run(ctx, chromedp.Evaluate(js, &has)); err != nil {
		t.Fatalf("hasChip: %v", err)
	}
	return has
}

// TestBrowserSharedTopbar asserts the one shared top-bar component (theme toggle
// + live dot) renders identically on both the project page and a story detail
// page — the nav is one template, not a per-page copy.
func TestBrowserSharedTopbar(t *testing.T) {
	base, repo := serveRepo(t, "8806")
	id := createStory(t, repo, "Topbar story", "")

	ctx := newChrome(t)
	// Project page renders the shared top bar.
	if err := chromedp.Run(ctx,
		chromedp.Navigate(base+"/"),
		chromedp.WaitVisible(`header.app #theme-toggle`, chromedp.ByQuery),
		chromedp.WaitVisible(`header.app .uptime`, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("project page shared topbar missing: %v", err)
	}
	// The same shared top bar renders on the story detail page.
	if err := chromedp.Run(ctx,
		chromedp.Navigate(base+"/story/"+id),
		chromedp.WaitVisible(`header.app #theme-toggle`, chromedp.ByQuery),
		chromedp.WaitVisible(`header.app .uptime`, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("story page shared topbar missing: %v", err)
	}
}

// setInput sets an input's value and fires an 'input' event (so listeners run).
func setInput(sel, val string) chromedp.Action {
	js := fmt.Sprintf(`(function(){
		var el = document.querySelector(%q);
		el.value = %q;
		el.dispatchEvent(new Event('input', { bubbles: true }));
	})()`, sel, val)
	return chromedp.Evaluate(js, nil)
}
