package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/testutil"
)

// noHyperlinks pins the OSC 8 capability off (the default for an unknown
// terminal) so line assertions are about content, not escapes.
func noHyperlinks(t *testing.T) {
	t.Helper()
	prev := hyperlinksEnabled
	hyperlinksEnabled = func() bool { return false }
	t.Cleanup(func() { hyperlinksEnabled = prev })
}

func withHyperlinks(t *testing.T) {
	t.Helper()
	prev := hyperlinksEnabled
	hyperlinksEnabled = func() bool { return true }
	t.Cleanup(func() { hyperlinksEnabled = prev })
}

// TestStatusLineCarriesLinkAndStoryStage (sty_4e6f0788 AC1): one line carrying
// the server URL and the engaged work as <story_id>::<stage>.
func TestStatusLineCarriesLinkAndStoryStage(t *testing.T) {
	noHyperlinks(t)
	live := webAvailability{Port: 8787, Live: true, Resolved: true}
	info := seatInfo{ItemID: "sty_abc123", StoryStatus: "in_progress", State: "integration"}

	got := formatStatusLine(live, info, true)
	if !strings.Contains(got, "http://localhost:8787") {
		t.Fatalf("line must carry the server URL: %q", got)
	}
	if !strings.Contains(got, "sty_abc123::in_progress") {
		t.Fatalf("line must carry <story_id>::<stage>: %q", got)
	}
	// The COMMITTED status is the stage, not the lease's in-flight target —
	// so the line agrees with `satelle story get`.
	if strings.Contains(got, "::integration") {
		t.Fatalf("stage must be the committed status, not the lease target: %q", got)
	}
	if strings.Count(got, "\n") != 0 {
		t.Fatalf("must be a single line: %q", got)
	}
}

// TestStatusLineReflectsReachability (AC2): a dead service renders visibly
// differently and is never presented as an openable link.
func TestStatusLineReflectsReachability(t *testing.T) {
	withHyperlinks(t) // even where links CAN render, a dead service gets none
	live := webAvailability{Port: 8787, Live: true, Resolved: true}
	dead := webAvailability{Port: 8787, Live: false, Resolved: true}
	unknown := webAvailability{Port: 8787}

	liveOut := serverSegment(live)
	deadOut := serverSegment(dead)
	if liveOut == deadOut {
		t.Fatal("live and dead server segments are identical")
	}
	if !strings.Contains(liveOut, "\x1b]8;;") {
		t.Fatalf("a live service should be linked where supported: %q", liveOut)
	}
	if strings.Contains(deadOut, "\x1b]8;;") {
		t.Fatalf("a dead service must not be a clickable link: %q", deadOut)
	}
	if !strings.Contains(deadOut, "down") {
		t.Fatalf("dead service must say so: %q", deadOut)
	}
	if strings.Contains(unknown.URL(), "0") && strings.Contains(serverSegment(unknown), "8787") {
		t.Fatalf("unresolved config must not present a port as fact: %q", serverSegment(unknown))
	}
}

// TestStatusLineNoStoryIsPlain (AC3): no engaged story reads plainly rather
// than as an empty, stale, or malformed segment.
func TestStatusLineNoStoryIsPlain(t *testing.T) {
	noHyperlinks(t)
	for _, tc := range []struct {
		name    string
		info    seatInfo
		engaged bool
	}{
		{"no lease at all", seatInfo{}, false},
		{"stale residue not engaged", seatInfo{ItemID: "sty_old", StoryStatus: "plan"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := storySegment(tc.info, tc.engaged)
			if got != noStorySegment {
				t.Fatalf("want %q, got %q", noStorySegment, got)
			}
			if strings.Contains(got, "::") || strings.Contains(got, "sty_") {
				t.Fatalf("must not emit a story segment when nothing is engaged: %q", got)
			}
		})
	}
}

