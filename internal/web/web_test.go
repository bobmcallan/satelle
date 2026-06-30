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

func TestUptimeButtonRendered(t *testing.T) {
	srv, _ := newServer(t)
	code, body := get(t, srv.URL+"/")
	if code != 200 {
		t.Fatalf("status = %d", code)
	}
	// A clear, non-pressable (disabled) uptime button shows in the header.
	if !strings.Contains(body, `class="uptime"`) || !strings.Contains(body, "disabled") {
		t.Errorf("header missing the clear (disabled) uptime button")
	}
	if !strings.Contains(body, "up ") {
		t.Errorf("uptime button missing the 'up …' elapsed text")
	}
	// The tooltip is reconciled with actual behaviour (sty_efeb2a69): it states the
	// value is a page-load snapshot ("at page load") AND that the green border is the
	// live-connection signal ("live updates connected") — not the old misleading
	// "web service uptime — green border means up".
	if !strings.Contains(body, "at page load") || !strings.Contains(body, "live updates connected") {
		t.Errorf("uptime tooltip not reconciled to describe the snapshot value + the connection border")
	}
	if strings.Contains(body, "green border means up") {
		t.Errorf("the old misleading uptime tooltip is still present")
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
