package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/syncstate"
)

func TestStorySyncLine(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	fresh := now.Add(-time.Minute)
	staleAfter := 2 * time.Hour
	live := hosted.HoldRecord{Location: "loc_a", Label: "desk", LastSeen: "2026-10-14T11:30:00Z", CheckedAt: fresh}
	dead := hosted.HoldRecord{Location: "loc_b", LastSeen: "2026-10-14T05:00:00Z", CheckedAt: fresh}
	conflict := map[string]syncstate.StoryConflict{"sty_c": {Reason: conflictLocalAhead, At: fresh}}

	many := map[string]hosted.HoldRecord{}
	for _, id := range []string{"sty_1", "sty_2", "sty_3", "sty_4", "sty_5", "sty_6", "sty_7"} {
		many[id] = live
	}

	for _, tc := range []struct {
		name      string
		conflicts map[string]syncstate.StoryConflict
		holds     map[string]hosted.HoldRecord
		snapshot  time.Time
		want      []string
		not       []string
		empty     bool
	}{
		{name: "nothing", empty: true},
		{name: "conflict only", conflicts: conflict, snapshot: fresh,
			want: []string{"sty_c in conflict (" + conflictLocalAhead + ")"}, not: []string{"held by", "unverified"}},
		{name: "live holder", holds: map[string]hosted.HoldRecord{"sty_h": live},
			want: []string{"sty_h held by remote desk", "last seen 2026-10-14T11:30:00Z (30m0s ago)"}, not: []string{"presumed dead", "unverified"}},
		{name: "holder without a label is named by its location", holds: map[string]hosted.HoldRecord{"sty_h": dead},
			want: []string{"sty_h held by remote loc_b", "presumed dead"}},
		{name: "conflicts come before holds", conflicts: conflict, snapshot: fresh, holds: map[string]hosted.HoldRecord{"sty_a": live},
			want: []string{"sty_c in conflict"}},
		{name: "a record older than the mirror window is unverified",
			holds: map[string]hosted.HoldRecord{"sty_h": {Location: "loc_a", Label: "desk", LastSeen: "2026-10-14T11:30:00Z", CheckedAt: now.Add(-time.Hour)}},
			want:  []string{"unverified (last checked 2026-10-14 11:00 UTC)"}},
		{name: "a record never checked is unverified", holds: map[string]hosted.HoldRecord{"sty_h": {Location: "loc_a", LastSeen: "2026-10-14T11:30:00Z"}},
			want: []string{"unverified (last checked never)"}},
		{name: "conflicts rest on the last good snapshot", conflicts: conflict, snapshot: now.Add(-2 * time.Hour),
			want: []string{"unverified (last checked 2026-10-14 10:00 UTC)"}},
		{name: "more than five are counted", holds: many,
			want: []string{"sty_1 held", "sty_5 held", "(+2 more)"}, not: []string{"sty_6 held", "sty_7 held"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := storySyncLine(tc.conflicts, tc.holds, tc.snapshot, staleAfter, now)
			if tc.empty {
				if got != "" {
					t.Fatalf("line = %q, want none", got)
				}
				return
			}
			if !strings.HasPrefix(got, "⚠️ satelle:") || strings.Contains(got, "\n") {
				t.Errorf("line must be one ⚠️ satelle: line, got %q", got)
			}
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("line missing %q: %s", w, got)
				}
			}
			for _, n := range tc.not {
				if strings.Contains(got, n) {
					t.Errorf("line must not contain %q: %s", n, got)
				}
			}
		})
	}
	if got := storySyncLine(conflict, many, fresh, staleAfter, now); strings.Index(got, "sty_c") > strings.Index(got, "sty_1") {
		t.Errorf("conflicts must be listed before holds: %s", got)
	}
}

// storySyncHookRepo is a bound repo, with a counting hosted server as the machine
// server so a call made at session start would be seen, whose recorded state
// holds one story in conflict and one held elsewhere.
func storySyncHookRepo(t *testing.T) (calls *atomic.Int32) {
	t.Helper()
	repo := sessionContextRepo(t)
	calls = new(atomic.Int32)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.NotFound(w, r)
	}))
	t.Cleanup(ts.Close)
	if err := os.WriteFile(config.GlobalConfigPath(), []byte("[hosted]\nserver = \""+ts.URL+"\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeHarnessTOML(t, repo, "[sync]\nproject = \"probe\"\nstories = \"personal\"\n")
	a, err := app.Open()
	if err != nil {
		t.Fatal(err)
	}
	root := a.RepoRoot
	_ = a.Close()
	now := time.Now().UTC()
	rec := hosted.HoldRecord{Location: "loc_other_aaaaaaaa", Label: "desk", LastSeen: now.Add(-3 * time.Hour).Format(time.RFC3339), CheckedAt: now}
	if err := hosted.RecordHold(ts.URL, "probe", root, "sty_heldelsewhere", rec); err != nil {
		t.Fatal(err)
	}
	if err := syncstate.RecordSnapshot(config.GlobalDir(), root, true, "", "", now); err != nil {
		t.Fatal(err)
	}
	if err := syncstate.RecordStoryConflicts(config.GlobalDir(), root, map[string]string{"sty_inconflict": conflictLocalAhead}, nil, now); err != nil {
		t.Fatal(err)
	}
	return calls
}

