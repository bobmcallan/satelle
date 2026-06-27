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
// port, waits until healthy, and returns the base URL + repo path. Cleanup
// stops the server.
func serveRepo(t *testing.T, port string) (string, string) {
	t.Helper()
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	cmd := exec.Command(testBin, "serve", "--port", port)
	cmd.Dir = repo
	if err := cmd.Start(); err != nil {
		t.Fatalf("start serve: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})
	base := "http://127.0.0.1:" + port
	if !waitHealthy(t, base+"/healthz", 5*time.Second) {
		t.Fatal("server did not become healthy")
	}
	return base, repo
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
		// Workflow tab lists the (embedded) baseline workflow and expands to its
		// state/transition diagram — read-only.
		clickJS(t, ctx, `.tab[data-panel="workflow"]`)
		if !waitCond(t, ctx, `!!document.querySelector('#panel-workflow tr.row[data-expand-url^="/fragment/workflow/"]') && getComputedStyle(document.querySelector('#panel-workflow')).display === 'block'`, 5*time.Second) {
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
		clickJS(t, ctx, `#panel-workflow tr.row[data-expand-url^="/fragment/workflow/"]`)
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

// TestBrowserUserPath walks a realistic session: a user opens the project page
// and expands a story while the agent (a separate CLI process) progresses that
// story — asserting the open expansion's timeline grows LIVE without collapsing,
// then breadcrumb-navigates to the detail page (which also live-updates) and
// back, and sorts with order:. This is the "live, navigable" requirement under
// automation.
func TestBrowserUserPath(t *testing.T) {
	base, repo := serveRepo(t, "8803")
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
		if path != "/" {
			t.Errorf("clicking the id navigated to %q (should stay on the project page)", path)
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
		clickJS(t, ctx, `.crumbs a[href="/"]`)
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
	if !waitCond(t, ctx, `!!document.querySelector('#panel-docs a.doc[href^="/doc/"]')`, 5*time.Second) {
		t.Fatal("no clickable doc card in the Documents tab")
	}
	var href string
	if err := chromedp.Run(ctx, chromedp.Evaluate(`document.querySelector('#panel-docs a.doc[href^="/doc/"]').getAttribute('href')`, &href)); err != nil {
		t.Fatal(err)
	}
	if err := chromedp.Run(ctx,
		chromedp.Navigate(base+href),
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
