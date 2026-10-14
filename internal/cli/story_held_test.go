package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/hosted"
)

type heldJSON struct {
	ID   string `json:"id"`
	Held *struct {
		Holder       string `json:"holder"`
		Location     string `json:"location"`
		Label        string `json:"label"`
		LastSeen     string `json:"last_seen"`
		PresumedDead bool   `json:"presumed_dead"`
		Text         string `json:"text"`
	} `json:"held"`
}

// AC6 (sty_6e5fc41c): `story get` and `story list` mark a story the registry
// records as held elsewhere — in text and as a `held` object — add `presumed
// dead` when its holder has been silent past [sync] hold_stale_after, and leave
// every other story, and the story held here, unmarked.
func TestStoryGetAndListMarkAStoryHeldElsewhere(t *testing.T) {
	fx := newHoldFixture(t, holdNoGit)
	live := createStoryID(t, "held by a live holder")
	dead := createStoryID(t, "held by a silent holder")
	mine := createStoryID(t, "held here")
	free := createStoryID(t, "held by nobody")
	recent := time.Now().UTC().Add(-5 * time.Minute).Format(time.RFC3339)
	for id, rec := range map[string]hosted.HoldRecord{
		live: {Location: otherLoc, Label: "desk", LastSeen: recent, CheckedAt: time.Now()},
		dead: {Location: "loc_silent_bbbbbbbb", LastSeen: otherSeen, CheckedAt: time.Now()},
		mine: {Location: fx.loc, Label: "box", LastSeen: recent},
	} {
		if err := hosted.RecordHold(fx.ts.URL, "probe", fx.repo, id, rec); err != nil {
			t.Fatal(err)
		}
	}

	get := func(id string) (heldJSON, string) {
		out, err := runRoot(t, "story", "get", id)
		if err != nil {
			t.Fatalf("get %s: %v\n%s", id, err, out)
		}
		var row heldJSON
		if err := json.Unmarshal([]byte(out), &row); err != nil {
			t.Fatalf("get %s is not JSON: %v\n%s", id, err, out)
		}
		return row, out
	}

	row, out := get(live)
	if row.Held == nil || row.Held.Holder != "remote desk" || row.Held.Location != otherLoc || row.Held.Label != "desk" || row.Held.LastSeen != recent || row.Held.PresumedDead {
		t.Fatalf("held = %+v, want remote desk, last seen %s, not presumed dead\n%s", row.Held, recent, out)
	}
	if want := "held: remote desk, last seen " + recent; row.Held.Text != want {
		t.Errorf("text = %q, want %q", row.Held.Text, want)
	}
	if !strings.Contains(out, `"title": "held by a live holder"`) || !strings.Contains(out, `"status"`) {
		t.Errorf("the story's own fields must survive the mark:\n%s", out)
	}

	row, _ = get(dead)
	if row.Held == nil || row.Held.Holder != "remote loc_silent_bbbbbbbb" || !row.Held.PresumedDead ||
		row.Held.Text != "held: remote loc_silent_bbbbbbbb, last seen "+otherSeen+", presumed dead" {
		t.Fatalf("held = %+v, want the location named and presumed dead", row.Held)
	}

	for _, id := range []string{mine, free} {
		if row, out := get(id); row.Held != nil || strings.Contains(out, `"held"`) {
			t.Errorf("%s is not held elsewhere and must carry no held mark:\n%s", id, out)
		}
	}

	out, err := runRoot(t, "story", "list", "--limit", "50")
	if err != nil {
		t.Fatalf("list: %v\n%s", err, out)
	}
	var rows []heldJSON
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("list is not JSON: %v\n%s", err, out)
	}
	byID := map[string]heldJSON{}
	for _, r := range rows {
		byID[r.ID] = r
	}
	if r := byID[live]; r.Held == nil || r.Held.Text != "held: remote desk, last seen "+recent {
		t.Errorf("list row for %s: held = %+v", live, r.Held)
	}
	if r := byID[dead]; r.Held == nil || !r.Held.PresumedDead {
		t.Errorf("list row for %s: held = %+v, want presumed dead", dead, r.Held)
	}
	for _, id := range []string{mine, free} {
		if r, ok := byID[id]; !ok || r.Held != nil {
			t.Errorf("list row for %s: present=%v held=%+v, want present and unmarked", id, ok, r.Held)
		}
	}
}

// With no registry the response is returned as it came: no re-encoding, so key
// order and spacing are the verb's own.
func TestMarkHeldStoriesLeavesAnUnheldResponseAlone(t *testing.T) {
	raw := json.RawMessage(`{"id":"sty_a","title":"T"}`)
	if got := markHeldStories(raw, nil); string(got) != string(raw) {
		t.Errorf("got %s, want it untouched", got)
	}
	got := markHeldStories(raw, map[string]heldMark{"sty_a": {Holder: "remote x", Text: "held: remote x"}})
	if !strings.HasPrefix(string(got), `{"id":"sty_a","title":"T","held":{`) {
		t.Errorf("held must be spliced after the row's own members: %s", got)
	}
}
