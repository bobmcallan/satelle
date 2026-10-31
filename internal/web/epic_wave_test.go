package web

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/mirror"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// epicFixture is a mirror partition holding an epic-parent, three children and
// whatever wave / engagement rows a test seeds. Children: a (startable), b (held on a),
// c (in flight).
type epicFixture struct {
	srv *httptest.Server
	s   *mirror.Store
	rk  string
	now time.Time
}

func newEpicFixture(t *testing.T, wave map[string]any, engagements ...map[string]any) *epicFixture {
	t.Helper()
	s, err := mirror.Open(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ctx := context.Background()
	now := time.Now().UTC()
	rk := "rk-epic"
	if _, err := s.TouchPartition(ctx, rk, "epicp", now); err != nil {
		t.Fatal(err)
	}
	mk := func(id, category string, status string, tags ...string) mirror.ItemRow {
		b, _ := json.Marshal(workitem.Item{
			ID: id, Kind: workitem.KindStory, Title: "T " + id, Status: status,
			Category: category, Tags: tags, UpdatedAt: now, CreatedAt: now,
		})
		return mirror.ItemRow{ID: id, Payload: string(b)}
	}
	stories := []mirror.ItemRow{
		mk("sty_epic", "epic-parent", "ready", "epic:x"),
		mk("sty_a", "fix", "backlog", "epic:x"),
		mk("sty_b", "fix", "backlog", "epic:x", "depends-on:sty_a"),
		mk("sty_c", "fix", "in_progress", "epic:x"),
		mk("sty_plain", "chore", "backlog"),
	}
	if err := s.ReplaceKind(ctx, rk, "story", stories, now); err != nil {
		t.Fatal(err)
	}
	if wave != nil {
		b, _ := json.Marshal(wave)
		if err := s.ReplaceKind(ctx, rk, "wave", []mirror.ItemRow{{ID: "sty_epic", Payload: string(b)}}, now); err != nil {
			t.Fatal(err)
		}
	}
	var engRows []mirror.ItemRow
	for _, e := range engagements {
		b, _ := json.Marshal(e)
		engRows = append(engRows, mirror.ItemRow{ID: e["id"].(string), Payload: string(b)})
	}
	if err := s.ReplaceKind(ctx, rk, "engagement", engRows, now); err != nil {
		t.Fatal(err)
	}
	ident, _ := json.Marshal(mirror.IdentityMeta{ProjectName: "epicp", RepoRoot: "/e", FooterEmail: "a@b.c"})
	_ = s.ReplaceKind(ctx, rk, "identity", []mirror.ItemRow{{ID: "meta", Payload: string(ident)}}, now)
	srv := httptest.NewServer(NewMirror(s).Handler)
	t.Cleanup(srv.Close)
	return &epicFixture{srv: srv, s: s, rk: rk, now: now}
}

func (f *epicFixture) get(t *testing.T, path string) string {
	t.Helper()
	resp, err := http.Get(f.srv.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d: %s", path, resp.StatusCode, raw)
	}
	return string(raw)
}

// memberRow is the members-table row of id in the epic's detail page.
func memberRow(t *testing.T, body, id string) string {
	t.Helper()
	_, after, ok := strings.Cut(body, `<td class="id"><a href="story/`+id+`">`)
	if !ok {
		t.Fatalf("no member row for %s in:\n%s", id, body)
	}
	row, _, _ := strings.Cut(after, "</tr>")
	return row
}

func inFlightEngagement(id string, now time.Time) map[string]any {
	return map[string]any{
		"id": id, "kind": "story", "in_flight": true,
		"agent": "coder", "model": "sonnet", "last_event": "tool: Bash",
		"last_event_at": now.Add(-time.Minute), "activity_started_at": now.Add(-5 * time.Minute),
	}
}

func waveRow() map[string]any {
	return map[string]any{
		"container": "sty_epic", "theme": "epic:x", "schedule": "parallel",
		"runnable": []string{"sty_a"},
		"omitted": []map[string]any{{
			"id": "sty_b", "cause": "waiting_on_dependency",
			"waiting_on": []map[string]any{{"id": "sty_a", "status": "backlog"}},
			"reason":     "sty_b waits on sty_a (backlog)",
		}},
	}
}

// sty_a42f4add AC2: every child shows what the verb said — startable now, or held
// with its cause, reason and each blocker's id and status; a finished child, which
// the verb lists in neither set, carries no wave label.
func TestEpicPageShowsTheWavePerChild(t *testing.T) {
	f := newEpicFixture(t, waveRow())
	body := f.get(t, "/r/epicp/story/sty_epic")

	if a := memberRow(t, body, "sty_a"); !strings.Contains(a, "startable now") || strings.Contains(a, "held") {
		t.Errorf("runnable child row = %q, want startable now and not held", a)
	}
	b := memberRow(t, body, "sty_b")
	for _, want := range []string{"held", "waiting_on_dependency", "sty_b waits on sty_a (backlog)", `href="story/sty_a"`, `badge s-backlog`} {
		if !strings.Contains(b, want) {
			t.Errorf("held child row missing %q: %s", want, b)
		}
	}
	if strings.Contains(b, "startable now") {
		t.Errorf("held child row says startable now: %s", b)
	}
	if c := memberRow(t, body, "sty_c"); strings.Contains(c, "startable now") || strings.Contains(c, "held") {
		t.Errorf("a child the verb lists in neither set must carry no wave label: %s", c)
	}
	if !strings.Contains(body, "wave schedule: parallel") {
		t.Errorf("page does not name the schedule the verb applied")
	}
}

// The verb's refusal is shown in place of the wave, and no child is labelled.
func TestEpicPageShowsTheVerbRefusal(t *testing.T) {
	const refusal = "story-wave: sty_epic: epic:x names two parents — no child is runnable"
	f := newEpicFixture(t, map[string]any{"container": "sty_epic", "error": refusal})
	body := f.get(t, "/r/epicp/story/sty_epic")
	if !strings.Contains(body, refusal) {
		t.Errorf("page does not show the refusal %q", refusal)
	}
	for _, id := range []string{"sty_a", "sty_b", "sty_c"} {
		if row := memberRow(t, body, id); strings.Contains(row, "startable now") || strings.Contains(row, "held") {
			t.Errorf("%s labelled although the verb refused: %s", id, row)
		}
	}
}

// No wave pushed (an older CLI): members render as before, no wave column text.
func TestEpicPageWithoutAWaveRowRendersMembersOnly(t *testing.T) {
	f := newEpicFixture(t, nil)
	body := f.get(t, "/r/epicp/story/sty_epic")
	if row := memberRow(t, body, "sty_a"); strings.Contains(row, "startable now") || strings.Contains(row, "held") {
		t.Errorf("no wave row, yet the member is labelled: %s", row)
	}
}

// sty_a42f4add AC3: a child with an in-flight dispatch shows the story list's
// running indicator on the epic's page; a sibling without one shows none.
func TestEpicPageShowsEachChildsRunningIndicator(t *testing.T) {
	f := newEpicFixture(t, waveRow(), inFlightEngagement("sty_c", time.Now().UTC()))
	body := f.get(t, "/r/epicp/story/sty_epic")
	c := memberRow(t, body, "sty_c")
	for _, want := range []string{`class="dispatch-indicator"`, "coder (sonnet) running", "tool: Bash"} {
		if !strings.Contains(c, want) {
			t.Errorf("in-flight child row missing %q: %s", want, c)
		}
	}
	for _, id := range []string{"sty_a", "sty_b"} {
		if row := memberRow(t, body, id); strings.Contains(row, "dispatch-indicator") {
			t.Errorf("%s has no dispatch yet shows an indicator: %s", id, row)
		}
	}
	// A settled engagement is not a running dispatch.
	settled := inFlightEngagement("sty_a", time.Now().UTC())
	settled["in_flight"] = false
	g := newEpicFixture(t, waveRow(), settled)
	if row := memberRow(t, g.get(t, "/r/epicp/story/sty_epic"), "sty_a"); strings.Contains(row, "dispatch-indicator") {
		t.Errorf("a settled engagement rendered a running indicator: %s", row)
	}
}

// sty_a42f4add AC4: while any child has a dispatch in flight the epic's page and its
// list row say agent work is in progress and how many children; with none they say
// nothing. The list row of a non-epic carries no roll-up.
func TestEpicRollupOnDetailAndListRow(t *testing.T) {
	const marker = "agent work in progress"
	cases := []struct {
		name        string
		engagements []map[string]any
		want        string // "" = no roll-up
	}{
		{"none", nil, ""},
		{"one", []map[string]any{inFlightEngagement("sty_a", time.Now().UTC())}, "1 child"},
		{"two", []map[string]any{inFlightEngagement("sty_a", time.Now().UTC()), inFlightEngagement("sty_c", time.Now().UTC())}, "2 children"},
		{"an unrelated story's dispatch does not count", []map[string]any{inFlightEngagement("sty_plain", time.Now().UTC())}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newEpicFixture(t, waveRow(), tc.engagements...)

			detail := f.get(t, "/r/epicp/story/sty_epic")
			list := f.get(t, "/r/epicp/")
			_, epicRow, _ := strings.Cut(list, `data-expand-url="fragment/story/sty_epic"`)
			epicRow, _, _ = strings.Cut(epicRow, "</tr>")
			_, aRow, _ := strings.Cut(list, `data-expand-url="fragment/story/sty_a"`)
			aRow, _, _ = strings.Cut(aRow, "</tr>")

			if tc.want == "" {
				if strings.Contains(detail, marker) || strings.Contains(epicRow, marker) {
					t.Errorf("roll-up text shown with nothing in flight")
				}
				if strings.Contains(detail, "epic-rollup") || strings.Contains(epicRow, "epic-rollup") {
					t.Errorf("roll-up shown with %d in-flight children", len(tc.engagements))
				}
				return
			}
			for name, got := range map[string]string{"detail page": detail, "list row": epicRow} {
				if !strings.Contains(got, marker+" · "+tc.want) {
					t.Errorf("%s missing %q", name, marker+" · "+tc.want)
				}
			}
			if strings.Contains(aRow, "epic-rollup") {
				t.Errorf("a child's list row carries the epic roll-up: %s", aRow)
			}
		})
	}
}
