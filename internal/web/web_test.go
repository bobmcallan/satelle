package web_test

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/web"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// ledgerInput builds a story_created entry for the given story.
func ledgerInput(storyID string) ledger.AppendInput {
	return ledger.AppendInput{StoryID: storyID, Kind: ledger.KindStoryCreated, Body: "created"}
}

func newServer(t *testing.T) (*httptest.Server, *store.DB) {
	t.Helper()
	// Isolate the machine-wide config so the topbar's signed-in/out state is
	// deterministic and never reflects the operator's real ~/.satelle login
	// (which would otherwise render an avatar instead of the "Sign in" link).
	t.Setenv("SATELLE_HOME", t.TempDir())
	db, err := store.Open(filepath.Join(t.TempDir(), "satelle.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	verb.SetWorkItemStore(db.Stories)
	verb.SetLedgerStore(db.Ledger)
	verb.SetDocIndexStore(db.DocIndex)
	a := &app.App{RepoRoot: "/repo", DBPath: "/repo/.satelle/satelle.db", Store: db}
	srv := httptest.NewServer(web.Build(a))
	t.Cleanup(func() {
		srv.Close()
		db.Close()
		verb.SetWorkItemStore(nil)
		verb.SetLedgerStore(nil)
		verb.SetDocIndexStore(nil)
	})
	return srv, db
}

// indexDocs writes each name→body of kind to a temp dir and Syncs it into the
// index, making them LISTED on-disk docs. Embedded defaults are no longer overlaid
// into List (sty_94da9ac9), so a test that needs a doc enumerated must put it on disk.
func indexDocs(t *testing.T, db *store.DB, kind string, docs map[string]string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), kind)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range docs {
		if err := os.WriteFile(filepath.Join(dir, name+".md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.DocIndex.Sync(context.Background(), map[string]string{kind: dir}, time.Now()); err != nil {
		t.Fatal(err)
	}
}

func get(t *testing.T, url string) (int, string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestHealthz(t *testing.T) {
	srv, _ := newServer(t)
	code, body := get(t, srv.URL+"/healthz")
	if code != 200 || !strings.Contains(body, "ok") {
		t.Errorf("healthz = %d %q", code, body)
	}
}

// TestTabsRenderAsAnchorLinks: the Stories/Tasks/Workflow/Documents tabs render as
// real <a href="#panel"> links in the server HTML (not <button>), so the browser
// offers open-in-new-tab / middle-click and the active tab lives in the URL
// (sty_918b2bf7).
func TestTabsRenderAsAnchorLinks(t *testing.T) {
	srv, _ := newServer(t)
	code, body := get(t, srv.URL+"/")
	if code != 200 {
		t.Fatalf("/ = %d", code)
	}
	for _, panel := range []string{"stories", "tasks", "workflow", "docs"} {
		anchor := `<a class="tab" role="tab" data-panel="` + panel + `" href="#` + panel + `">`
		if !strings.Contains(body, anchor) {
			t.Errorf("tab %q is not an anchor link with href=#%s:\n%s", panel, panel, body)
		}
	}
	if strings.Contains(body, `<button class="tab"`) {
		t.Error("a tab is still a <button> — tabs must be <a> links")
	}
}

// TestFaviconLinkedAndServed: the green-dot logo is the favicon on every page —
// the asset is served and each page <head> links it (sty_f00d40c9).
func TestFaviconLinkedAndServed(t *testing.T) {
	srv, db := newServer(t)
	it, err := db.Stories.Create(context.Background(),
		workitem.CreateInput{Kind: workitem.KindStory, Title: "Icon story"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	// The asset is served as an SVG green dot.
	code, svg := get(t, srv.URL+"/static/favicon.svg")
	if code != 200 {
		t.Fatalf("/static/favicon.svg = %d", code)
	}
	if !strings.Contains(svg, "<svg") || !strings.Contains(svg, "<circle") {
		t.Errorf("favicon is not an SVG circle:\n%s", svg)
	}
	if !strings.Contains(svg, "#2f6f4f") {
		t.Errorf("favicon is not the brand accent green #2f6f4f:\n%s", svg)
	}
	// The ◐ halfmoon monogram (matching satelle.dev): an outline circle plus a
	// <path> filling the left half — not the old solid dot.
	if !strings.Contains(svg, "<path") {
		t.Errorf("favicon is not the halfmoon monogram (missing the left-half <path>):\n%s", svg)
	}

	// Every page <head> links it — a main page and the sub-pages.
	for _, path := range []string{"/", "/help", "/workspace", "/story/" + it.ID} {
		code, body := get(t, srv.URL+path)
		if code != 200 {
			t.Fatalf("%s = %d", path, code)
		}
		if !strings.Contains(body, `rel="icon"`) || !strings.Contains(body, "favicon.svg") {
			t.Errorf("page %s does not link the favicon:\n%s", path, body)
		}
	}
}

func TestProjectPageRendersData(t *testing.T) {
	srv, db := newServer(t)
	ctx := context.Background()
	if _, err := db.Stories.Create(ctx, workitem.CreateInput{
		Kind: workitem.KindStory, Title: "Render the page", Status: workitem.StatusInProgress,
	}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Stories.Create(ctx, workitem.CreateInput{
		Kind: workitem.KindTask, Title: "ship notes",
	}, time.Now()); err != nil {
		t.Fatal(err)
	}

	code, body := get(t, srv.URL+"/")
	if code != 200 {
		t.Fatalf("status = %d", code)
	}
	for _, want := range []string{"Render the page", "ship notes", "/repo", "Stories", "Tasks", `badge s-in_progress`} {
		if !strings.Contains(body, want) {
			t.Errorf("page missing %q", want)
		}
	}
}

// TestHeaderBrandingProjectNameAndHomeMark asserts the satelle.dev-aligned header
// branding: the project page leads with the repo's project name (not the old
// hardcoded "satelle. project" wordmark), and the shared topbar carries the ◐
// halfmoon brand mark linking home in a new tab.
func TestHeaderBrandingProjectNameAndHomeMark(t *testing.T) {
	srv, db := newServer(t)
	if _, err := db.Stories.Create(context.Background(),
		workitem.CreateInput{Kind: workitem.KindStory, Title: "Branding story"}, time.Now()); err != nil {
		t.Fatal(err)
	}

	code, body := get(t, srv.URL+"/")
	if code != 200 {
		t.Fatalf("status = %d", code)
	}
	// H1 is the project name — newServer's RepoRoot is "/repo".
	if !strings.Contains(body, "<h1>repo</h1>") {
		t.Errorf("project header H1 is not the project name:\n%s", body)
	}
	if strings.Contains(body, "satelle<span class=\"dot\">.</span> project") {
		t.Errorf("project header still shows the old 'satelle. project' wordmark:\n%s", body)
	}
	// The leading brand mark: a ◐ satelle wordmark linking home in a new tab, inside
	// the shared full-bleed navbar band.
	if !strings.Contains(body, `<header class="topbar">`) ||
		!strings.Contains(body, `class="brand-mark"`) ||
		!strings.Contains(body, `class="brand-word">satelle`) ||
		!strings.Contains(body, `href="https://satelle.dev/"`) ||
		!strings.Contains(body, `target="_blank"`) ||
		!strings.Contains(body, `rel="noopener"`) {
		t.Errorf("navbar missing the leading ◐ satelle wordmark (new-tab link to satelle.dev) in the topbar band:\n%s", body)
	}
	// The navbar is flex (no floats), so source order == visual order: the brand mark
	// LEADS at the left, the account/nav controls follow, and the theme toggle is LAST.
	bm, ac, tt := strings.Index(body, `class="brand-mark"`), strings.Index(body, `class="signin"`), strings.Index(body, `class="theme-toggle"`)
	if bm < 0 || ac < 0 || tt < 0 || !(bm < ac && ac < tt) {
		t.Errorf("navbar order is not brand-mark → account → theme-toggle (mark leads, toggle last): brand-mark=%d account=%d theme-toggle=%d", bm, ac, tt)
	}
	// The theme toggle uses the DS ☾ glyph, never ◐ (which is reserved for the mark).
	if ti := strings.Index(body, `class="theme-toggle"`); ti >= 0 {
		btn := body[ti:]
		if end := strings.Index(btn, "</button>"); end >= 0 {
			btn = btn[:end]
		}
		if strings.Contains(btn, "◐") || !strings.Contains(btn, "☾") {
			t.Errorf("theme toggle must use the ☾ glyph, not ◐:\n%s", btn)
		}
	}
}

// TestNavbarConsistentAcrossSurfaces asserts the ONE shared full-bleed navbar
// renders identically on every in-process surface — project page, aggregate
// /workspace, and settings — with the mark leading, no per-surface drift, and the
// retired uptime pill gone (sty_cd2fe2f3). The supervisor landing is covered by
// multi_test.go's projects-page test.
func TestNavbarConsistentAcrossSurfaces(t *testing.T) {
	srv, _ := newServer(t)
	for _, path := range []string{"/", "/workspace", "/settings"} {
		code, body := get(t, srv.URL+path)
		if code != 200 {
			t.Fatalf("%s = %d", path, code)
		}
		if n := strings.Count(body, `<header class="topbar">`); n != 1 {
			t.Errorf("%s: expected exactly one navbar band, got %d", path, n)
		}
		if !strings.Contains(body, `class="brand-word">satelle`) {
			t.Errorf("%s: navbar missing the leading ◐ satelle wordmark", path)
		}
		bm, tt := strings.Index(body, `class="brand-mark"`), strings.Index(body, `class="theme-toggle"`)
		if bm < 0 || tt < 0 || bm > tt {
			t.Errorf("%s: mark not source-ordered before the theme toggle: brand=%d toggle=%d", path, bm, tt)
		}
		if strings.Contains(body, `class="uptime"`) {
			t.Errorf("%s: retired uptime pill still present", path)
		}
		// Every surface loads app.js so the theme toggle + live wiring work uniformly.
		if !strings.Contains(body, `src="static/app.js"`) {
			t.Errorf("%s: does not load app.js (theme toggle would be dead)", path)
		}
	}
}

// TestNavbarCSSTokens asserts the served CSS carries the full-bleed band with the
// satelle.dev 1px --line hairline rule + shared content-width token, the brand ink
// wordmark / accent ◐ split, and the mark's MUTED disconnected state on the ◐ only
// (sty_cd2fe2f3, sty_2faa7dd4).
func TestNavbarCSSTokens(t *testing.T) {
	srv, _ := newServer(t)
	code, css := get(t, srv.URL+"/static/app.css")
	if code != 200 {
		t.Fatalf("/static/app.css = %d", code)
	}
	for _, want := range []string{
		".topbar {", "border-bottom: 1px solid var(--line)", // full-bleed band + hairline rule
		"var(--content-w)",         // shared content-width token
		".brand-mark.sse-down svg", // disconnected state scoped to the ◐ mark only
		"--fail-soft:",             // the dedicated muted red token
		".brand-mark svg { width: 20px; height: 20px; color: var(--accent)", // ◐ is accent
	} {
		if !strings.Contains(css, want) {
			t.Errorf("served CSS missing %q", want)
		}
	}
	// The wordmark is ink, and the disconnect no longer reddens the whole brand: the
	// old 2px-ink band rule and the whole-brand sse-down rule must be gone.
	if strings.Contains(css, "2px solid var(--ink)") {
		t.Errorf("navbar band should be a 1px --line hairline, not the old 2px ink rule")
	}
	if strings.Contains(css, ".brand-mark.sse-down {") {
		t.Errorf("sse-down must be scoped to the ◐ svg, not the whole brand-mark (the wordmark stays ink)")
	}
	// AC4: the account avatar is an OUTLINED circle (1px --line ring, ink initial),
	// not the old filled-accent disc.
	i := strings.Index(css, ".account .avatar {")
	if i < 0 {
		t.Fatalf("no .account .avatar rule in served CSS")
	}
	avatarRule := css[i:]
	if j := strings.Index(avatarRule, "}"); j >= 0 {
		avatarRule = avatarRule[:j]
	}
	for _, want := range []string{"border: 1px solid var(--line)", "background: none", "color: var(--ink)"} {
		if !strings.Contains(avatarRule, want) {
			t.Errorf(".account .avatar should be outlined (%q); rule:\n%s", want, avatarRule)
		}
	}
	if strings.Contains(avatarRule, "background: var(--accent)") {
		t.Errorf(".account .avatar must not be the old filled-accent disc")
	}
}

// TestTopbarNavRow asserts the shared navbar carries the satelle.dev nav row
// (Install · Docs · Projects text links + a GitHub OUTLINED ICON button) between the
// brand and the theme-toggle-last, with NO Home/Help top-nav items, external links
// opening in a new tab, Projects active-marked on the workspace landing, and the DS
// link styling in the served CSS (sty_523f93b3, sty_2faa7dd4).
func TestTopbarNavRow(t *testing.T) {
	srv, _ := newServer(t)

	// The nav appears on every surface via the one shared partial, in satelle.dev
	// order, after the brand and before the theme toggle (which stays last).
	for _, path := range []string{"/", "/workspace", "/help", "/settings"} {
		code, body := get(t, srv.URL+path)
		if code != 200 {
			t.Fatalf("%s = %d", path, code)
		}
		idx := []struct {
			label, needle string
		}{
			{"brand-mark", `class="brand-mark"`},
			{"Install", `>Install</a>`},
			{"Docs", `>Docs</a>`},
			{"Projects", `>Projects</a>`},
			{"GitHub icon button", `class="github-btn"`},
			{"theme-toggle", `class="theme-toggle"`},
		}
		prev := -1
		for _, it := range idx {
			at := strings.Index(body, it.needle)
			if at < 0 {
				t.Errorf("%s: navbar missing %q", path, it.label)
				continue
			}
			if at < prev {
				t.Errorf("%s: %q is out of order (brand → Install → Docs → Projects → GitHub icon → theme-toggle-last)", path, it.label)
			}
			prev = at
		}
		// The dropped Home/Help top-nav text items must be gone (Help stays on the
		// meta/breadcrumb line; the brand IS the home affordance).
		if strings.Contains(body, `>Home</a>`) {
			t.Errorf("%s: navbar should not carry a Home top-nav text item", path)
		}
		if strings.Contains(body, `class="topnav"`) && strings.Contains(body[strings.Index(body, `class="topnav"`):], `>Help</a>`) {
			// Only flag a Help link inside the topnav; the meta line may still link Help.
			nav := body[strings.Index(body, `class="topnav"`):]
			nav = nav[:strings.Index(nav, "</nav>")]
			if strings.Contains(nav, `>Help</a>`) {
				t.Errorf("%s: navbar should not carry a Help top-nav text item", path)
			}
		}
		// GitHub is an ICON button, not a text link.
		if strings.Contains(body, `>GitHub</a>`) {
			t.Errorf("%s: GitHub must be an outlined icon button (.github-btn), not a text link", path)
		}
	}

	// External links open in a new tab.
	_, home := get(t, srv.URL+"/")
	for _, ext := range []string{"https://satelle.dev/install", "https://satelle.dev/docs", "https://github.com/bobmcallan/satelle"} {
		i := strings.Index(home, ext)
		if i < 0 {
			t.Errorf("nav missing external link %q", ext)
			continue
		}
		start := strings.LastIndex(home[:i], "<a ")
		anchor := home[start : start+strings.Index(home[start:], ">")]
		if !strings.Contains(anchor, `target="_blank"`) || !strings.Contains(anchor, `rel="noopener"`) {
			t.Errorf("external link %q not new-tab (target=_blank rel=noopener): %s", ext, anchor)
		}
	}

	// Active marking: the workspace landing marks Projects active.
	_, ws := get(t, srv.URL+"/workspace")
	if !strings.Contains(ws, `class="active" aria-current="page">Projects</a>`) {
		t.Errorf("workspace landing did not mark Projects active:\n%s", ws)
	}

	// The served CSS carries the DS link styling and the outlined GitHub button.
	_, css := get(t, srv.URL+"/static/app.css")
	for _, want := range []string{".topnav a.active", "var(--accent)", ".topnav a:hover", "var(--chip)", ".topnav a.github-btn"} {
		if !strings.Contains(css, want) {
			t.Errorf("served CSS missing DS nav styling %q", want)
		}
	}
}

// TestBrandMarkNoHoverUnderline asserts the ◐ brand mark suppresses the global
// a:hover underline — it is an icon, not body text (sty_6ee88532).
func TestBrandMarkNoHoverUnderline(t *testing.T) {
	srv, _ := newServer(t)
	code, css := get(t, srv.URL+"/static/app.css")
	if code != 200 {
		t.Fatalf("/static/app.css = %d", code)
	}
	i := strings.Index(css, ".brand-mark:hover")
	if i < 0 {
		t.Fatalf("no .brand-mark:hover rule in served CSS")
	}
	rule := css[i:]
	if j := strings.IndexByte(rule, '}'); j >= 0 {
		rule = rule[:j]
	}
	if !strings.Contains(rule, "text-decoration: none") {
		t.Errorf(".brand-mark:hover does not kill the underline (needs text-decoration: none):\n%s", rule)
	}
}

func TestTagChipsCarryFilterToken(t *testing.T) {
	srv, db := newServer(t)
	ctx := context.Background()
	if _, err := db.Stories.Create(ctx, workitem.CreateInput{
		Kind: workitem.KindStory, Title: "Taggy story", Status: workitem.StatusInProgress,
		Category: "improvement", Tags: []string{"web", "epic:agent-rename"},
	}, time.Now()); err != nil {
		t.Fatal(err)
	}

	code, body := get(t, srv.URL+"/")
	if code != 200 {
		t.Fatalf("status = %d", code)
	}
	// Each tag chip is a clickable <button> carrying the exact filter token it
	// adds: a bare/kv tag → tags:<full-tag>; the category chip → category:<value>.
	for _, want := range []string{
		`<button type="button" class="tagchip clickable" data-filter="tags:web"`,
		`data-filter="tags:epic:agent-rename"`,
		`data-filter="category:improvement"`,
		`aria-label="filter by epic:agent-rename"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page missing clickable tag chip affordance %q", want)
		}
	}
	// The chips remain accessible-labelled buttons, not the old inert spans.
	if strings.Contains(body, `<span class="tagchip kv cat">`) {
		t.Errorf("category chip should be a clickable button, not an inert span")
	}
}

func TestBacklogCountRendered(t *testing.T) {
	srv, db := newServer(t)
	ctx := context.Background()
	mk := func(title, status string) {
		if _, err := db.Stories.Create(ctx, workitem.CreateInput{
			Kind: workitem.KindStory, Title: title, Status: status,
		}, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	// 3 stories total; 2 in the open backlog, 1 in_progress.
	mk("backlog one", workitem.StatusBacklog)
	mk("backlog two", workitem.StatusBacklog)
	mk("working", workitem.StatusInProgress)

	code, body := get(t, srv.URL+"/")
	if code != 200 {
		t.Fatalf("status = %d", code)
	}
	// Tab shows the backlog count (2 open) as a distinct badge alongside the total.
	if !strings.Contains(body, "2 backlog") {
		t.Errorf("page missing backlog count %q", "2 backlog")
	}
	if !strings.Contains(body, "n-backlog") {
		t.Errorf("backlog badge should carry the distinct n-backlog class")
	}
}

// TestStatusBadgesOutlinedPills asserts the badge restyle (sty_970dbef3): an
// UPPERCASE, OUTLINED pill (border + matching text on a near-transparent fill)
// where every workflow state this repo uses carries its OWN --badge-c hue, so no
// state falls back to an undifferentiated grey. The transparent-fill outline means
// the one hue reads on both themes. This supersedes the earlier dark-only badge fix
// (sty_173e49a7), whose dark-legibility intent the per-status light hues preserve.
func TestStatusBadgesOutlinedPills(t *testing.T) {
	srv, db := newServer(t)
	code, css := get(t, srv.URL+"/static/app.css")
	if code != 200 {
		t.Fatalf("/static/app.css = %d", code)
	}
	// Subtle + uppercase (sty_aed93a00): the base .badge is text-transform uppercase
	// with a SOFTENED border driven by the per-status hue (color-mix toward
	// transparent), and the label text mixed toward the foreground (--ink) so it
	// reads on both themes rather than as a saturated hue on the panel.
	if !strings.Contains(css, "text-transform: uppercase") {
		t.Errorf("badge should be uppercase (text-transform: uppercase)")
	}
	if !strings.Contains(css, "border: 1px solid color-mix(in srgb, var(--badge-c") {
		t.Errorf("badge should be outlined with a softened (color-mix) --badge-c border")
	}
	if !strings.Contains(css, "color: color-mix(in srgb, var(--badge-c, var(--muted)) 62%, var(--ink))") {
		t.Errorf("badge text should be softened toward --ink for both-theme legibility")
	}
	// Every workflow state used by this repo defines its own colour — no grey fallback.
	for _, st := range []string{"backlog", "in_progress", "integration", "commit", "push", "committed", "done", "cancelled"} {
		re := regexp.MustCompile(`\.badge\.s-` + st + `\s+\{ --badge-c:`)
		if !re.MatchString(css) {
			t.Errorf("status %q is missing its own .badge.s-%s { --badge-c: … } rule", st, st)
		}
	}
	// backlog and done carry DISTINCT hues (AC4 names these two explicitly).
	if !strings.Contains(css, ".badge.s-backlog     { --badge-c: #2ecc71;") {
		t.Errorf("backlog badge should be the reference mint green #2ecc71")
	}
	if !strings.Contains(css, ".badge.s-done        { --badge-c: #16a34a;") {
		t.Errorf("done badge should be the deep green #16a34a (distinct from backlog)")
	}

	// The markup carries the per-status class for backlog and done (the pill colour
	// is keyed off it). Seed one of each and confirm the class is emitted in the page.
	ctx := context.Background()
	for _, st := range []string{workitem.StatusBacklog, workitem.StatusDone} {
		if _, err := db.Stories.Create(ctx, workitem.CreateInput{
			Kind: workitem.KindStory, Title: "badge " + st, Status: st,
		}, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	_, page := get(t, srv.URL+"/")
	for _, want := range []string{`class="badge s-backlog"`, `class="badge s-done"`} {
		if !strings.Contains(page, want) {
			t.Errorf("page missing the per-status badge class %q", want)
		}
	}
}

// TestBacklogBadgeRecomputedOnRefetch asserts the served app.js recomputes the
// 'N backlog' badge from the live rows in the same refetch path that refreshes the
// total .n count (sty_af09a484) — so the badge no longer freezes at the page-load
// value on an SSE refetch.
func TestBacklogBadgeRecomputedOnRefetch(t *testing.T) {
	srv, _ := newServer(t)
	code, js := get(t, srv.URL+"/static/app.js")
	if code != 200 {
		t.Fatalf("/static/app.js = %d", code)
	}
	if !strings.Contains(js, "refreshBacklogBadge") {
		t.Errorf("app.js missing the refreshBacklogBadge recompute helper")
	}
	// It counts the live backlog rows and is wired into the refetch path.
	if !strings.Contains(js, `.row[data-status="backlog"]`) {
		t.Errorf("backlog badge must be recomputed from live data-status=\"backlog\" rows")
	}
	if !strings.Contains(js, `if (topic === "stories") refreshBacklogBadge(panel)`) {
		t.Errorf("refreshBacklogBadge must run inside refetchPanel where .n is refreshed")
	}
}

func TestStoriesFilterCountRendered(t *testing.T) {
	srv, _ := newServer(t)
	code, body := get(t, srv.URL+"/")
	if code != 200 {
		t.Fatalf("status = %d", code)
	}
	// The stories filterbar carries the filter-count element (filled with
	// "<filtered> / <total>" by app.js on filter); assert it is present to render.
	if !strings.Contains(body, "filter-count") {
		t.Errorf("stories filterbar missing the filter-count element")
	}
}

// TestUptimeFoldedIntoBrandMark asserts the retired 'up Nm' pill is gone and the
// uptime snapshot now rides in the brand mark's title tooltip (sty_cd2fe2f3).
func TestUptimeFoldedIntoBrandMark(t *testing.T) {
	srv, _ := newServer(t)
	code, body := get(t, srv.URL+"/")
	if code != 200 {
		t.Fatalf("status = %d", code)
	}
	// The separate uptime pill is removed.
	if strings.Contains(body, `class="uptime"`) {
		t.Errorf("the retired uptime pill is still rendered")
	}
	// The uptime snapshot is now in the brand-mark title, alongside the note that the
	// mark's colour signals the live-update connection.
	bm := strings.Index(body, `class="brand-mark"`)
	if bm < 0 {
		t.Fatalf("no brand-mark in the topbar")
	}
	tag := body[bm:]
	if end := strings.Index(tag, ">"); end >= 0 {
		tag = tag[:end]
	}
	if !strings.Contains(tag, "up ") || !strings.Contains(tag, "at page load") || !strings.Contains(tag, "live-update connection") {
		t.Errorf("brand-mark title does not fold in the uptime snapshot + connection note:\n%s", tag)
	}
}

func TestThemeGlobalRoundTrip(t *testing.T) {
	t.Setenv("SATELLE_HOME", t.TempDir())
	srv, _ := newServer(t)
	// Default is light.
	if _, body := get(t, srv.URL+"/theme"); !strings.Contains(body, "light") {
		t.Fatalf("default /theme should be light, got %s", body)
	}
	// Persist dark to the machine-wide config.
	resp, err := http.Post(srv.URL+"/theme", "application/x-www-form-urlencoded", strings.NewReader("theme=dark"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("POST /theme status = %d", resp.StatusCode)
	}
	// GET reflects dark, and the project page injects it server-side (no flash).
	if _, body := get(t, srv.URL+"/theme"); !strings.Contains(body, "dark") {
		t.Errorf("/theme not dark after set: %s", body)
	}
	if _, page := get(t, srv.URL+"/"); !strings.Contains(page, `data-theme="dark"`) {
		t.Errorf("project page did not inject the global dark theme")
	}

	// Now switch to light: an EXPLICIT light must be authoritative too — the
	// server injects data-theme="light" so it overrides any stale per-browser
	// localStorage='dark' (the head script only applies localStorage when the
	// server set no data-theme).
	resp, err = http.Post(srv.URL+"/theme", "application/x-www-form-urlencoded", strings.NewReader("theme=light"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if _, body := get(t, srv.URL+"/theme"); !strings.Contains(body, "light") {
		t.Errorf("/theme not light after set: %s", body)
	}
	if _, page := get(t, srv.URL+"/"); !strings.Contains(page, `data-theme="light"`) {
		t.Errorf("project page did not inject the explicit light theme over localStorage")
	}
}

func TestUnknownPath404(t *testing.T) {
	srv, _ := newServer(t)
	if code, _ := get(t, srv.URL+"/nope"); code != 404 {
		t.Errorf("unknown path = %d, want 404", code)
	}
}

func TestFragmentEndpoints(t *testing.T) {
	srv, db := newServer(t)
	ctx := context.Background()
	it, err := db.Stories.Create(ctx, workitem.CreateInput{Kind: workitem.KindStory, Title: "Frag me"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	// Panel rows fragment.
	code, body := get(t, srv.URL+"/fragment/stories")
	if code != 200 || !strings.Contains(body, it.ID) || !strings.Contains(body, `class="row"`) {
		t.Errorf("stories fragment: %d\n%s", code, body)
	}
	// Inline detail fragment.
	code, body = get(t, srv.URL+"/fragment/story/"+it.ID)
	if code != 200 || !strings.Contains(body, "expbody") || !strings.Contains(body, "Timeline") {
		t.Errorf("story detail fragment: %d\n%s", code, body)
	}
}

func TestRealtimeTriggerOnDBChange(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "satelle.db"))
	if err != nil {
		t.Fatal(err)
	}
	verb.SetWorkItemStore(db.Stories)
	verb.SetLedgerStore(db.Ledger)
	verb.SetDocIndexStore(db.DocIndex)
	t.Cleanup(func() {
		db.Close()
		verb.SetWorkItemStore(nil)
		verb.SetLedgerStore(nil)
		verb.SetDocIndexStore(nil)
		verb.SetChangeNotifier(nil)
	})

	a := &app.App{RepoRoot: "/repo", DBPath: "x", Store: db}
	s := web.New(a)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.StartRealtime(ctx, 30*time.Millisecond)

	srv := httptest.NewServer(s.Handler)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/events")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	got := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			if line := sc.Text(); strings.HasPrefix(line, "data: ") {
				got <- strings.TrimPrefix(line, "data: ")
				return
			}
		}
	}()

	// Let the poller seed its baseline, then mutate the store from "another path".
	time.Sleep(80 * time.Millisecond)
	if _, err := db.Stories.Create(context.Background(), workitem.CreateInput{Kind: workitem.KindStory, Title: "live"}, time.Now()); err != nil {
		t.Fatal(err)
	}

	select {
	case topic := <-got:
		if topic != "stories" {
			t.Errorf("trigger topic = %q, want stories", topic)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no realtime trigger within 3s")
	}
}

func TestStoryDetailPageShowsTimeline(t *testing.T) {
	srv, db := newServer(t)
	ctx := context.Background()
	it, err := db.Stories.Create(ctx, workitem.CreateInput{
		Kind: workitem.KindStory, Title: "Trackable story",
		AcceptanceCriteria: "1. it renders", Status: workitem.StatusInProgress,
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	// A ledger event so the timeline is non-empty.
	if _, err := db.Ledger.Append(ctx, ledgerInput(it.ID), time.Now()); err != nil {
		t.Fatal(err)
	}

	code, body := get(t, srv.URL+"/story/"+it.ID)
	if code != 200 {
		t.Fatalf("detail status = %d", code)
	}
	for _, want := range []string{"Trackable story", it.ID, "Acceptance criteria", "it renders", "Timeline", "story_created", `class="crumbs"`} {
		if !strings.Contains(body, want) {
			t.Errorf("detail page missing %q", want)
		}
	}

	// Unknown id → 404.
	if code, _ := get(t, srv.URL+"/story/sty_missing"); code != 404 {
		t.Errorf("missing story = %d, want 404", code)
	}
}

func TestWorkspacePageAggregatesAcrossRepos(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SATELLE_HOME", home)
	ctx := context.Background()

	// Current repo (served) with one story.
	cur := t.TempDir()
	db1, err := store.Open(filepath.Join(cur, ".satelle", "satelle.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db1.Stories.Create(ctx, workitem.CreateInput{Kind: workitem.KindStory, Title: "cur-story"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	verb.SetWorkItemStore(db1.Stories)
	verb.SetLedgerStore(db1.Ledger)
	verb.SetDocIndexStore(db1.DocIndex)

	// A second repo, registered in the workspace registry.
	other := t.TempDir()
	db2, err := store.Open(filepath.Join(other, ".satelle", "satelle.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db2.Stories.Create(ctx, workitem.CreateInput{Kind: workitem.KindStory, Title: "other-story"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	db2.Close()
	gc, _ := config.LoadGlobal()
	gc.Workspace.AddRepo(other)
	if err := config.SaveGlobal(gc); err != nil {
		t.Fatal(err)
	}

	a := &app.App{RepoRoot: cur, DBPath: filepath.Join(cur, ".satelle", "satelle.db"), Store: db1}
	srv := httptest.NewServer(web.Build(a))
	t.Cleanup(func() {
		srv.Close()
		db1.Close()
		verb.SetWorkItemStore(nil)
		verb.SetLedgerStore(nil)
		verb.SetDocIndexStore(nil)
	})

	code, body := get(t, srv.URL+"/workspace")
	if code != 200 {
		t.Fatalf("/workspace = %d", code)
	}
	if !strings.Contains(body, "cur-story") || !strings.Contains(body, "other-story") {
		t.Errorf("workspace page should aggregate both repos' stories; got:\n%s", body)
	}
	// The aggregate is ONE flattened table carrying the PROJECT name as a column
	// (sty_a4633eff): a Project header, each repo's name rendered as a row cell,
	// and the old per-repo section tables gone.
	if !strings.Contains(body, "<th>Project</th>") {
		t.Errorf("workspace table missing the Project column header:\n%s", body)
	}
	for _, repo := range []string{filepath.Base(cur), filepath.Base(other)} {
		if !strings.Contains(body, "<td>"+repo+"</td>") {
			t.Errorf("workspace table missing a Project cell for %q", repo)
		}
	}
	if strings.Contains(body, `class="ws-repo"`) {
		t.Error("workspace page still renders per-repo section tables")
	}
	// The single-repo project page stays single-repo (no other-story).
	_, proj := get(t, srv.URL+"/")
	if strings.Contains(proj, "other-story") {
		t.Error("project page should remain single-repo")
	}
}

var footerVersionRe = regexp.MustCompile(`<span class="footer-version">([^<]*)</span>`)

// TestFooterConsistentAcrossPages asserts the one shared footer (satelle
// <version>) renders identically on the project, help, workspace, doc and detail
// pages — it is one template, not a per-page copy.
func TestFooterConsistentAcrossPages(t *testing.T) {
	srv, db := newServer(t)
	ctx := context.Background()
	it, err := db.Stories.Create(ctx, workitem.CreateInput{Kind: workitem.KindStory, Title: "Footer story"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	db.DocIndex.SetDefaults([]docindex.Doc{{Kind: "documents", Name: "guide", Body: "# Guide\n\nhi"}})

	footer := func(path string) string {
		code, body := get(t, srv.URL+path)
		if code != 200 {
			t.Fatalf("%s = %d", path, code)
		}
		m := footerVersionRe.FindStringSubmatch(body)
		if m == nil {
			t.Fatalf("no shared footer on %s:\n%s", path, body)
		}
		return m[1]
	}

	want := footer("/")
	if !strings.HasPrefix(want, "satelle ") {
		t.Errorf("footer is not 'satelle <version>': %q", want)
	}
	for _, path := range []string{"/help", "/workspace", "/story/" + it.ID, "/doc/documents/guide"} {
		if got := footer(path); got != want {
			t.Errorf("footer on %s = %q, want %q (footers must match)", path, got, want)
		}
	}
}

func TestHelpPageRendersTopics(t *testing.T) {
	srv, _ := newServer(t)
	code, body := get(t, srv.URL+"/help")
	if code != 200 {
		t.Fatalf("/help = %d", code)
	}
	for _, want := range []string{"create-story", "reviewer-checks", "satelle-story-done-review", `class="prose"`} {
		if !strings.Contains(body, want) {
			t.Errorf("/help page missing %q", want)
		}
	}
}

func TestWorkflowTabAndFragment(t *testing.T) {
	srv, db := newServer(t)
	// Seed a workflow on disk: it surfaces through doc-list (the panel) and doc-get
	// (the fragment). Embedded defaults are not listed (sty_94da9ac9), so the panel
	// row requires an on-disk doc.
	body := "---\nname: wf-x\napplies_to: [\"web\"]\n---\nstates:\n  - backlog\n  - done\ntransitions:\n  - {from: backlog, to: done, reviewer_skill: \"x-done-review\"}\n"
	indexDocs(t, db, "workflows", map[string]string{"wf-x": body})

	code, page := get(t, srv.URL+"/")
	if code != 200 || !strings.Contains(page, `data-panel="workflow"`) {
		t.Fatalf("project page missing Workflow tab: %d", code)
	}
	if !strings.Contains(page, "wf-x") || !strings.Contains(page, "fragment/workflow/wf-x") {
		t.Errorf("workflow row/expand-url missing from page")
	}
	code, frag := get(t, srv.URL+"/fragment/workflow/wf-x")
	if code != 200 {
		t.Fatalf("workflow fragment = %d", code)
	}
	for _, want := range []string{"States", "Transitions", "wf-node", "x-done-review", "applies_to",
		"wf-diagram", "wf-dnode", "wf-edge-path"} {
		if !strings.Contains(frag, want) {
			t.Errorf("workflow fragment missing %q", want)
		}
	}
}

// TestFaviconIcoServed asserts the bare /favicon.ico request (what a browser
// issues on a direct-address visit, bypassing the page's <link rel=icon>) gets
// the ◐ SVG mark (sty_a4633eff).
func TestFaviconIcoServed(t *testing.T) {
	srv, _ := newServer(t)
	resp, err := http.Get(srv.URL + "/favicon.ico")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("/favicon.ico = %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "image/svg+xml" {
		t.Errorf("content-type = %q, want image/svg+xml", ct)
	}
	b := make([]byte, 1<<12)
	n, _ := resp.Body.Read(b)
	if !strings.Contains(string(b[:n]), "<svg") {
		t.Errorf("favicon.ico body is not the SVG mark:\n%s", b[:n])
	}
}

// TestProjectHeaderLinks asserts the project page's header meta carries the
// root-absolute workspace link and no projects link (sty_a4633eff): the old
// relative "projects" href resolved per-slug to a 404 on every child.
func TestProjectHeaderLinks(t *testing.T) {
	srv, _ := newServer(t)
	code, body := get(t, srv.URL+"/")
	if code != 200 {
		t.Fatalf("/ = %d", code)
	}
	// The breadcrumb is the up-navigation: workspace / <project name> (sty_89e85f51).
	// It links the ROOT / landing (the canonical connected-projects page), not the
	// heavier /workspace view (sty_ac5b157d).
	if !strings.Contains(body, `<a href="/">workspace</a>`) {
		t.Error("breadcrumb must link the ROOT / landing (absolute href)")
	}
	if strings.Contains(body, `<a href="/workspace">workspace</a>`) {
		t.Error("breadcrumb must not link the heavier /workspace view")
	}
	if !strings.Contains(body, `<span class="cur">repo</span>`) {
		t.Error("breadcrumb current segment must be the project name (base of /repo)")
	}
	// The subtitle's redundant workspace link and the old dynamic tab crumb are gone.
	if strings.Contains(body, "workspace →") {
		t.Error("subtitle must not render the removed workspace → link")
	}
	if strings.Contains(body, `id="crumb-tab"`) {
		t.Error("the dynamic crumb-tab segment must be gone (breadcrumb is now workspace / project)")
	}
	if strings.Contains(body, "projects →") {
		t.Error("project header must not render the removed projects link")
	}
}

// TestBreadcrumbProjectSwitcher proves the breadcrumb <project> segment becomes a
// dropdown when the supervisor injects the project header, and degrades to the plain
// name without it (sty_2bc00a9d).
func TestBreadcrumbProjectSwitcher(t *testing.T) {
	srv, _ := newServer(t)

	// With the supervisor's project header → the switcher renders, listing a
	// sibling and marking the current project (basePath is empty in tests, so
	// current is matched by name == ProjectName "repo").
	// Unique names → each entry shows the NAME only (no suffix), a title tooltip
	// with the path, and the current project marked (basePath empty in tests, so
	// current matches by name == ProjectName "repo").
	body := switcherBody(t, srv, []web.Project{
		{Slug: "repo", Name: "repo", Path: "/home/u/repo"},
		{Slug: "other", Name: "other", Path: "/home/u/other"},
	})
	for _, want := range []string{
		`<details class="proj-switch">`,
		`href="/other/" title="/home/u/other">other</a>`, // link + tooltip, name only
		`aria-current="page">repo</a>`,                   // current, name only
	} {
		if !strings.Contains(body, want) {
			t.Errorf("switcher body missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "proj-slug") {
		t.Errorf("unique names must not render a disambiguating suffix:\n%s", body)
	}

	// Same name in two dirs → BOTH entries show the path to disambiguate.
	dup := switcherBody(t, srv, []web.Project{
		{Slug: "satelle", Name: "satelle", Path: "/a/satelle"},
		{Slug: "satelle-2", Name: "satelle", Path: "/b/satelle"},
	})
	for _, want := range []string{
		`<span class="proj-slug">/a/satelle</span>`,
		`<span class="proj-slug">/b/satelle</span>`,
	} {
		if !strings.Contains(dup, want) {
			t.Errorf("same-name entries must show the path to disambiguate, missing %q:\n%s", want, dup)
		}
	}

	// No header → plain project name, no switcher (single-project / degraded).
	_, plain := get(t, srv.URL+"/")
	if strings.Contains(plain, "proj-switch") {
		t.Error("no-header request must not render the switcher")
	}
	if !strings.Contains(plain, `<span class="cur">repo</span>`) {
		t.Error("no-header request must render the plain project name")
	}
}

// switcherBody fetches / with the project header set to projs and returns the body.
func switcherBody(t *testing.T, srv *httptest.Server, projs []web.Project) string {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/", nil)
	req.Header.Set(web.ProjectsHeader, web.EncodeProjects(projs))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return string(b)
}

// TestBrandMarkAnimatedSVG asserts the topbar brand mark is the inline
// moon-phase SVG (sty_8c00b58a): a SMIL-animated terminator path plus the
// static reduced-motion fallback, using currentColor — no bare ◐ glyph inside
// the anchor (the theme toggle keeps its own glyph).
func TestBrandMarkAnimatedSVG(t *testing.T) {
	srv, _ := newServer(t)
	code, body := get(t, srv.URL+"/")
	if code != 200 {
		t.Fatalf("/ = %d", code)
	}
	i := strings.Index(body, `class="brand-mark"`)
	j := strings.Index(body, `class="theme-toggle"`)
	if i < 0 || j < 0 || i > j {
		t.Fatalf("brand-mark/theme-toggle not found in order: %d %d", i, j)
	}
	mark := body[i:j]
	for _, want := range []string{
		"<svg", `<animate attributeName="d"`, `dur="12s"`,
		"prefers-reduced-motion", `id="static"`, "currentColor",
	} {
		if !strings.Contains(mark, want) {
			t.Errorf("brand mark missing %q:\n%s", want, mark)
		}
	}
	if strings.Contains(mark, ">◐</a>") {
		t.Error("brand mark still renders the bare ◐ text glyph")
	}
}

// TestSpaceGroteskSelfHosted asserts the web UI typography is self-hosted
// (sty_92163102): the Space Grotesk variable woff2 is embedded and served under
// /static/fonts/, and the served stylesheet declares its @font-face and a
// Space Grotesk-first body stack — no external font request anywhere. (The
// family ships no italic face, so a single normal-style face is the whole set.)
func TestSpaceGroteskSelfHosted(t *testing.T) {
	srv, _ := newServer(t)

	for _, f := range []string{"space-grotesk-latin.woff2"} {
		resp, err := http.Get(srv.URL + "/static/fonts/" + f)
		if err != nil {
			t.Fatal(err)
		}
		b := make([]byte, 4)
		n, _ := io.ReadFull(resp.Body, b)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("/static/fonts/%s = %d", f, resp.StatusCode)
		}
		if n != 4 || string(b) != "wOF2" {
			t.Errorf("/static/fonts/%s is not woff2 (magic %q)", f, b[:n])
		}
	}

	code, css := get(t, srv.URL+"/static/app.css")
	if code != 200 {
		t.Fatalf("/static/app.css = %d", code)
	}
	for _, want := range []string{
		`font-family: "Space Grotesk"`,
		"font-weight: 300 700",
		"font-display: swap",
		`url("fonts/space-grotesk-latin.woff2")`,
		`font: 15px/1.5 "Space Grotesk",`,
	} {
		if !strings.Contains(css, want) {
			t.Errorf("app.css missing %q", want)
		}
	}
	if strings.Contains(css, "fonts.googleapis") || strings.Contains(css, "@import") {
		t.Error("stylesheet must not reference an external font host")
	}
	// Mono surfaces keep the mono token (the product's display/mono split).
	if !strings.Contains(css, "--mono: ui-monospace") {
		t.Error("the --mono token must stay monospace")
	}
}
