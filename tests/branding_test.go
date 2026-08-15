//go:build integration

package tests

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestWebHeaderBrandingEndToEnd drives the real binary's served project page to
// prove the satelle.dev-aligned branding lands end-to-end (sty_fa2eb142 +
// epic:mirror-ui-parity): project H1, brand mark, favicon, and shared navbar.
func TestWebHeaderBrandingEndToEnd(t *testing.T) {
	base, repo := serveRepo(t, "8845")
	name := filepath.Base(repo)
	// serveRepo returns the project origin (http://host/r/<slug>); host root is for static.
	host := strings.TrimSuffix(base, "/r/"+name)

	body := httpGet(t, base+"/")

	if !strings.Contains(body, "<h1>"+name+"</h1>") {
		t.Errorf("project header H1 is not the project name %q:\n%s", name, body)
	}
	if strings.Contains(body, `satelle<span class="dot">.</span> project`) {
		t.Errorf("project header still shows the old 'satelle. project' wordmark")
	}

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

	// Site-aligned ◐ monogram (sty_2b1af84b): animated terminator + reduced-motion
	// static fallback, brand green. Markers are structural so comment/path-data
	// tweaks don't false-fail, but the legacy static-only half-disk is rejected.
	fav := httpGet(t, host+"/static/favicon.svg")
	for _, want := range []string{"<circle", "#2f6f4f", "<animate", "prefers-reduced-motion", `id="static"`} {
		if !strings.Contains(fav, want) {
			t.Errorf("favicon missing %q (want satelle.dev monogram):\n%s", want, fav)
		}
	}
	ico := httpGet(t, host+"/favicon.ico")
	for _, want := range []string{"<animate", "#2f6f4f"} {
		if !strings.Contains(ico, want) {
			t.Errorf("/favicon.ico missing %q (must serve the same SVG):\n%s", want, ico)
		}
	}

	order := []string{
		`class="brand-mark"`, `>Install</a>`, `>Docs</a>`,
		`>Projects</a>`, `>Logs</a>`, `class="github-btn"`, `class="theme-toggle"`,
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
	for _, gone := range []string{`>Home</a>`, `>Help</a>`, `>GitHub</a>`} {
		if strings.Contains(body, gone) {
			t.Errorf("navbar should no longer carry %q", gone)
		}
	}
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
	// Workspace landing at / marks Projects active.
	if w := httpGet(t, host+"/"); !strings.Contains(w, `class="active" aria-current="page">Projects</a>`) {
		t.Errorf("workspace landing did not mark the Projects nav link active:\n%s", w)
	}

	// Project settings stay RO; machine hosted server is writable on /settings/global.
	if g := httpGet(t, base+"/settings"); !strings.Contains(g, "settings") || !strings.Contains(g, "read-only") {
		t.Errorf("project settings did not render as read-only:\n%s", g)
	}
	if g := httpGet(t, host+"/settings/global"); !strings.Contains(g, `name="server"`) {
		t.Errorf("global settings must expose the hosted-server field:\n%s", g)
	}
}
