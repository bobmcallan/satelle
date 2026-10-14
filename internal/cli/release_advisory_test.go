package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/releasestate"
	"github.com/bobmcallan/satelle/internal/testutil"
)

var releaseNow = time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)

// TestReleaseCurrencyLine (sty_15a374a8 AC4): the decision, over explicit
// inputs. A build AHEAD of the cached latest is silent, as is a dev build — the
// bug this guards is a string-inequality comparison reporting a newer release
// that is older than the running binary.
func TestReleaseCurrencyLine(t *testing.T) {
	fresh := releaseNow.Add(-time.Hour)
	for _, tc := range []struct {
		name         string
		cli, serve   string
		pinned       bool
		st           releasestate.State
		want         []string // substrings the line must carry; nil → silent
		wantNot      []string
		wantSilently bool
	}{
		{name: "newer CLI", cli: "0.0.368", serve: "0.0.7",
			st:   releasestate.State{CLILatest: "v0.0.369", ServeLatest: "serve-v0.0.7", CheckedAt: fresh},
			want: []string{"satelle 0.0.368 → 0.0.369", "`satelle update`"}, wantNot: []string{"satelled", "--local", "unverified"}},
		{name: "newer serve", cli: "0.0.369", serve: "0.0.7",
			st:   releasestate.State{CLILatest: "v0.0.369", ServeLatest: "serve-v0.0.9", CheckedAt: fresh},
			want: []string{"satelled 0.0.7 → 0.0.9", "`satelle update`"}, wantNot: []string{"satelle 0.0.369 →"}},
		{name: "both", cli: "0.0.368", serve: "0.0.7",
			st:   releasestate.State{CLILatest: "v0.0.369", ServeLatest: "serve-v0.0.9", CheckedAt: fresh},
			want: []string{"satelle 0.0.368 → 0.0.369", "satelled 0.0.7 → 0.0.9"}},
		{name: "current", cli: "0.0.369", serve: "0.0.9",
			st:           releasestate.State{CLILatest: "v0.0.369", ServeLatest: "serve-v0.0.9", CheckedAt: fresh},
			wantSilently: true},
		{name: "running ahead of latest is silent", cli: "0.0.400", serve: "0.0.20",
			st:           releasestate.State{CLILatest: "v0.0.369", ServeLatest: "serve-v0.0.9", CheckedAt: fresh},
			wantSilently: true},
		{name: "ordered, not lexical: 0.0.9 is older than 0.0.10", cli: "0.0.9", serve: "",
			st:   releasestate.State{CLILatest: "v0.0.10", CheckedAt: fresh},
			want: []string{"satelle 0.0.9 → 0.0.10"}},
		{name: "dev build", cli: "0.0.0-dev", serve: "0.0.7",
			st:           releasestate.State{CLILatest: "v9.9.9", ServeLatest: "serve-v9.9.9", CheckedAt: fresh},
			wantSilently: true},
		{name: "unparseable running version", cli: "garbage", serve: "",
			st:           releasestate.State{CLILatest: "v0.0.369", CheckedAt: fresh},
			wantSilently: true},
		{name: "satelled not installed", cli: "0.0.369", serve: "",
			st:           releasestate.State{CLILatest: "v0.0.369", ServeLatest: "serve-v0.0.9", CheckedAt: fresh},
			wantSilently: true},
		{name: "stale cache is marked unverified", cli: "0.0.368", serve: "",
			st:   releasestate.State{CLILatest: "v0.0.369", CheckedAt: releaseNow.Add(-48 * time.Hour)},
			want: []string{"satelle 0.0.368 → 0.0.369", "unverified (last checked 2026-10-12 12:00 UTC)"}},
		{name: "pinned repo also names --local", cli: "0.0.368", serve: "", pinned: true,
			st:   releasestate.State{CLILatest: "v0.0.369", CheckedAt: fresh},
			want: []string{"`satelle update`", "`satelle update --local`"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := releaseCurrencyLine(tc.cli, tc.serve, tc.pinned, tc.st, releaseNow)
			if tc.wantSilently {
				if got != "" {
					t.Fatalf("want silence, got %q", got)
				}
				return
			}
			for _, s := range tc.want {
				if !strings.Contains(got, s) {
					t.Errorf("line %q lacks %q", got, s)
				}
			}
			for _, s := range tc.wantNot {
				if strings.Contains(got, s) {
					t.Errorf("line %q must not carry %q", got, s)
				}
			}
			if strings.Contains(got, "\n") {
				t.Errorf("the advisory is one line: %q", got)
			}
		})
	}
}