// TestStatusLineDegradesWithoutOSC8 (AC4): on a terminal that cannot render
// hyperlinks the output is readable plain text with no escape sequences.
func TestStatusLineDegradesWithoutOSC8(t *testing.T) {
	noHyperlinks(t)
	got := formatStatusLine(webAvailability{Port: 8787, Live: true, Resolved: true},
		seatInfo{ItemID: "sty_abc123", StoryStatus: "plan"}, true)

	if strings.ContainsRune(got, '\x1b') {
		t.Fatalf("no escape sequence may reach a terminal without OSC 8 support: %q", got)
	}
	if !strings.Contains(got, "http://localhost:8787") {
		t.Fatalf("the URL must still be readable as plain text: %q", got)
	}
}

// TestStatusLineHyperlinkWrapsOnlyTheURL (AC4): when links ARE supported the
// OSC 8 framing wraps the URL, not the whole line.
func TestStatusLineHyperlinkWrapsOnlyTheURL(t *testing.T) {
	withHyperlinks(t)
	got := formatStatusLine(webAvailability{Port: 8787, Live: true, Resolved: true},
		seatInfo{ItemID: "sty_abc123", StoryStatus: "plan"}, true)

	if !strings.Contains(got, "\x1b]8;;http://localhost:8787\x1b\\") {
		t.Fatalf("want OSC 8 open around the URL: %q", got)
	}
	if strings.HasPrefix(got, "\x1b]8;;") {
		t.Fatalf("the whole line must not be the link: %q", got)
	}
	if i := strings.Index(got, "sty_abc123"); i < 0 || strings.Contains(got[i:], "\x1b]8;;") {
		t.Fatalf("the story segment must not be inside the link: %q", got)
	}
}

// TestHyperlinkDefaultsOffForUnknownTerminal (AC4): Terminal.app and anything
// unrecognised must not get escapes — the default is off, not on.
func TestHyperlinkDefaultsOffForUnknownTerminal(t *testing.T) {
	for _, tc := range []struct {
		termProgram, term string
		want              bool
	}{
		{"Apple_Terminal", "xterm-256color", false},
		{"", "", false},
		{"iTerm.app", "xterm-256color", true},
		{"WezTerm", "", true},
		{"", "xterm-kitty", true},
	} {
		t.Setenv("TERM_PROGRAM", tc.termProgram)
		t.Setenv("TERM", tc.term)
		if got := defaultHyperlinksEnabled(); got != tc.want {
			t.Errorf("TERM_PROGRAM=%q TERM=%q → %v, want %v", tc.termProgram, tc.term, got, tc.want)
		}
	}
}

// TestStatusLineRenderIsReadOnly (AC1): rendering must not mutate lease state —
// in particular it must never trigger the stale-lease reaping `satelle story
// seat` performs. The lease rows are compared byte-for-byte across a render.
func TestStatusLineRenderIsReadOnly(t *testing.T) {
	noHyperlinks(t)
	stubHealthz(t, false)
	_, storyID := liveSeatRepo(t)

	snapshot := func() string {
		db, err := store.Open(runtimeDBPath(t))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = db.Close() }()
		rows, err := db.Leases.List(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		b, err := json.Marshal(rows)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}

	before := snapshot()
	if !strings.Contains(before, storyID) {
		t.Fatalf("fixture should hold a live seat for %s: %s", storyID, before)
	}

	line := renderStatusLine()
	if !strings.Contains(line, storyID+"::in_progress") {
		t.Fatalf("renderer must report the engaged story: %q", line)
	}

	if after := snapshot(); after != before {
		t.Fatalf("render mutated lease state:\n before=%s\n after =%s", before, after)
	}
}

// TestStatusLineReapsNothingEvenWhenStale (AC1): the sharpest form of the same
// rule — a STALE lease is exactly what `satelle story seat` would delete. The
// renderer must leave it, and say no story is engaged.
func TestStatusLineReapsNothingEvenWhenStale(t *testing.T) {
	noHyperlinks(t)
	stubHealthz(t, false)
	_, storyID := liveSeatRepo(t)
	ageLease(t, storyID, 72*time.Hour)

	countRows := func() int {
		db, err := store.Open(runtimeDBPath(t))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = db.Close() }()
		rows, err := db.Leases.List(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		return len(rows)
	}

	before := countRows()
	if before != 1 {
		t.Fatalf("fixture should hold one lease, got %d", before)
	}
	line := renderStatusLine()
	if got := countRows(); got != before {
		t.Fatalf("renderer reaped a stale lease (%d → %d rows) — it must never write", before, got)
	}
	if strings.Contains(line, storyID+"::") {
		t.Fatalf("a stale seat is not engaged work: %q", line)
	}
}

