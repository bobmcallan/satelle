//go:build integration

package tests

import (
	"strings"
	"testing"
)

// TestWebTypographySelfHostedEndToEnd drives the real binary's served static
// surface to prove the Space Grotesk typography is fully self-hosted
// (sty_92163102): the served stylesheet declares the embedded @font-face and a
// Space Grotesk-first body stack with no external font host, and the woff2
// itself is served under the repo's own /<slug>/static/fonts/ (the CSS-relative
// url() resolution the unit tests can't exercise through a real prefix).
func TestWebTypographySelfHostedEndToEnd(t *testing.T) {
	base, _ := serveRepo(t, "8824")

	css := httpGet(t, base+"/static/app.css")
	for _, want := range []string{
		`font-family: "Space Grotesk"`,
		"font-weight: 300 700",
		`url("fonts/space-grotesk-latin.woff2")`,
		`font: 15px/1.5 "Space Grotesk",`,
	} {
		if !strings.Contains(css, want) {
			t.Errorf("served app.css missing %q", want)
		}
	}
	if strings.Contains(css, "fonts.googleapis") || strings.Contains(css, "fonts.gstatic") || strings.Contains(css, "@import") {
		t.Error("served stylesheet references an external font host")
	}
	if strings.Contains(css, "Montserrat") {
		t.Error("served stylesheet still references Montserrat")
	}

	// The face resolves CSS-relative under the slug prefix and is real woff2.
	woff := httpGet(t, base+"/static/fonts/space-grotesk-latin.woff2")
	if !strings.HasPrefix(woff, "wOF2") {
		t.Errorf("/static/fonts/space-grotesk-latin.woff2 is not woff2 (magic %q)", woff[:min(4, len(woff))])
	}
}
