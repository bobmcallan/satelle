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

// sty_0dc600e1 AC5: in hosted mode the project page lists only the stories this
// machine engaged, not a recent story changed only by another machine.
// sty_9ece9394 AC4: it links only pages the server serves — the project page at
// /app/p/<project> — and nothing for progress, epics or members.
func TestHostedProjectPageListsThisMachinesStoriesAndLinksTheServer(t *testing.T) {
	page := hostedPartition(t, &mirror.HostedMeta{Server: "https://satelle.example/", Project: "probe", MachineStories: []string{"sty_mine"}})
	body := httpGetBody(t, page)
	if !strings.Contains(body, "fragment/story/sty_mine") {
		t.Errorf("the engaged story is not listed:\n%s", body)
	}
	if strings.Contains(body, "fragment/story/sty_other") {
		t.Errorf("a story changed only by another machine is listed:\n%s", body)
	}
	if want := `href="https://satelle.example/app/p/probe"`; !strings.Contains(body, want) {
		t.Errorf("the page lacks %s:\n%s", want, body)
	}
	for _, view := range []string{"/probe/stories", "/progress", "/epics", "/members"} {
		if strings.Contains(body, view+`"`) {
			t.Errorf("the page links %s, which the server does not serve:\n%s", view, body)
		}
	}
	if rows := httpGetBody(t, page+"fragment/stories"); strings.Contains(rows, "sty_other") || !strings.Contains(rows, "sty_mine") {
		t.Errorf("the stories fragment must list the engaged story only:\n%s", rows)
	}
}

// sty_0dc600e1 AC6: a local partition keeps its full story list and links nothing
// to a server.
func TestLocalProjectPageListsEveryStoryWithNoServerLinks(t *testing.T) {
	body := httpGetBody(t, hostedPartition(t, nil))
	for _, id := range []string{"sty_mine", "sty_other"} {
		if !strings.Contains(body, "fragment/story/"+id) {
			t.Errorf("the local page does not list %s:\n%s", id, body)
		}
	}
	if strings.Contains(body, "hosted-links") {
		t.Errorf("a local page links to a server:\n%s", body)
	}
}

// sty_9ece9394 AC3: in hosted mode a story engaged from a story worktree — its
// working_on row written with no location — is listed beside the one engaged from
// the main tree, carrying its tags for the client-side tags: filter, while a story
// another location engaged is not.
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
			if !strings.Contains(body, "fragment/story/"+id) {
				t.Errorf("%s, engaged on this machine, is not listed:\n%s", id, body)
			}
		}
		if strings.Contains(body, "fragment/story/sty_other") {
			t.Errorf("a story another location engaged is listed:\n%s", body)
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
