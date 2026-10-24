package cli

import (
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/ledger"
)

// `satelle story hold` is no longer a command (sty_40853d02 AC2); its help topic
// is gone with it.
func TestStoryHoldCommandIsGone(t *testing.T) {
	tempRepo(t)
	for _, sub := range []string{"checkout", "release", "takeover", "show"} {
		out, err := runRoot(t, "story", "hold", sub, "sty_00000000")
		if err == nil || !strings.Contains(err.Error(), `unknown command "hold"`) {
			t.Errorf("story hold %s: err = %v, want cobra's unknown command\n%s", sub, err, out)
		}
	}
	if out, err := runRoot(t, "help", "story-hold"); err == nil && strings.Contains(out, "takeover") {
		t.Errorf("the story-hold help topic is still served:\n%s", out)
	}
}

// ledgerKindCount is how many events of kind the server holds for a story.
func (f *fakeWorkstateServer) ledgerKindCount(project, storyID, kind string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, raw := range f.ledgerByID[project] {
		if m, _ := raw.(map[string]any); m["story_id"] == storyID && m["kind"] == kind {
			n++
		}
	}
	return n
}

// The CLI calls no hosted hold route (sty_40853d02 AC1): the fixture's server
// answers any checkout, release, takeover, checkout-log or per-item hold lookup by
// failing the test, and a story is taken from create through engage, push and close
// — and a push after the close — without one. Nothing is held, so nothing is
// released, and `story get` carries no held mark and no takeover.
func TestEngagePushAndCloseCallNoHoldRoute(t *testing.T) {
	fx := newHoldFixture(t, holdNoGit)
	id := createStoryID(t, "no holds from start to finish")

	if out, err := engageStory(t, id); err != nil {
		t.Fatalf("engage: %v\n%s", err, out)
	}
	if out := fx.push(t); strings.Contains(out, "held elsewhere") || strings.Contains(out, "takeover") {
		t.Errorf("a push after engaging speaks of a hold:\n%s", out)
	}
	if !fx.f.hasItem("probe", id) {
		t.Error("the engaged story was not pushed")
	}

	if out, err := runRoot(t, "story", "set", id, "--status", "done"); err != nil {
		t.Fatalf("close: %v\n%s", err, out)
	}
	out := fx.push(t)
	if strings.Contains(out, "released the hosted hold") || strings.Contains(out, "takeover") {
		t.Errorf("a push after the close speaks of a hold:\n%s", out)
	}
	if got := fx.status(t, id); got != "done" {
		t.Fatalf("status = %q after the close, want done", got)
	}

	get, err := runRoot(t, "story", "get", id)
	if err != nil {
		t.Fatalf("get: %v\n%s", err, get)
	}
	for _, banned := range []string{`"held"`, "held:", "takeover"} {
		if strings.Contains(get, banned) {
			t.Errorf("story get carries %q:\n%s", banned, get)
		}
	}
	// The engagement is on the ledger as a visible event, and it travelled with the story.
	if rows := workingOnRows(t, id); len(rows) != 1 || rows[0].Location != fx.loc {
		t.Errorf("working_on rows = %+v, want one at %s", rows, fx.loc)
	}
	if n := fx.f.ledgerKindCount("probe", id, ledger.KindWorkingOn); n != 1 {
		t.Errorf("the server holds %d working_on event(s) for the story, want the one pushed", n)
	}
}
