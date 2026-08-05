package web

import (
	"regexp"
	"strings"
	"testing"
)

// TestRouteDocMatchesSiblingProseChrome pins the expanded route panel's card
// chrome to the sibling pre.prose panels, and keeps the 760px reading measure
// on plain .doc-article attachments (sty_c3b1eb57).
func TestRouteDocMatchesSiblingProseChrome(t *testing.T) {
	raw, err := staticFS.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(raw)

	// .doc-article keeps the measure for collapsed Documents-list attachments.
	if !strings.Contains(css, ".doc-article { font-size: 14.5px; line-height: 1.65; max-width: 760px; }") {
		t.Error(".doc-article must keep max-width: 760px for list attachments")
	}

	// .route-doc overrides that measure and wears pre.prose's card chrome.
	routeRule := regexp.MustCompile(`(?s)\.route-doc\s*\{[^}]*\}`)
	m := routeRule.FindString(css)
	if m == "" {
		t.Fatal("app.css missing .route-doc rule")
	}
	for _, want := range []string{
		"max-width: none",
		"border-radius: 8px",
		"padding: 10px 12px",
		"background: var(--panel)",
		"border: 1px solid var(--line)",
	} {
		if !strings.Contains(m, want) {
			t.Errorf(".route-doc rule missing %q; got %q", want, m)
		}
	}
	// Sibling prose panels carry the same chrome values (single family of cards).
	proseRule := regexp.MustCompile(`(?s)\.expbody pre\.prose\s*\{[^}]*\}`)
	p := proseRule.FindString(css)
	if p == "" {
		t.Fatal("app.css missing .expbody pre.prose rule")
	}
	for _, want := range []string{"border-radius: 8px", "padding: 10px 12px", "background: var(--panel)"} {
		if !strings.Contains(p, want) {
			t.Errorf("pre.prose sibling chrome missing %q; got %q", want, p)
		}
	}
}
