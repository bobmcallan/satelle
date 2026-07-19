//go:build integration

package tests

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestWebTypographySelfHostedEndToEnd proves Space Grotesk is fully self-hosted
// on the push-fed serve static surface (sty_92163102).
func TestWebTypographySelfHostedEndToEnd(t *testing.T) {
	base, repo := serveRepo(t, "8824")
	host := strings.TrimSuffix(base, "/r/"+filepath.Base(repo))

	css := httpGet(t, host+"/static/app.css")
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

	woff := httpGet(t, host+"/static/fonts/space-grotesk-latin.woff2")
	if !strings.HasPrefix(woff, "wOF2") {
		t.Errorf("/static/fonts/space-grotesk-latin.woff2 is not woff2 (magic %q)", woff[:min(4, len(woff))])
	}
}
