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

// TestMirrorLoadPanelsFromKindsOnly proves sty_400c022b AC2: pageData fields
// assemble from mirror kinds alone (no repo DB).
func TestMirrorLoadPanelsFromKindsOnly(t *testing.T) {
	s, err := mirror.Open(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	ctx := context.Background()
	now := time.Now().UTC()
	rk := "rk-panels"
	if _, err := s.TouchPartition(ctx, rk, "demo", now); err != nil {
		t.Fatal(err)
	}

	story := workitem.Item{
		ID: "sty_x", Kind: workitem.KindStory, Title: "Panel Story",
		Status: workitem.StatusBacklog, Category: "feature", Priority: "high",
		UpdatedAt: now, CreatedAt: now,
	}
	sb, _ := json.Marshal(story)
	task := workitem.Item{
		ID: "tsk_x", Kind: workitem.KindTask, Title: "Panel Task",
		Status: workitem.StatusBacklog, UpdatedAt: now, CreatedAt: now,
	}
	tb, _ := json.Marshal(task)
	doc := map[string]any{
		"name": "toy", "kind": "workflows", "body": "---\nname: toy\napplies_to: [\"*\"]\n---\n",
		"headline": "toy", "mod_time": now, "provenance": "authored", "source": "/x/toy.md",
	}
	db, _ := json.Marshal(doc)
	led := map[string]any{
		"id": "evt_1", "story_id": "sty_x", "kind": "status_transition",
		"created_at": now, "payload": map[string]string{"from": "backlog", "to": "plan"},
	}
	lb, _ := json.Marshal(led)
	seat, _ := json.Marshal(map[string]any{"id": "sty_x", "in_flight": true, "stale": false})
	ident, _ := json.Marshal(mirror.IdentityMeta{
		ProjectName: "demo", RepoRoot: "/tmp/demo", FooterEmail: "op@example.com",
	})

	for _, r := range []struct {
		kind string
		rows []mirror.ItemRow
	}{
		{"story", []mirror.ItemRow{{ID: "sty_x", Payload: string(sb)}}},
		{"task", []mirror.ItemRow{{ID: "tsk_x", Payload: string(tb)}}},
		{"doc", []mirror.ItemRow{{ID: "toy", Payload: string(db)}}},
		{"ledger_event", []mirror.ItemRow{{ID: "evt_1", Payload: string(lb)}}},
		{"seat", []mirror.ItemRow{{ID: "sty_x", Payload: string(seat)}}},
		{"identity", []mirror.ItemRow{{ID: "meta", Payload: string(ident)}}},
	} {
		if err := s.ReplaceKind(ctx, rk, r.kind, r.rows, now); err != nil {
			t.Fatal(err)
		}
	}

	data, id, err := mirrorLoadPanels(ctx, s, rk, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if id.FooterEmail != "op@example.com" {
		t.Errorf("footer email = %q", id.FooterEmail)
	}
	if data.ProjectName != "demo" || data.RepoRoot != "/tmp/demo" {
		t.Errorf("identity fields: name=%q root=%q", data.ProjectName, data.RepoRoot)
	}
	if data.BacklogCount != 1 {
		t.Errorf("BacklogCount = %d, want 1", data.BacklogCount)
	}
	if len(data.Stories) != 1 || data.Stories[0].Title != "Panel Story" {
		t.Errorf("stories = %+v", data.Stories)
	}
	if len(data.Tasks) != 1 {
		t.Errorf("tasks = %d", len(data.Tasks))
	}
	// AC2: lights assembled from mirror ledger + seat (not empty for engaged backlog).
	if len(data.Stories[0].Lights) == 0 {
		t.Errorf("expected progress lights from mirror ledger/seat, got none: %+v", data.Stories[0])
	}
	if len(data.DocKinds) == 0 {
		t.Error("expected DocKinds groups")
	}
	// Workflows built from workflows kind docs.
	if len(data.Workflows) != 1 || data.Workflows[0].Provenance != "authored" {
		t.Errorf("workflows = %+v", data.Workflows)
	}
	if data.TopBar.MirrorRO != true || data.TopBar.IdentityEmail != "op@example.com" {
		t.Errorf("topbar RO/identity: %+v", data.TopBar)
	}
}

// TestMirrorProjectPageRendersTemplates seeds a partition and checks HTML chrome.
func TestMirrorProjectPageRendersTemplates(t *testing.T) {
	s, err := mirror.Open(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ctx := context.Background()
	now := time.Now().UTC()
	rk := "rk-html"
	if _, err := s.TouchPartition(ctx, rk, "proj", now); err != nil {
		t.Fatal(err)
	}
	story := workitem.Item{
		ID: "sty_h", Kind: workitem.KindStory, Title: "HTML Story",
		Status: workitem.StatusBacklog, Category: "chore",
		UpdatedAt: now, CreatedAt: now,
	}
	sb, _ := json.Marshal(story)
	ident, _ := json.Marshal(mirror.IdentityMeta{ProjectName: "proj", RepoRoot: "/p", FooterEmail: "a@b.c"})
	_ = s.ReplaceKind(ctx, rk, "story", []mirror.ItemRow{{ID: "sty_h", Payload: string(sb)}}, now)
	_ = s.ReplaceKind(ctx, rk, "identity", []mirror.ItemRow{{ID: "meta", Payload: string(ident)}}, now)

	ms := NewMirror(s)
	srv := httptest.NewServer(ms.Handler)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/r/proj/")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	body := string(raw)
	for _, want := range []string{
		"HTML Story", "Stories", "Tasks", "Workflow", "Documents",
		`class="topbar"`, `class="theme-toggle"`, "a@b.c",
		`<base href="/r/proj/">`,
		// sty_eea989dd: identity strip explains RO local UI (no bare "mirror" mode pill).
		`title="Read-only local UI — project data pushed by the CLI (not live-edited here)"`,
		`aria-label="Operator identity a@b.c; read-only local UI, project data pushed by the CLI"`,
		">Install</a>", ">Docs</a>", ">Projects</a>",
		"https://satelle.dev/install", "https://satelle.dev/docs",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("project page missing %q", want)
		}
	}
	if strings.Contains(body, ">mirror</span>") {
		t.Error("project page must not render bare mirror mode pill")
	}

	// Workspace landing.
	resp2, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	raw2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	land := string(raw2)
	if !strings.Contains(land, "workspace") || !strings.Contains(land, `/r/proj/`) {
		t.Errorf("landing missing partition link:\n%s", land)
	}
	// Empty identity on landing: no mode pill (sty_eea989dd).
	if strings.Contains(land, ">mirror</span>") {
		t.Error("landing must not show bare mirror pill when identity is empty")
	}
	for _, want := range []string{">Install</a>", ">Docs</a>", ">Projects</a>"} {
		if !strings.Contains(land, want) {
			t.Errorf("landing missing nav %q", want)
		}
	}
	// Non-ingest POST rejected.
	preq, _ := http.NewRequest(http.MethodPost, srv.URL+"/theme", strings.NewReader("theme=dark"))
	presp, err := http.DefaultClient.Do(preq)
	if err != nil {
		t.Fatal(err)
	}
	presp.Body.Close()
	if presp.StatusCode == 200 || presp.StatusCode == 204 {
		t.Errorf("POST /theme must not succeed, got %d", presp.StatusCode)
	}
}