// TestStatusLineNeverFailsLoudly (AC5): outside any satelle repo — no config, no
// database — the renderer still emits one well-formed line and exits zero.
func TestStatusLineNeverFailsLoudly(t *testing.T) {
	noHyperlinks(t)
	stubHealthz(t, false)
	testutil.IsolateHome(t)
	t.Setenv("SATELLE_CONFIG", "")
	t.Chdir(t.TempDir())

	line := safeRenderStatusLine()
	if strings.TrimSpace(line) == "" {
		t.Fatal("must emit a line, never nothing")
	}
	if strings.Count(line, "\n") != 0 {
		t.Fatalf("must stay one line: %q", line)
	}
	for _, bad := range []string{"panic", "goroutine", ".go:", "Error:"} {
		if strings.Contains(line, bad) {
			t.Fatalf("statusline leaked diagnostics (%q): %q", bad, line)
		}
	}
}

// TestRunStatusLineExitsZeroAndWritesOneLine (AC5): the command path itself
// returns nil and writes exactly one complete line.
func TestRunStatusLineExitsZeroAndWritesOneLine(t *testing.T) {
	noHyperlinks(t)
	stubHealthz(t, false)
	testutil.IsolateHome(t)
	t.Setenv("SATELLE_CONFIG", "")
	t.Chdir(t.TempDir())

	f, err := os.CreateTemp(t.TempDir(), "line")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if err := runStatusLine(f); err != nil {
		t.Fatalf("statusline must never return an error, got %v", err)
	}
	b, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	out := string(b)
	if !strings.HasSuffix(out, "\n") || strings.Count(out, "\n") != 1 {
		t.Fatalf("want exactly one terminated line, got %q", out)
	}
}

// TestStatusLineDegradedIsWellFormed (AC5): the last-resort line is a single
// honest statement, not an empty string.
func TestStatusLineDegradedIsWellFormed(t *testing.T) {
	if strings.TrimSpace(statusLineDegraded) == "" || strings.Contains(statusLineDegraded, "\n") {
		t.Fatalf("degraded line must be one non-empty line: %q", statusLineDegraded)
	}
}

// --- install / remove -------------------------------------------------------

func readSettings(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatalf("settings.json is not JSON: %v\n%s", err, raw)
	}
	return root
}

func claudeSettingsPath(repo string) string {
	return filepath.Join(repo, ".claude", "settings.json")
}

// TestInstallWritesStatusLineOnFreshScaffold (AC6, AC11): a repo with no
// settings file gets satelle's statusLine alongside the hooks.
func TestInstallWritesStatusLineOnFreshScaffold(t *testing.T) {
	repo := t.TempDir()
	created, _, _, err := ensureClaudeHooks(repo)
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("want a created scaffold")
	}
	root := readSettings(t, claudeSettingsPath(repo))
	if !isSatelleOwnedStatusLine(root["statusLine"]) {
		t.Fatalf("fresh scaffold must carry satelle's statusLine, got %v", root["statusLine"])
	}
	sl := root["statusLine"].(map[string]any)
	if sl["type"] != "command" || sl["command"] != statusLineCommand {
		t.Fatalf("statusLine entry = %v", sl)
	}
}

// TestInstallStatusLineIsIdempotent (AC6): re-running install leaves a
// satelle-owned statusLine byte-identical.
func TestInstallStatusLineIsIdempotent(t *testing.T) {
	repo := t.TempDir()
	if _, _, _, err := ensureClaudeHooks(repo); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(claudeSettingsPath(repo))
	if err != nil {
		t.Fatal(err)
	}
	if _, updated, _, err := ensureClaudeHooks(repo); err != nil {
		t.Fatal(err)
	} else {
		for _, u := range updated {
			if strings.Contains(u, "statusLine") {
				t.Fatalf("second install must not touch statusLine, reported %q", u)
			}
		}
	}
	second, err := os.ReadFile(claudeSettingsPath(repo))
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatalf("re-install changed the file:\n%s\n---\n%s", first, second)
	}
}

