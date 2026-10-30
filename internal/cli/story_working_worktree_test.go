package cli

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/verb"
)

// gitHoldFixture is a hold fixture whose bound repo is a git repository, so its
// story store is the one a linked worktree of it resolves too.
func gitHoldFixture(t *testing.T) *holdFixture {
	t.Helper()
	fx := newHoldFixture(t, holdNoGit)
	gitInitCommit(t, fx.repo)
	return fx
}

// worktreeOf adds a linked worktree of fx's repo and leaves the process in it: a
// story engaged here is engaged from a `satelle story worktree` tree on the same
// machine. Nothing names the main tree's config, so the worktree's commands
// resolve the way every worktree command does.
func worktreeOf(t *testing.T, fx *holdFixture) string {
	t.Helper()
	wt := linkedWorktree(t, fx.repo)
	t.Chdir(wt)
	t.Setenv("SATELLE_CONFIG", "")
	return wt
}

// sty_9ece9394 AC1: a story engaged from a linked worktree writes a working_on row
// naming this machine's location — the one its main checkout registered — with
// the worktree kept as the row's worktree.
func TestWorktreeEngageWritesMainLocation(t *testing.T) {
	fx := gitHoldFixture(t)
	if fx.loc == "" {
		t.Fatal("the main checkout has no location")
	}
	id := createStoryID(t, "engaged from a worktree")
	wt := worktreeOf(t, fx)
	if out, err := engageStory(t, id); err != nil {
		t.Fatalf("engage from the worktree: %v\n%s", err, out)
	}

	a, err := app.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	var rows []verb.WorkingOnPayload
	err = a.Store.Ledger.ForEachKind(t.Context(), "", ledger.KindWorkingOn, func(e ledger.Entry) error {
		var p verb.WorkingOnPayload
		if e.StoryID == id && json.Unmarshal(e.Payload, &p) == nil {
			rows = append(rows, p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("engaging wrote %d working_on rows, want 1: %+v", len(rows), rows)
	}
	r := rows[0]
	if r.Location != fx.loc {
		t.Errorf("row location = %q, want the main checkout's %q", r.Location, fx.loc)
	}
	if want := hosted.LocationLabel(fx.repo); r.Label != want {
		t.Errorf("row label = %q, want %q", r.Label, want)
	}
	if r.Worktree != wt {
		t.Errorf("row worktree = %q, want %q", r.Worktree, wt)
	}
}

// sty_9ece9394 AC1: read from a linked worktree, a row naming this machine's
// location is this machine's own work, not another worker's, while a row naming
// another location still is.
func TestOtherWorkingSkipsOwnWorktreeRow(t *testing.T) {
	fx := gitHoldFixture(t)
	mine := createStoryID(t, "worked on here")
	theirs := createStoryID(t, "worked on elsewhere")
	worktreeOf(t, fx)

	a, err := app.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	now := time.Now().UTC()
	appendWorkingOn(t, a, mine, verb.WorkingOnPayload{Location: fx.loc, State: "in_progress"}, now.Add(-time.Hour))
	appendWorkingOn(t, a, theirs, verb.WorkingOnPayload{Location: otherLoc, Label: "desk", State: "in_progress"}, now.Add(-time.Hour))

	got := otherWorking(t.Context(), a)
	if _, ok := got[mine]; ok {
		t.Errorf("a story this machine works on is listed as another worker's: %+v", got[mine])
	}
	if r, ok := got[theirs]; !ok || r.Location != otherLoc {
		t.Errorf("the story another location works on is not listed: %+v", got)
	}
}
