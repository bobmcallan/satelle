//go:build integration

package tests

import (
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

// TestBrowserUrlAddressableView drives the real page to prove the Stories view is
// URL-addressable (sty_918b2bf7): the tabs are real <a href="#panel"> links, a tab
// click switches the panel and updates the hash, and the filter-bar query is
// written to the URL so a reload restores the same filtered list.
func TestBrowserUrlAddressableView(t *testing.T) {
	ctx := newChrome(t)
	base, repo := serveRepo(t, "8812")
	mustRun(t, testBin, repo, "story", "create",
		"--title", "Alpha", "--body", "b", "--acceptance", "1. x", "--tags", "urltest")
	mustRun(t, testBin, repo, "story", "create",
		"--title", "Beta", "--body", "b", "--acceptance", "1. x")

	// 1. Tabs are real links (open-in-new-tab works) with a panel-encoding href.
	var tag, href string
	if err := chromedp.Run(ctx,
		chromedp.Navigate(base),
		chromedp.WaitVisible(`.tab[data-panel="workflow"]`, chromedp.ByQuery),
		chromedp.Evaluate(`document.querySelector('.tab[data-panel="workflow"]').tagName`, &tag),
		chromedp.Evaluate(`document.querySelector('.tab[data-panel="workflow"]').getAttribute('href')`, &href),
	); err != nil {
		t.Fatal(err)
	}
	if tag != "A" {
		t.Errorf("workflow tab is <%s>, want <A> (a real link supporting open-in-new-tab)", tag)
	}
	if href != "#workflow" {
		t.Errorf("tab href = %q, want #workflow", href)
	}

	// 2. A tab-link click switches the panel and updates the URL hash in-place.
	clickJS(t, ctx, `.tab[data-panel="workflow"]`)
	if !waitCond(t, ctx,
		`location.hash === '#workflow' && getComputedStyle(document.querySelector('#panel-workflow')).display === 'block'`,
		3*time.Second) {
		t.Error("clicking the workflow tab did not switch to #workflow")
	}

	// 3. The filter is written to the URL, and a reload restores it.
	if err := chromedp.Run(ctx,
		chromedp.Navigate(base),
		chromedp.WaitVisible(`#panel-stories table.panel-table`, chromedp.ByQuery),
		setInput(`#panel-stories .filterbar input`, "tags:urltest status:all"),
	); err != nil {
		t.Fatal(err)
	}
	if !waitCond(t, ctx,
		`new URLSearchParams(location.search).get('stories') === 'tags:urltest status:all'`,
		3*time.Second) {
		t.Fatal("filter was not written to the URL as ?stories=…")
	}
	var url string
	if err := chromedp.Run(ctx, chromedp.Evaluate(`location.href`, &url)); err != nil {
		t.Fatal(err)
	}
	var restored string
	if err := chromedp.Run(ctx,
		chromedp.Navigate(url), // reload the captured URL — a fresh page load
		chromedp.WaitVisible(`#panel-stories .filterbar input`, chromedp.ByQuery),
		chromedp.Evaluate(`document.querySelector('#panel-stories .filterbar input').value`, &restored),
	); err != nil {
		t.Fatal(err)
	}
	if restored != "tags:urltest status:all" {
		t.Errorf("filter not restored from the URL on reload: input = %q", restored)
	}
}