// TestInstallAddsStatusLineToExistingSettings (AC6): repos that already have a
// settings.json (hooks but no statusLine) gain one on install — fresh creation
// alone would leave every existing repo without it.
func TestInstallAddsStatusLineToExistingSettings(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	existing := `{
  "hooks": {},
  "model": "opus"
}
`
	if err := os.WriteFile(claudeSettingsPath(repo), []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}
	_, updated, _, err := ensureClaudeHooks(repo)
	if err != nil {
		t.Fatal(err)
	}
	root := readSettings(t, claudeSettingsPath(repo))
	if !isSatelleOwnedStatusLine(root["statusLine"]) {
		t.Fatalf("existing settings must gain satelle's statusLine, got %v", root["statusLine"])
	}
	if root["model"] != "opus" {
		t.Fatalf("unrelated top-level keys must survive, got %v", root["model"])
	}
	var said bool
	for _, u := range updated {
		if strings.Contains(u, "statusLine") {
			said = true
		}
	}
	if !said {
		t.Fatalf("install should report the statusLine addition, got %v", updated)
	}
}

// TestInstallLeavesForeignStatusLineByteForByte (AC7): a statusLine the operator
// owns is untouched, install still succeeds, and the paste snippet is offered.
func TestInstallLeavesForeignStatusLineByteForByte(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	foreign := map[string]any{"type": "command", "command": "~/bin/my-statusline.sh --fancy"}
	doc := map[string]any{"hooks": map[string]any{}, "statusLine": foreign}
	b, _ := json.MarshalIndent(doc, "", "  ")
	if err := os.WriteFile(claudeSettingsPath(repo), append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, _, _, err := ensureClaudeHooks(repo); err != nil {
		t.Fatalf("install must still succeed with a foreign statusLine: %v", err)
	}

	root := readSettings(t, claudeSettingsPath(repo))
	got, _ := json.Marshal(root["statusLine"])
	want, _ := json.Marshal(foreign)
	if string(got) != string(want) {
		t.Fatalf("foreign statusLine was modified:\n got=%s\nwant=%s", got, want)
	}

	notice := foreignStatusLineNotice(claudeSettingsPath(repo))
	if notice == "" {
		t.Fatal("operator must be told the statusLine was skipped")
	}
	if !strings.Contains(notice, statusLineCommand) {
		t.Fatalf("notice must carry the exact snippet to paste: %q", notice)
	}
}

// TestNoForeignNoticeWhenSatelleOwned (AC7): the skip notice is only for a
// genuinely foreign entry — satelle's own must not trigger it.
func TestNoForeignNoticeWhenSatelleOwned(t *testing.T) {
	repo := t.TempDir()
	if _, _, _, err := ensureClaudeHooks(repo); err != nil {
		t.Fatal(err)
	}
	if notice := foreignStatusLineNotice(claudeSettingsPath(repo)); notice != "" {
		t.Fatalf("satelle's own statusLine must not read as foreign: %q", notice)
	}
}

// TestRemovePrunesOnlySatelleStatusLine (AC8): remove drops satelle's entry,
// leaves the file and every user key, and is idempotent. The file carries a user
// key so it survives as JSON — a WHOLLY satelle-owned settings.json is emptied
// by the pre-existing prune path, which this story does not change.
func TestRemovePrunesOnlySatelleStatusLine(t *testing.T) {
	repo := t.TempDir()
	if _, _, _, err := ensureClaudeHooks(repo); err != nil {
		t.Fatal(err)
	}
	path := claudeSettingsPath(repo)
	root := readSettings(t, path)
	if !isSatelleOwnedStatusLine(root["statusLine"]) {
		t.Fatal("fixture should start with satelle's statusLine")
	}
	root["model"] = "opus"
	b, _ := json.MarshalIndent(root, "", "  ")
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}

	action, gotPath, _, err := removeClaudeHooks(repo)
	if err != nil {
		t.Fatal(err)
	}
	if action != "updated" {
		t.Fatalf("remove action = %q, want updated", action)
	}
	if _, err := os.Stat(gotPath); err != nil {
		t.Fatalf("remove must never delete the settings file: %v", err)
	}
	after := readSettings(t, path)
	if _, present := after["statusLine"]; present {
		t.Fatalf("satelle statusLine should be gone, got %v", after["statusLine"])
	}
	if after["model"] != "opus" {
		t.Fatalf("user keys must survive, got %v", after["model"])
	}

	// Idempotent: a second remove must not error or delete the file.
	if _, _, _, err := removeClaudeHooks(repo); err != nil {
		t.Fatalf("second remove: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("second remove deleted the file: %v", err)
	}
}

// TestRemoveOfWhollySatelleScaffoldKeepsFile (AC8): the settings file is never
// deleted, even when nothing but satelle content was in it.
func TestRemoveOfWhollySatelleScaffoldKeepsFile(t *testing.T) {
	repo := t.TempDir()
	if _, _, _, err := ensureClaudeHooks(repo); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := removeClaudeHooks(repo); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(claudeSettingsPath(repo)); err != nil {
		t.Fatalf("settings file must survive remove: %v", err)
	}
	raw, err := os.ReadFile(claudeSettingsPath(repo))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), statusLineCommand) {
		t.Fatalf("satelle statusLine survived remove:\n%s", raw)
	}
}