// TestReleaseAdvisoryAt (AC5): the gatherer reads only the cache, starts the
// detached refresh only for a stale or missing cache with no attempt inside the
// debounce window, and its only write is the checked_attempt stamp.
func TestReleaseAdvisoryAt(t *testing.T) {
	cache := releasestate.State{CLILatest: "v0.0.369", ServeLatest: "serve-v0.0.9", ServeRunning: "0.0.7"}
	for _, tc := range []struct {
		name      string
		state     *releasestate.State // nil → no cache file
		wantSpawn bool
		wantLine  bool
		unverif   bool
	}{
		{"fresh cache: line, no spawn", withChecked(cache, releaseNow.Add(-time.Hour), time.Time{}), false, true, false},
		{"stale cache: unverified line and a spawn", withChecked(cache, releaseNow.Add(-30*time.Hour), time.Time{}), true, true, true},
		{"missing cache: spawn, no line", nil, true, false, false},
		{"stale, attempted 2m ago: no spawn", withChecked(cache, releaseNow.Add(-30*time.Hour), releaseNow.Add(-2*time.Minute)), false, true, true},
		{"stale, attempted 11m ago: spawn", withChecked(cache, releaseNow.Add(-30*time.Hour), releaseNow.Add(-11*time.Minute)), true, true, true},
		{"missing but attempted 2m ago: no spawn", &releasestate.State{CheckedAttempt: releaseNow.Add(-2 * time.Minute)}, false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testutil.IsolateHome(t)
			dir := config.GlobalDir()
			if tc.state != nil {
				if err := releasestate.Record(dir, *tc.state); err != nil {
					t.Fatal(err)
				}
				if !tc.state.CheckedAttempt.IsZero() {
					if err := releasestate.MarkAttempt(dir, tc.state.CheckedAttempt); err != nil {
						t.Fatal(err)
					}
				}
			}
			spawns := 0
			line := releaseAdvisoryAt(dir, "0.0.368", false, releaseNow, func() { spawns++ })
			if (spawns == 1) != tc.wantSpawn || spawns > 1 {
				t.Fatalf("spawns = %d, want spawn=%v", spawns, tc.wantSpawn)
			}
			if (line != "") != tc.wantLine {
				t.Fatalf("line = %q, want line=%v", line, tc.wantLine)
			}
			if strings.Contains(line, "unverified") != tc.unverif {
				t.Fatalf("line = %q, want unverified=%v", line, tc.unverif)
			}
			after, _, _ := releasestate.Load(dir)
			if tc.wantSpawn && !after.CheckedAttempt.Equal(releaseNow) {
				t.Errorf("a spawn must stamp checked_attempt = now, got %v", after.CheckedAttempt)
			}
			// The hook's only write is the stamp: nothing else changed.
			if tc.state != nil && (after.CLILatest != tc.state.CLILatest || !after.CheckedAt.Equal(tc.state.CheckedAt)) {
				t.Errorf("the hook rewrote the cache: before %+v after %+v", *tc.state, after)
			}
		})
	}
}

func withChecked(s releasestate.State, checked, attempt time.Time) *releasestate.State {
	s.CheckedAt, s.CheckedAttempt = checked, attempt
	return &s
}

// TestHookContextReleaseNotice (AC6): through the real hook verb, the release
// line reaches Claude's user as a top-level systemMessage and Grok — which has
// no user channel — only as model context, once, with no systemMessage.
func TestHookContextReleaseNotice(t *testing.T) {
	const line = "⚠️ satelle: a newer release exists — RELEASE-LINE-MARKER"
	prev := releaseAdvisoryFor
	releaseAdvisoryFor = func(_ *app.App) string { return line }
	t.Cleanup(func() { releaseAdvisoryFor = prev })

	for _, tc := range []struct {
		harness, event string
		wantMessage    bool
	}{
		{"claude", "SessionStart", true},
		{"grok", config.Config{}.SessionContextEvent("grok"), false},
	} {
		t.Run(tc.harness, func(t *testing.T) {
			sessionContextRepo(t)
			var out, errb bytes.Buffer
			raw := []byte(`{"hook_event_name":"` + tc.event + `","session_id":"rel-` + tc.harness + `"}`)
			if err := runHookContext(&out, &errb, tc.harness, raw); err != nil {
				t.Fatalf("the hook must exit 0, got %v", err)
			}
			var doc struct {
				HookSpecificOutput struct {
					AdditionalContext string `json:"additionalContext"`
				} `json:"hookSpecificOutput"`
				SystemMessage string `json:"systemMessage"`
			}
			if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
				t.Fatalf("not JSON: %v\n%s", err, out.String())
			}
			if tc.wantMessage {
				if doc.SystemMessage != line {
					t.Errorf("systemMessage = %q, want the release line", doc.SystemMessage)
				}
			} else if doc.SystemMessage != "" || strings.Contains(out.String(), "systemMessage") {
				t.Errorf("%s has no user channel, got systemMessage in %s", tc.harness, out.String())
			}
			if n := strings.Count(doc.HookSpecificOutput.AdditionalContext, "RELEASE-LINE-MARKER"); n != 1 {
				t.Errorf("the release line must be in model context exactly once, got %d", n)
			}
		})
	}
}
