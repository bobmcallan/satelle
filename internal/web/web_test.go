package web_test

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
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
	// Seed a workflow via the embedded-defaults overlay (no disk needed): it
	// surfaces through doc-list (the panel) and doc-get (the fragment).
	body := "---\nname: wf-x\napplies_to: [\"web\"]\n---\nstates:\n  - backlog\n  - done\ntransitions:\n  - {from: backlog, to: done, reviewer_skill: \"x-done-review\"}\n"
	db.DocIndex.SetDefaults([]docindex.Doc{{Kind: "workflows", Name: "wf-x", Body: body}})

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
