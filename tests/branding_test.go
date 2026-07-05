//go:build integration

package tests

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestWebHeaderBrandingEndToEnd drives the real binary's served project page to
// prove the satelle.dev-aligned branding lands end-to-end (sty_fa2eb142): the
// header leads with the repo's project name (not the old hardcoded
// "satelle. project" wordmark), a ◐ halfmoon brand mark links the home page in a
// new tab, and the favicon is the halfmoon monogram.
func TestWebHeaderBrandingEndToEnd(t *testing.T) {
	base, repo := serveRepo(t, "8815")
	name := filepath.Base(repo) // the project is served under /<basename>; H1 mirrors it

	body := httpGet(t, base+"/")

	if !strings.Contains(body, "<h1>"+name+"</h1>") {
		t.Errorf("project header H1 is not the project name %q:\n%s", name, body)
	}
	if strings.Contains(body, `satelle<span class="dot">.</span> project`) {
		t.Errorf("project header still shows the old 'satelle. project' wordmark")
	}

	// The leading ◐ satelle brand mark: a link to the home page, opening a new tab.
	for _, want := range []string{
		`class="brand-mark"`,
		`href="https://satelle.dev/"`,
		`target="_blank"`,
		`rel="noopener"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("header missing %q (the ◐ home brand mark):\n%s", want, body)
		}
	}

	// The favicon is the halfmoon monogram (outline circle + left-half <path>),
	// in the brand accent green — not the old solid dot.
	fav := httpGet(t, base+"/static/favicon.svg")
	if !strings.Contains(fav, "<circle") || !strings.Contains(fav, "<path") || !strings.Contains(fav, "#2f6f4f") {
		t.Errorf("favicon is not the halfmoon monogram:\n%s", fav)
	}

	// The shared navbar carries the satelle.dev nav row (sty_523f93b3, sty_2faa7dd4):
	// Install · Docs · Projects text links then a GitHub OUTLINED ICON button, in
	// order between the brand and the theme-toggle-last, with NO Home/Help top-nav
	// items. (Folded into this test so it reuses the one serve — no extra subprocess.)
	order := []string{
		`class="brand-mark"`, `>Install</a>`, `>Docs</a>`,
		`>Projects</a>`, `class="github-btn"`, `class="theme-toggle"`,
	}
	prev := -1
	for _, needle := range order {
		at := strings.Index(body, needle)
		if at < 0 {
			t.Errorf("navbar missing %q on the served page", needle)
			continue
		}
		if at < prev {
			t.Errorf("navbar element %q is out of order", needle)
		}
		prev = at
	}
	// The dropped Home/Help text items and the GitHub text link must be gone.
	for _, gone := range []string{`>Home</a>`, `>Help</a>`, `>GitHub</a>`} {
		if strings.Contains(body, gone) {
			t.Errorf("navbar should no longer carry %q (Home/Help dropped; GitHub is an icon button)", gone)
		}
	}
	// External nav links open in a new tab.
	for _, ext := range []string{"https://satelle.dev/install", "https://satelle.dev/docs", "https://github.com/bobmcallan/satelle"} {
		i := strings.Index(body, ext)
		if i < 0 {
			t.Errorf("navbar missing external link %q", ext)
			continue
		}
		start := strings.LastIndex(body[:i], "<a ")
		anchor := body[start : start+strings.Index(body[start:], ">")]
		if !strings.Contains(anchor, `target="_blank"`) || !strings.Contains(anchor, `rel="noopener"`) {
			t.Errorf("external link %q is not new-tab: %s", ext, anchor)
		}
	}
	// The workspace landing marks the Projects nav link active (per-page active marking).
	if w := httpGet(t, base+"/workspace"); !strings.Contains(w, `class="active" aria-current="page">Projects</a>`) {
		t.Errorf("workspace landing did not mark the Projects nav link active")
	}

	// The global settings page (sty_432bdeb7) renders with the shared navbar and the
	// hosted-server field. (Folded in to reuse the one serve — no extra subprocess.)
	if g := httpGet(t, base+"/settings/global"); !strings.Contains(g, `name="server"`) || !strings.Contains(g, "global settings") {
		t.Errorf("/settings/global did not render the global settings form")
	}
}
