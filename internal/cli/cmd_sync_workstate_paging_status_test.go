package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// A story's status follows its latest hosted status_transition. Paged, the
// transition can arrive on a later page than the story; the status is forwarded
// once the pass has delivered it, not decided early and left behind.
func TestWorkstateStatusFollowsTransitionOnLaterPage(t *testing.T) {
	_, f, _ := pagingEnv(t, 1)
	t0 := time.Now().UTC().Add(-2 * time.Hour)
	t1 := t0.Add(time.Hour)
	stamp := func(item, led string) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if item != "" {
			f.itemVer = f.stamp(f.itemVer, "probe", item)
		}
		if led != "" {
			f.ledgerVer = f.stamp(f.ledgerVer, "probe", led)
		}
	}
	transition := func(id, to string, at time.Time) {
		seedHostedLedger(f, id, "sty_pgstat", "status_transition", map[string]any{"to": to}, at)
		stamp("", id)
	}

	seedHostedStory(f, "sty_pgstat", "Moves on", "backlog", t0)
	stamp("sty_pgstat", "")
	transition("led_a_first", "backlog", t0)
	if out, err := runRoot(t, "sync", "workstate", "snapshot"); err != nil {
		t.Fatalf("first snapshot: %v\n%s", err, out)
	}

	// Hosted moves the story on. Its transition sorts after another changed ledger
	// row, so with one row per page it lands on page 2 and the story on page 1.
	seedHostedStory(f, "sty_pgstat", "Moves on", "plan", t1)
	stamp("sty_pgstat", "")
	seedHostedLedger(f, "led_b_note", "sty_pgstat", "note", map[string]any{"n": 1}, t1)
	stamp("", "led_b_note")
	transition("led_c_last", "plan", t1)

	out, err := runRoot(t, "sync", "workstate", "snapshot")
	if err != nil {
		t.Fatalf("second snapshot: %v\n%s", err, out)
	}
	if !strings.Contains(out, "(since pass, 2 page(s))") {
		t.Fatalf("second snapshot = %q, want a two-page since pass", out)
	}
	got, err := runRoot(t, "story", "get", "sty_pgstat")
	if err != nil {
		t.Fatalf("get: %v\n%s", err, got)
	}
	var story struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(got), &story); err != nil {
		t.Fatalf("parse story: %v\n%s", err, got)
	}
	if story.Status != "plan" {
		t.Fatalf("status = %q, want plan once the transition on page 2 landed", story.Status)
	}
}