// TestRemoveLeavesForeignStatusLineIntact (AC8): a foreign statusLine and every
// other top-level key survive a remove.
func TestRemoveLeavesForeignStatusLineIntact(t *testing.T) {
	repo := t.TempDir()
	if _, _, _, err := ensureClaudeHooks(repo); err != nil {
		t.Fatal(err)
	}
	// Replace satelle's statusLine with a foreign one, and add a user key.
	path := claudeSettingsPath(repo)
	root := readSettings(t, path)
	foreign := map[string]any{"type": "command", "command": "~/bin/mine.sh"}
	root["statusLine"] = foreign
	root["model"] = "opus"
	b, _ := json.MarshalIndent(root, "", "  ")
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, _, _, err := removeClaudeHooks(repo); err != nil {
		t.Fatal(err)
	}
	after := readSettings(t, path)
	got, _ := json.Marshal(after["statusLine"])
	want, _ := json.Marshal(foreign)
	if string(got) != string(want) {
		t.Fatalf("foreign statusLine must survive remove:\n got=%s\nwant=%s", got, want)
	}
	if after["model"] != "opus" {
		t.Fatalf("other top-level keys must survive, got %v", after["model"])
	}
}

// TestStatusLineAloneIsNotScaffoldDrift (AC9): the presence or absence of
// statusLine must never register as drift — drift is a PreToolUse concern.
func TestStatusLineAloneIsNotScaffoldDrift(t *testing.T) {
	repo := t.TempDir()
	if _, _, _, err := ensureClaudeHooks(repo); err != nil {
		t.Fatal(err)
	}
	withLine := DetectScaffoldDrift(repo)

	path := claudeSettingsPath(repo)
	root := readSettings(t, path)
	delete(root, "statusLine")
	b, _ := json.MarshalIndent(root, "", "  ")
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	withoutLine := DetectScaffoldDrift(repo)

	if len(withLine) != len(withoutLine) {
		t.Fatalf("statusLine changed drift findings: %d with, %d without", len(withLine), len(withoutLine))
	}
	if len(withLine) != 0 {
		t.Fatalf("a freshly installed scaffold must be drift-clean, got %v", withLine)
	}
}

// TestGrokAndCodexGetNoStatusline (AC10): neither harness's scaffold carries a
// statusline key — they cannot accept one, so writing it would be a lie.
func TestGrokAndCodexGetNoStatusline(t *testing.T) {
	repo := t.TempDir()
	if _, _, _, err := ensureGrokHooks(repo); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := ensureCodexHooks(repo); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{grokHooksRel, codexHooksRel} {
		raw, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"statusLine", "status_line"} {
			if strings.Contains(string(raw), key) {
				t.Fatalf("%s must not carry %q:\n%s", rel, key, raw)
			}
		}
	}
}
