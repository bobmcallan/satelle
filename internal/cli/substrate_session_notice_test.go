package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/health"
	"github.com/bobmcallan/satelle/internal/snapsync"
	"github.com/bobmcallan/satelle/internal/syncstate"
)

// Session-start substrate notice (sty_f1a3851d): the substrate line joins the
// release line on the user-visible channel.

const releaseNoticeLine = "⚠️ satelle: a newer release exists — RELEASE-LINE-MARKER"

// sessionNotice runs the real hook verb for claude, with the release line
// stubbed, and returns the top-level systemMessage ("" when absent) and the
// model context.
func sessionNotice(t *testing.T, release, session string) (notice, context string) {
	t.Helper()
	prev := releaseAdvisoryFor
	releaseAdvisoryFor = func(_ *app.App) string { return release }
	t.Cleanup(func() { releaseAdvisoryFor = prev })
	var out, errb bytes.Buffer
	raw := []byte(`{"hook_event_name":"SessionStart","session_id":"` + session + `"}`)
	if err := runHookContext(&out, &errb, "claude", raw); err != nil {
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
	return doc.SystemMessage, doc.HookSpecificOutput.AdditionalContext
}

// AC1: a drifted machine's user notice carries the substrate line beside the
// release line, naming each drifted area with its state and one command.
func TestHookContextSubstrateLineJoinsReleaseNotice(t *testing.T) {
	repo := substrateHookRepo(t, "edited\n", "x\n") // unpushed
	// ...and the last hosted check found it behind.
	if err := syncstate.RecordSubstrate(config.GlobalDir(), repo, map[string]string{"skills": snapsync.StateBehind}, time.Now()); err != nil {
		t.Fatal(err)
	}
	notice, context := sessionNotice(t, releaseNoticeLine, "sub-join")
	lines := strings.Split(notice, "\n")
	if len(lines) != 2 || lines[0] != releaseNoticeLine {
		t.Fatalf("systemMessage must be the release line then the substrate line, got %q", notice)
	}
	for _, want := range []string{"skills unpushed", "skills behind", "satelle sync status"} {
		if !strings.Contains(lines[1], want) {
			t.Errorf("substrate line %q does not carry %q", lines[1], want)
		}
	}
	if n := strings.Count(context, lines[1]); n != 1 {
		t.Errorf("the substrate line must be in model context once, got %d", n)
	}
}

// AC3: in step, or local-only, the substrate adds nothing to the notice.
func TestHookContextSubstrateSilentWhenInStepOrLocalOnly(t *testing.T) {
	substrateHookRepo(t, "x\n", "x\n")
	if notice, _ := sessionNotice(t, releaseNoticeLine, "sub-instep"); notice != releaseNoticeLine {
		t.Errorf("an in-step machine's notice must be only the release line, got %q", notice)
	}
	if notice, _ := sessionNotice(t, "", "sub-instep-bare"); notice != "" {
		t.Errorf("an in-step machine with no release news must have no notice, got %q", notice)
	}

	repo := sessionContextRepo(t)
	writeHarnessTOML(t, repo, "[review]\ngate_create = false\n")
	data := filepath.Join(repo, config.DefaultDataDir, "skills")
	if err := os.MkdirAll(data, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "x.md"), []byte("never synced\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if notice, _ := sessionNotice(t, releaseNoticeLine, "sub-local"); notice != releaseNoticeLine {
		t.Errorf("a local-only substrate must add nothing to the notice, got %q", notice)
	}
}

// AC4: a file with a .local part in a synced area never counts as drift.
func TestHookContextSubstrateIgnoresLocalFiles(t *testing.T) {
	repo := substrateHookRepo(t, "x\n", "x\n")
	data := filepath.Join(repo, config.DefaultDataDir)
	for _, p := range []string{
		filepath.Join(data, "skills", "mine.local.md"),
		filepath.Join(data, config.LocalAgentsName),
	} {
		if err := os.WriteFile(p, []byte("only on this machine\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if notice, _ := sessionNotice(t, "", "sub-dotlocal"); notice != "" {
		t.Errorf(".local files must not raise a substrate notice, got %q", notice)
	}
	// Changing one later is still not drift.
	if err := os.WriteFile(filepath.Join(data, "skills", "mine.local.md"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if notice, _ := sessionNotice(t, "", "sub-dotlocal-2"); notice != "" {
		t.Errorf("changing a .local file must not raise a substrate notice, got %q", notice)
	}
}

func uncheckedFinding(area string) health.Finding {
	return health.Error(health.IDSyncSubstrateUnchecked, "Substrate unchecked", area+": never checked").
		About(area).WithRemediation("run `satelle sync status`")
}

func driftFinding(id, title, area, remedy string) health.Finding {
	return health.Error(id, title, area+": drifted").About(area).WithRemediation(remedy)
}

// AC2: the decision over explicit findings. 'Unverified' is the doctor's
// unchecked finding and nothing else; the refresh starts only for unchecked
// areas and at most once per debounce window.
func TestSubstrateAdvisoryAt(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	push := driftFinding(health.IDSyncSubstrateUnpushed, "Substrate unpushed", "skills", "run `satelle sync push`")
	behind := driftFinding(health.IDSyncSubstrateBehind, "Substrate behind", "workflows", "run `satelle sync pull`")
	unbound := health.Error(health.IDSyncSubstrateUnbound, "Substrate unbound", "no server")
	inStep := health.Info(health.IDSyncSubstrateOk, "Substrate in step", "ok")
	for _, tc := range []struct {
		name     string
		findings health.Findings
		attempt  time.Time
		want     []string // substrings the line carries; nil with wantNot nil → silent
		wantNot  []string
		silent   bool
		refresh  bool
	}{
		{name: "in step", findings: health.Findings{inStep}, silent: true},
		{name: "fresh drift is not unverified", findings: health.Findings{push},
			want: []string{"skills unpushed", "run `satelle sync push`"}, wantNot: []string{"unverified"}},
		{name: "two remedies fall back to sync status", findings: health.Findings{push, behind},
			want: []string{"skills unpushed, workflows behind", "run `satelle sync status` for the next command"}},
		{name: "drift and unchecked", findings: health.Findings{push, uncheckedFinding("documents")},
			want: []string{"skills unpushed", "(unverified: documents not checked recently)", "run `satelle sync push`"}, refresh: true},
		{name: "unchecked only", findings: health.Findings{uncheckedFinding("skills"), uncheckedFinding("documents")},
			want: []string{"substrate not checked", "skills, documents unverified", "run `satelle sync status`"}, wantNot: []string{"not in step"}, refresh: true},
		{name: "unchecked, refresh began inside the window", findings: health.Findings{uncheckedFinding("skills")},
			attempt: now.Add(-substrateRefreshDebounce + time.Second), want: []string{"skills unverified"}},
		{name: "unchecked, refresh began outside the window", findings: health.Findings{uncheckedFinding("skills")},
			attempt: now.Add(-substrateRefreshDebounce), want: []string{"skills unverified"}, refresh: true},
		{name: "unbound is the doctor's finding, a refresh cannot help", findings: health.Findings{unbound, uncheckedFinding("skills")},
			want: []string{"skills unverified"}},
		{name: "unbound alone is silent", findings: health.Findings{unbound}, silent: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			line, refresh := substrateAdvisoryAt(tc.findings, tc.attempt, now)
			if refresh != tc.refresh {
				t.Errorf("refresh = %v, want %v", refresh, tc.refresh)
			}
			if tc.silent && line != "" {
				t.Fatalf("want silence, got %q", line)
			}
			for _, w := range tc.want {
				if !strings.Contains(line, w) {
					t.Errorf("line %q does not carry %q", line, w)
				}
			}
			for _, w := range tc.wantNot {
				if strings.Contains(line, w) {
					t.Errorf("line %q must not carry %q", line, w)
				}
			}
		})
	}
}

// AC2: through the real gatherer. An area last checked longer ago than
// [sync] stale_after is unverified and starts one refresh per window, through
// the seam; with stale_after unset the doctor judges no staleness, so neither
// the line nor a spawn appears.
func TestSubstrateAdvisoryRefreshesThroughTheSeamOncePerWindow(t *testing.T) {
	repo := substrateHookRepo(t, "x\n", "x\n")
	old := time.Now().Add(-48 * time.Hour)
	if err := syncstate.RecordSubstrate(config.GlobalDir(), repo, map[string]string{"skills": snapsync.StateInStep}, old); err != nil {
		t.Fatal(err)
	}
	spawned := 0
	prev := substrateRefresh
	substrateRefresh = func(root string) {
		spawned++
		if root != repo {
			t.Errorf("refresh root = %q, want %q", root, repo)
		}
	}
	t.Cleanup(func() { substrateRefresh = prev })
	a, err := app.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })

	line := substrateAdvisory(a)
	if !strings.Contains(line, "skills unverified") || spawned != 1 {
		t.Fatalf("stale area: line %q, spawned %d (want unverified line and one spawn)", line, spawned)
	}
	if line = substrateAdvisory(a); !strings.Contains(line, "skills unverified") || spawned != 1 {
		t.Fatalf("second session inside the window: line %q, spawned %d (want the line, no new spawn)", line, spawned)
	}

	// No [sync] stale_after: the doctor does not judge staleness, so neither does the advisory.
	writeHarnessTOML(t, repo, "[sync]\nskills = \"personal\"\nproject = \"p\"\n")
	if err := syncstate.MarkSubstrateAttempt(config.GlobalDir(), repo, time.Time{}); err != nil {
		t.Fatal(err)
	}
	b, err := app.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })
	if line = substrateAdvisory(b); line != "" || spawned != 1 {
		t.Fatalf("stale_after unset: line %q, spawned %d (want silence, no spawn)", line, spawned)
	}
}
