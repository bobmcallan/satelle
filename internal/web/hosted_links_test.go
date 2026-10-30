package web

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/mirror"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// hostedPartition is a mirror partition holding a story this machine engaged and
// a story another machine changed a minute ago, with hosted as its identity's
// hosted block.
func hostedPartition(t *testing.T, hosted *mirror.HostedMeta) string {
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
	row := func(id, title string, at time.Time) mirror.ItemRow {
		b, _ := json.Marshal(workitem.Item{ID: id, Kind: workitem.KindStory, Title: title, Status: "in_progress", CreatedAt: at, UpdatedAt: at})
		return mirror.ItemRow{ID: id, Payload: string(b)}
	}
	ident, _ := json.Marshal(mirror.IdentityMeta{ProjectName: slug, RepoRoot: "/repos/hosted", Hosted: hosted})
	for kind, rows := range map[string][]mirror.ItemRow{
		"story":    {row("sty_mine", "engaged here", now.Add(-72*time.Hour)), row("sty_other", "changed elsewhere", now.Add(-time.Minute))},
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

var serverViews = []string{"/stories", "/progress", "/epics", "/members"}

// sty_0dc600e1 AC5: in hosted mode the project page lists only the stories this
// machine engaged, not a recent story changed only by another machine, and links
// the full story list, progress, epics and members to the server.
func TestHostedProjectPageListsThisMachinesStoriesAndLinksTheServer(t *testing.T) {
	page := hostedPartition(t, &mirror.HostedMeta{Server: "https://satelle.example/", Project: "probe", MachineStories: []string{"sty_mine"}})
	body := httpGetBody(t, page)
	if !strings.Contains(body, "fragment/story/sty_mine") {
		t.Errorf("the engaged story is not listed:\n%s", body)
	}
	if strings.Contains(body, "fragment/story/sty_other") {
		t.Errorf("a story changed only by another machine is listed:\n%s", body)
	}
	for _, view := range serverViews {
		if want := `href="https://satelle.example/probe` + view + `"`; !strings.Contains(body, want) {
			t.Errorf("the page lacks %s:\n%s", want, body)
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
