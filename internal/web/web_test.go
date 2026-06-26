package web_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/app"
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

func TestUnknownPath404(t *testing.T) {
	srv, _ := newServer(t)
	if code, _ := get(t, srv.URL+"/nope"); code != 404 {
		t.Errorf("unknown path = %d, want 404", code)
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
	for _, want := range []string{"Trackable story", it.ID, "Acceptance criteria", "it renders", "Timeline", "story_created", "← project"} {
		if !strings.Contains(body, want) {
			t.Errorf("detail page missing %q", want)
		}
	}

	// Unknown id → 404.
	if code, _ := get(t, srv.URL+"/story/sty_missing"); code != 404 {
		t.Errorf("missing story = %d, want 404", code)
	}
}
