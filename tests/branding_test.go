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

	// The far-right brand mark: a ◐ link to the home page, opening a new tab.
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
}