type sessionDoc struct {
	HookSpecificOutput struct {
		AdditionalContext string `json:"additionalContext"`
	} `json:"hookSpecificOutput"`
	SystemMessage string `json:"systemMessage"`
}

// AC3: through the real hook verb the story-sync line is joined with the release
// line in the session notice — both reach Claude's and pi's user as systemMessage
// — and a repo with nothing to report adds no line. SessionStart makes no call.
func TestHookContextStorySyncNoticeJoinsTheReleaseLine(t *testing.T) {
	const release = "⚠️ satelle: a newer release exists — RELEASE-LINE-MARKER"
	prev := releaseAdvisoryFor
	releaseAdvisoryFor = func(*app.App) string { return release }
	t.Cleanup(func() { releaseAdvisoryFor = prev })

	for _, harness := range []string{"claude", "pi"} {
		t.Run(harness, func(t *testing.T) {
			calls := storySyncHookRepo(t)
			var out, errb bytes.Buffer
			raw := []byte(`{"hook_event_name":"SessionStart","session_id":"story-sync-` + harness + `"}`)
			if err := runHookContext(&out, &errb, harness, raw); err != nil {
				t.Fatalf("the hook must exit 0, got %v", err)
			}
			var doc sessionDoc
			if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
				t.Fatalf("not JSON: %v\n%s", err, out.String())
			}
			for _, want := range []string{"RELEASE-LINE-MARKER", "sty_inconflict in conflict", "sty_heldelsewhere held by remote desk", "presumed dead"} {
				if !strings.Contains(doc.SystemMessage, want) {
					t.Errorf("systemMessage missing %q: %s", want, doc.SystemMessage)
				}
			}
			if n := strings.Count(doc.HookSpecificOutput.AdditionalContext, "sty_heldelsewhere held by"); n != 1 {
				t.Errorf("the story-sync line must be in model context once, got %d", n)
			}
			if n := calls.Load(); n != 0 {
				t.Errorf("session start made %d call(s) to the hosted server", n)
			}
		})
	}

	t.Run("nothing recorded adds no line", func(t *testing.T) {
		sessionContextRepo(t)
		var out, errb bytes.Buffer
		raw := []byte(`{"hook_event_name":"SessionStart","session_id":"story-sync-none"}`)
		if err := runHookContext(&out, &errb, "claude", raw); err != nil {
			t.Fatal(err)
		}
		var doc sessionDoc
		if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
			t.Fatalf("not JSON: %v\n%s", err, out.String())
		}
		if doc.SystemMessage != release {
			t.Errorf("systemMessage = %q, want only the release line", doc.SystemMessage)
		}
	})
}

// AC3: grok has no user channel and delivers session context at its first
// PostToolUse (its session-context event), so the line reaches the model only,
// once, on that event.
func TestHookContextStorySyncLineReachesGrokModelOnceOnItsContextEvent(t *testing.T) {
	prev := releaseAdvisoryFor
	releaseAdvisoryFor = func(*app.App) string { return "" }
	t.Cleanup(func() { releaseAdvisoryFor = prev })

	calls := storySyncHookRepo(t)
	event := config.Config{}.SessionContextEvent("grok")
	if strings.EqualFold(event, "SessionStart") {
		t.Fatalf("precondition: grok's session-context event is not SessionStart, got %q", event)
	}
	var out, errb bytes.Buffer
	raw := []byte(`{"hook_event_name":"` + event + `","session_id":"story-sync-grok"}`)
	if err := runHookContext(&out, &errb, "grok", raw); err != nil {
		t.Fatal(err)
	}
	var doc sessionDoc
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out.String())
	}
	if doc.SystemMessage != "" || strings.Contains(out.String(), "systemMessage") {
		t.Errorf("grok has no user channel, got systemMessage in %s", out.String())
	}
	if n := strings.Count(doc.HookSpecificOutput.AdditionalContext, "sty_heldelsewhere held by"); n != 1 {
		t.Errorf("the line must be in model context exactly once, got %d", n)
	}
	if n := calls.Load(); n != 0 {
		t.Errorf("the hook made %d call(s) to the hosted server", n)
	}

	// A SessionStart payload names a different event than grok's channel: nothing.
	out.Reset()
	if err := runHookContext(&out, &errb, "grok", []byte(`{"hook_event_name":"SessionStart","session_id":"story-sync-grok-2"}`)); err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 {
		t.Errorf("grok must emit nothing at SessionStart, got %s", out.String())
	}
}
