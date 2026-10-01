package cli

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/mirror"
	"github.com/bobmcallan/satelle/internal/web"
	"github.com/bobmcallan/satelle/internal/workitem"
	"github.com/bobmcallan/satelle/internal/workspace"
)

// sty_9f4f8e12 AC7: the close-gate payload, the web story view, the CLI route
// view and the workspace aggregate report ONE member set for a container. The
// fixture is built so the old parent_id rule and the epic: tag rule disagree: a
// tag-only child, a child with both, a parent_id link with no tag (not a
// member), and an unrelated story.
func TestEpicSurfacesAgreeOnOneContainer(t *testing.T) {
	repo := tempRepo(t)
	if _, err := runRoot(t, "init"); err != nil {
		t.Fatalf("init: %v", err)
	}
	a, err := app.Open()
	if err != nil {
		t.Fatalf("open app in %s: %v", repo, err)
	}
	t.Cleanup(func() { _ = a.Close() })

	ctx := context.Background()
	now := time.Now().UTC()
	mk := func(category, parent string, status string, tags ...string) workitem.Item {
		t.Helper()
		it, err := a.Store.Stories.Create(ctx, workitem.CreateInput{
			Kind: workitem.KindStory, Title: "t", Body: "b", AcceptanceCriteria: "1. ok",
			Status: status, Category: category, ParentID: parent, Tags: tags,
		}, now)
		if err != nil {
			t.Fatal(err)
		}
		return it
	}
	epic := mk("epic-parent", "", "backlog", "epic:surf")
	tagOnly := mk("fix", "", "backlog", "epic:surf")
	both := mk("substrate", epic.ID, "done", "epic:surf")
	_ = mk("fix", epic.ID, "backlog")      // parent_id link, no tag: not a member
	_ = mk("fix", "", "backlog", "epic:x") // another epic's child
	want := []string{tagOnly.ID, both.ID}
	sort.Strings(want)

	norm := func(ids []string) []string {
		sort.Strings(ids)
		return ids
	}

	// 1. The close-gate payload's children.
	var gate []string
	for _, c := range childrenResolver(a)(ctx, epic) {
		gate = append(gate, c.ID)
	}

	// 2. The CLI route view.
	out, err := runRoot(t, "story", "route", epic.ID)
	if err != nil {
		t.Fatalf("story route: %v\n%s", err, out)
	}
	if !strings.Contains(out, "## Members") {
		t.Fatalf("the route view must list the container's members:\n%s", out)
	}
	cliIDs := regexp.MustCompile(`(?m)^- (sty_[0-9a-f]+) — `).FindAllStringSubmatch(out[strings.Index(out, "## Members"):], -1)
	var route []string
	for _, m := range cliIDs {
		route = append(route, m[1])
	}

	// 3. The workspace aggregate.
	agg := workspace.Load(ctx, []string{repo})
	if len(agg.Repos) != 1 || agg.Repos[0].Err != "" {
		t.Fatalf("workspace load: %+v", agg.Repos)
	}
	refs, err := agg.Repos[0].Members(epic.ID)
	if err != nil {
		t.Fatal(err)
	}
	var ws []string
	for _, r := range refs {
		ws = append(ws, r.ID)
	}

	// 4. The web story view, served from a mirror holding the same stories.
	ms, err := mirror.Open(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ms.Close() })
	all, err := a.Store.Stories.List(ctx, workitem.ListFilter{Kind: workitem.KindStory})
	if err != nil {
		t.Fatal(err)
	}
	rows := make([]mirror.ItemRow, 0, len(all))
	for _, it := range all {
		b, _ := json.Marshal(it)
		rows = append(rows, mirror.ItemRow{ID: it.ID, Payload: string(b)})
	}
	rk := "rk-epic-surfaces"
	if _, err := ms.TouchPartition(ctx, rk, "surf", now); err != nil {
		t.Fatal(err)
	}
	if err := ms.ReplaceKind(ctx, rk, "story", rows, now); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(web.NewMirror(ms).Handler)
	t.Cleanup(srv.Close)
	resp, err := srv.Client().Get(srv.URL + "/r/surf/story/" + epic.ID)
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	html := string(page)
	i := strings.Index(html, `class="panel-table epic-members"`)
	if i < 0 {
		t.Fatalf("the story view must list the container's members:\n%s", html)
	}
	var webIDs []string
	for _, m := range regexp.MustCompile(`<td class="id"><a href="story/(sty_[0-9a-f]+)">`).FindAllStringSubmatch(html[i:], -1) {
		webIDs = append(webIDs, m[1])
	}

	for name, got := range map[string][]string{"close-gate payload": gate, "CLI route view": route, "workspace aggregate": ws, "web story view": webIDs} {
		g := norm(append([]string(nil), got...))
		if strings.Join(g, ",") != strings.Join(want, ",") {
			t.Errorf("%s reports %v, want %v", name, g, want)
		}
	}
}
