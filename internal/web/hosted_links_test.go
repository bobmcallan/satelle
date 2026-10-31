package web

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/mirror"
	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// hostedPartition is a mirror partition holding stories, with hosted as its
// identity's hosted block. With no stories given it holds a story this machine
// engaged and a story another machine changed a minute ago.
func hostedPartition(t *testing.T, hosted *mirror.HostedMeta, stories ...workitem.Item) string {
	t.Helper()
	s, err := mirror.Open(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ctx := context.Background()
	now := time.Now().UTC()
	rk, slug := "rk-hosted", "hosted"
	if _, err := s.TouchPartition(ctx, rk, slug, now); err != nil {
		t.Fatal(err)
	}
	story := func(id, title string, at time.Time) workitem.Item {
		return workitem.Item{ID: id, Kind: workitem.KindStory, Title: title, Status: "in_progress", CreatedAt: at, UpdatedAt: at}
	}
	if len(stories) == 0 {
		stories = []workitem.Item{story("sty_mine", "engaged here", now.Add(-72*time.Hour)), story("sty_other", "changed elsewhere", now.Add(-time.Minute))}
	}
	var rows []mirror.ItemRow
	for _, it := range stories {
		b, _ := json.Marshal(it)
		rows = append(rows, mirror.ItemRow{ID: it.ID, Payload: string(b)})
	}
	ident, _ := json.Marshal(mirror.IdentityMeta{ProjectName: slug, RepoRoot: "/repos/hosted", Hosted: hosted})
	for kind, rows := range map[string][]mirror.ItemRow{
		"story":    rows,
		"identity": {{ID: "meta", Payload: string(ident)}},
	} {
		if err := s.ReplaceKind(ctx, rk, kind, rows, now); err != nil {
			t.Fatal(err)
		}
	}
	ts := httptest.NewServer(NewMirrorWithInstance(s, "test").Handler)
	t.Cleanup(ts.Close)
	return ts.URL + "/r/" + slug + "/"
}

// hostedProbe is the hosted block of a partition whose machine works sty_mine.
func hostedProbe(machine ...string) *mirror.HostedMeta {
	return &mirror.HostedMeta{Server: "https://satelle.example/", Project: "probe", MachineStories: machine}
}

// itemAt is a story with status and tags, created and changed at at.
func itemAt(id, title, status string, at time.Time, tags ...string) workitem.Item {
	return workitem.Item{ID: id, Kind: workitem.KindStory, Title: title, Status: status, Tags: tags, CreatedAt: at, UpdatedAt: at}
}

// sty_dd594bb7 AC1: in hosted mode the project page and the stories fragment list
// every story row in the local store — held or not, whatever its status — where
// sty_0dc600e1 AC5 listed only the stories this machine engaged.
func TestHostedProjectPageListsEveryStory(t *testing.T) {
	now := time.Now().UTC()
	page := hostedPartition(t, hostedProbe("sty_mine"),
		itemAt("sty_mine", "engaged here", "in_progress", now.Add(-72*time.Hour)),
		itemAt("sty_other", "engaged elsewhere", "in_progress", now.Add(-time.Minute)),
		itemAt("sty_back", "waiting", "backlog", now),
		itemAt("sty_done", "finished", "done", now))
	for _, body := range []string{httpGetBody(t, page), httpGetBody(t, page+"fragment/stories")} {
		for _, id := range []string{"sty_mine", "sty_other", "sty_back", "sty_done"} {
			if !strings.Contains(body, "fragment/story/"+id) {
				t.Errorf("%s is in the local store but not listed:\n%s", id, body)
			}
		}
	}
}

// sty_dd594bb7 AC2: an epic's page lists every child, held or not, and the rows
// search reads carry each story's terms, held or not — search filters on the
// client over data-search, so the server-side proof is that unheld rows carry it.
func TestHostedEpicAndSearchCoverUnheldStories(t *testing.T) {
	now := time.Now().UTC()
	epic := itemAt("sty_epic", "the epic", "in_progress", now, "epic:probe")
	epic.Category = "epic-parent"
	page := hostedPartition(t, hostedProbe("sty_held"), epic,
		itemAt("sty_held", "held child zebra", "in_progress", now, "epic:probe"),
		itemAt("sty_unheld", "unheld child walrus", "backlog", now, "epic:probe"))
	view := httpGetBody(t, page+"story/sty_epic")
	for _, id := range []string{"sty_held", "sty_unheld"} {
		if !strings.Contains(view, `href="story/`+id+`"`) {
			t.Errorf("the epic's page does not list its child %s:\n%s", id, view)
		}
	}
	for _, body := range []string{httpGetBody(t, page), httpGetBody(t, page+"fragment/stories")} {
		for id, term := range map[string]string{"sty_held": "zebra", "sty_unheld": "walrus"} {
			row := rowFor(body, id)
			if !strings.Contains(row, `data-search="`) || !strings.Contains(row[strings.Index(row, `data-search="`):], term) {
				t.Errorf("search cannot find %s by %q — its row: %q", id, term, row)
			}
		}
	}
}

// sty_dd594bb7 AC3: a story this machine is working carries a marker on its row,
// another does not, both are listed, and no filter token reads the marker.
func TestHostedMarksThisMachinesStoriesWithoutFiltering(t *testing.T) {
	page := hostedPartition(t, hostedProbe("sty_mine"))
	for _, body := range []string{httpGetBody(t, page), httpGetBody(t, page+"fragment/stories")} {
		mine, other := rowFor(body, "sty_mine"), rowFor(body, "sty_other")
		if mine == "" || other == "" {
			t.Fatalf("both stories must be listed, marked or not:\n%s", body)
		}
		if !strings.Contains(mine, `data-mine="true"`) {
			t.Errorf("the row this machine works carries no marker: %s", mine)
		}
		if strings.Contains(other, "data-mine") {
			t.Errorf("a story another machine works is marked as this machine's: %s", other)
		}
		if n := strings.Count(body, `class="mine-marker"`); n != 1 {
			t.Errorf("the page shows %d mine markers, want 1 (sty_mine's)", n)
		}
	}
	js, err := staticFS.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, filter := range []string{"mine:", "dataset.mine", "data-mine"} {
		if strings.Contains(string(js), filter) {
			t.Errorf("app.js reads the marker (%q): no list may filter by it", filter)
		}
	}
}

// sty_dd594bb7 AC4: the hosted page links no satelle.dev story, project,
// progress, epics or members page — sty_9ece9394 AC4's project link is gone.
func TestHostedProjectPageLinksNoServerPage(t *testing.T) {
	body := httpGetBody(t, hostedPartition(t, hostedProbe("sty_mine")))
	for _, banned := range []string{"hosted-links", "/app/p/", `href="https://satelle.example`} {
		if strings.Contains(body, banned) {
			t.Errorf("the hosted page carries %q:\n%s", banned, body)
		}
	}
	for _, view := range []string{"/stories", "/progress", "/epics", "/members", "/story/"} {
		for _, host := range []string{"https://satelle.dev", "https://satelle.example"} {
			if strings.Contains(body, `href="`+host+"/app") || strings.Contains(body, `href="`+host+view) {
				t.Errorf("the hosted page links the server's %s page on %s:\n%s", view, host, body)
			}
		}
	}
}

// sty_0dc600e1 AC6: a local partition keeps its full story list, links nothing
// to a server and marks no story.
func TestLocalProjectPageListsEveryStoryWithNoServerLinks(t *testing.T) {
	body := httpGetBody(t, hostedPartition(t, nil))
	for _, id := range []string{"sty_mine", "sty_other"} {
		if !strings.Contains(body, "fragment/story/"+id) {
			t.Errorf("the local page does not list %s:\n%s", id, body)
		}
	}
	if strings.Contains(body, "hosted-links") || strings.Contains(body, "data-mine") {
		t.Errorf("a local page links to a server or marks a story:\n%s", body)
	}
}

// sty_9ece9394 AC3, as sty_dd594bb7 AC1/AC3 amend it: in hosted mode a story
// engaged from a story worktree — its working_on row written with no location —
// is marked as this machine's beside the one engaged from the main tree, carrying
// its tags for the client-side tags: filter, and a story another location engaged
// is listed too, unmarked.
func TestHostedListsWorktreeEngagedStoryWithTags(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	now := time.Now().UTC()
	const here = "loc_here_000"
	for id, loc := range map[string]string{"sty_wt": "", "sty_main": here, "sty_other": "loc_other_000"} {
		p, _ := json.Marshal(verb.WorkingOnPayload{Location: loc, State: "in_progress"})
		if _, err := db.Ledger.Append(ctx, ledger.AppendInput{StoryID: id, Kind: ledger.KindWorkingOn, Payload: p}, now); err != nil {
			t.Fatal(err)
		}
	}
	set, err := verb.MachineSet(ctx, db.Ledger, here)
	if err != nil {
		t.Fatal(err)
	}
	var machine []string
	for id := range set {
		machine = append(machine, id)
	}
	story := func(id string, tags ...string) workitem.Item {
		return workitem.Item{ID: id, Kind: workitem.KindStory, Title: id, Status: "in_progress", Tags: tags, CreatedAt: now, UpdatedAt: now}
	}
	page := hostedPartition(t, &mirror.HostedMeta{Server: "https://satelle.example", Project: "probe", MachineStories: machine},
		story("sty_wt", "epic:x"), story("sty_main"), story("sty_other", "epic:x"))
	for _, body := range []string{httpGetBody(t, page), httpGetBody(t, page+"fragment/stories")} {
		for _, id := range []string{"sty_wt", "sty_main"} {
			if !strings.Contains(rowFor(body, id), `data-mine="true"`) {
				t.Errorf("%s, engaged on this machine, is not listed with its marker:\n%s", id, body)
			}
		}
		if row := rowFor(body, "sty_other"); row == "" || strings.Contains(row, "data-mine") {
			t.Errorf("a story another location engaged must be listed, unmarked: %q", row)
		}
		row := rowFor(body, "sty_wt")
		if !strings.Contains(row, `data-tags="epic:x"`) {
			t.Errorf("the worktree-engaged row does not carry its epic:x tag: %s", row)
		}
	}
}

// rowFor is the opening <tr> tag of id's row in body, or "" when it is absent.
func rowFor(body, id string) string {
	end := strings.Index(body, `data-expand-url="fragment/story/`+id+`"`)
	if end < 0 {
		return ""
	}
	start := strings.LastIndex(body[:end], "<tr")
	if start < 0 {
		return ""
	}
	return body[start:end]
}
