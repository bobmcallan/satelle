package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/config"
)

// sty_9e88b82f — Antigravity (agy) hook scaffold, payload handling, wrapper.
// The payload fixtures are documented in testdata/antigravity/README.md.

// agyFixtureDir is absolute: the repo fixtures below t.Chdir away from the
// package directory before a fixture is read.
var agyFixtureDir, _ = filepath.Abs(filepath.Join("testdata", "antigravity"))

func agyFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(agyFixtureDir, name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func withHarnessFlag(t *testing.T, harness string) {
	t.Helper()
	prev := hookHarnessFlag
	hookHarnessFlag = harness
	t.Cleanup(func() { hookHarnessFlag = prev })
}

func agyHooksPath(repo string) string {
	return filepath.Join(repo, filepath.FromSlash(antigravityHooksRel))
}

func readAgyHooks(t *testing.T, repo string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(agyHooksPath(repo))
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatalf("hooks.json is not JSON: %v\n%s", err, raw)
	}
	return root
}

// AC1: the one hook-surface table carries agy's matchers and events.
func TestHarnessHooksAntigravity(t *testing.T) {
	hs := harnessHooks("antigravity")
	if hs.gateMatcher != "write_to_file|replace_file_content" {
		t.Errorf("gateMatcher = %q", hs.gateMatcher)
	}
	if hs.commitMatcher != "run_command" {
		t.Errorf("commitMatcher = %q", hs.commitMatcher)
	}
	if got := strings.Join(hs.events, ","); got != "PreInvocation,PreToolUse,Stop" {
		t.Errorf("events = %q", got)
	}
	if hs.hasEvent("SessionStart") || hs.hasEvent("UserPromptSubmit") {
		t.Errorf("agy has neither SessionStart nor UserPromptSubmit: %v", hs.events)
	}
}

// AC2: the scaffold's event→command mapping, shape and absence of async.
func TestBuildAntigravityHookSettings(t *testing.T) {
	repo := t.TempDir()
	raw := buildAntigravityHookSettings(repo)
	if strings.Contains(string(raw), "async") {
		t.Errorf("agy handlers are synchronous; no async flag expected:\n%s", raw)
	}
	var root map[string]map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatal(err)
	}
	spec := root["satelle"]
	if spec == nil {
		t.Fatalf("no satelle named hook:\n%s", raw)
	}

	pre, _ := spec["PreToolUse"].([]any)
	if len(pre) != 2 {
		t.Fatalf("PreToolUse groups = %d, want 2", len(pre))
	}
	want := []struct{ matcher, sub string }{
		{"write_to_file|replace_file_content", "gate"},
		{"run_command", "commitgate"},
	}
	for i, w := range want {
		g := pre[i].(map[string]any)
		if g["matcher"] != w.matcher {
			t.Errorf("group %d matcher = %v, want %s", i, g["matcher"], w.matcher)
		}
		h := g["hooks"].([]any)[0].(map[string]any)
		cmd, _ := h["command"].(string)
		if h["type"] != "command" || cmd != renderHookCommand(repo, "antigravity", w.sub) {
			t.Errorf("group %d handler = %v", i, h)
		}
		// agy runs handlers from .agents/, so the wrapper path must be absolute.
		if !strings.HasPrefix(cmd, "sh "+filepath.ToSlash(repo)) || !strings.HasSuffix(cmd, "satelle-hook.sh "+w.sub+" antigravity") {
			t.Errorf("group %d command not the absolute wrapper form: %s", i, cmd)
		}
	}

	// PreInvocation and Stop are FLAT handler lists in agy, not matcher groups.
	for event, wantCmd := range map[string]string{
		"PreInvocation": "satelle hook context --harness antigravity",
		"Stop":          "satelle hook stopcheck --harness antigravity",
	} {
		list, _ := spec[event].([]any)
		if len(list) != 1 {
			t.Fatalf("%s handlers = %d, want 1", event, len(list))
		}
		h := list[0].(map[string]any)
		if _, grouped := h["hooks"]; grouped || h["command"] != wantCmd || h["type"] != "command" {
			t.Errorf("%s handler = %v, want flat command %q", event, h, wantCmd)
		}
	}
	stop := spec["Stop"].([]any)[0].(map[string]any)
	if timeout, _ := stop["timeout"].(float64); timeout < stopHookTimeoutSec {
		t.Errorf("Stop timeout = %v; must outlast the stopcheck gate wait (%d)", stop["timeout"], stopHookTimeoutSec)
	}
}

// AC2: create, idempotent re-run, heal of a deleted entry, user hooks preserved.
func TestEnsureAntigravityHooksCreateHealIdempotent(t *testing.T) {
	repo := t.TempDir()
	created, updated, incomplete, err := ensureAntigravityHooks(repo)
	if err != nil || !created || len(updated) != 0 || len(incomplete) != 0 {
		t.Fatalf("create: created=%v updated=%v incomplete=%v err=%v", created, updated, incomplete, err)
	}
	if _, err := os.Stat(filepath.Join(repo, filepath.FromSlash(satelleHookScriptRel))); err != nil {
		t.Fatalf("the shared wrapper script must be written: %v", err)
	}
	first, _ := os.ReadFile(agyHooksPath(repo))
	if string(first) != string(buildAntigravityHookSettings(repo)) {
		t.Fatalf("created file differs from the builder output:\n%s", first)
	}

	created, updated, incomplete, err = ensureAntigravityHooks(repo)
	if err != nil || created || len(updated) != 0 || len(incomplete) != 0 {
		t.Fatalf("re-run: created=%v updated=%v incomplete=%v err=%v", created, updated, incomplete, err)
	}
	if again, _ := os.ReadFile(agyHooksPath(repo)); string(again) != string(first) {
		t.Fatalf("re-run is not byte-identical:\n%s", again)
	}

	// Delete two events and add a user hook: heal restores ours, leaves theirs.
	root := readAgyHooks(t, repo)
	spec := root["satelle"].(map[string]any)
	delete(spec, "PreInvocation")
	delete(spec, "Stop")
	root["mine"] = map[string]any{"PostToolUse": []any{
		map[string]any{"matcher": "run_command", "hooks": []any{map[string]any{"command": "./lint.sh"}}},
	}}
	b, _ := json.MarshalIndent(root, "", "  ")
	if err := os.WriteFile(agyHooksPath(repo), b, 0o644); err != nil {
		t.Fatal(err)
	}
	created, updated, incomplete, err = ensureAntigravityHooks(repo)
	if err != nil || created || len(incomplete) != 0 {
		t.Fatalf("heal: created=%v incomplete=%v err=%v", created, incomplete, err)
	}
	if got := strings.Join(updated, "; "); got != "added PreInvocation hook; added Stop hook" {
		t.Fatalf("heal updates = %q", got)
	}
	healed := readAgyHooks(t, repo)
	if _, ok := healed["mine"]; !ok {
		t.Fatal("the user's named hook was lost on heal")
	}
	if got := healed["satelle"].(map[string]any); got["PreInvocation"] == nil || got["Stop"] == nil {
		t.Fatalf("heal did not restore the events: %v", got)
	}
	if _, again, incomplete, err := ensureAntigravityHooks(repo); err != nil || len(again) != 0 || len(incomplete) != 0 {
		t.Fatalf("second heal must change nothing: updated=%v incomplete=%v err=%v", again, incomplete, err)
	}
}

// Heal also upgrades a stale relative wrapper path and a too-short Stop timeout,
// and never touches a file it cannot parse.
func TestEnsureAntigravityHooksHealsStaleAndSkipsUnparseable(t *testing.T) {
	repo := t.TempDir()
	if _, _, _, err := ensureAntigravityHooks(repo); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(agyHooksPath(repo))
	abs := renderHookCommand(repo, "antigravity", "gate")
	stale := strings.Replace(string(raw), abs, renderHookCommand("", "antigravity", "gate"), 1)
	stale = strings.Replace(stale, `"timeout": 1800`, `"timeout": 30`, 1)
	if stale == string(raw) {
		t.Fatal("test setup did not stale the file")
	}
	if err := os.WriteFile(agyHooksPath(repo), []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}
	_, updated, incomplete, err := ensureAntigravityHooks(repo)
	if err != nil || len(incomplete) != 0 {
		t.Fatalf("heal: incomplete=%v err=%v", incomplete, err)
	}
	if got := strings.Join(updated, "; "); !strings.Contains(got, "absolute script path") || !strings.Contains(got, "raised Stop timeout") {
		t.Fatalf("stale heal updates = %q", got)
	}
	if fixed, _ := os.ReadFile(agyHooksPath(repo)); string(fixed) != string(raw) {
		t.Fatalf("heal did not converge on the builder output:\n%s", fixed)
	}

	garbage := []byte("{ not json")
	if err := os.WriteFile(agyHooksPath(repo), garbage, 0o644); err != nil {
		t.Fatal(err)
	}
	created, updated, incomplete, err := ensureAntigravityHooks(repo)
	if err != nil || created || len(updated) != 0 || strings.Join(incomplete, ",") != "(unparseable)" {
		t.Fatalf("unparseable: created=%v updated=%v incomplete=%v err=%v", created, updated, incomplete, err)
	}
	if b, _ := os.ReadFile(agyHooksPath(repo)); !bytes.Equal(b, garbage) {
		t.Fatalf("an unparseable file must be left untouched, got %q", b)
	}
}

// AC3: removal strips only satelle's entries and deletes the file (and an empty
// .agents/) only when nothing else remains.
func TestRemoveAntigravityHooks(t *testing.T) {
	t.Run("wholly satelle: file and empty dir go", func(t *testing.T) {
		repo := t.TempDir()
		if _, _, _, err := ensureAntigravityHooks(repo); err != nil {
			t.Fatal(err)
		}
		action, path, _, err := removeAntigravityHooks(repo)
		if err != nil || action != "removed" {
			t.Fatalf("action=%q err=%v", action, err)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("hooks.json still present: %v", err)
		}
		if _, err := os.Stat(filepath.Join(repo, ".agents")); !os.IsNotExist(err) {
			t.Fatalf("empty .agents/ should go too: %v", err)
		}
	})

	t.Run("user hook kept, satelle stripped, dir kept", func(t *testing.T) {
		repo := t.TempDir()
		if _, _, _, err := ensureAntigravityHooks(repo); err != nil {
			t.Fatal(err)
		}
		root := readAgyHooks(t, repo)
		root["mine"] = map[string]any{"Stop": []any{map[string]any{"command": "./notify.sh"}}}
		// A user handler INSIDE the satelle-named hook must survive too.
		spec := root["satelle"].(map[string]any)
		spec["PreInvocation"] = append(spec["PreInvocation"].([]any), map[string]any{"command": "./user-context.sh"})
		b, _ := json.MarshalIndent(root, "", "  ")
		if err := os.WriteFile(agyHooksPath(repo), b, 0o644); err != nil {
			t.Fatal(err)
		}
		other := filepath.Join(repo, ".agents", "skills.md")
		if err := os.WriteFile(other, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}

		action, _, _, err := removeAntigravityHooks(repo)
		if err != nil || action != "updated" {
			t.Fatalf("action=%q err=%v", action, err)
		}
		left, _ := os.ReadFile(agyHooksPath(repo))
		for _, gone := range []string{"satelle-hook.sh", "satelle hook "} {
			if strings.Contains(string(left), gone) {
				t.Errorf("satelle entry %q survived removal:\n%s", gone, left)
			}
		}
		for _, kept := range []string{"./notify.sh", "./user-context.sh"} {
			if !strings.Contains(string(left), kept) {
				t.Errorf("user hook %q was removed:\n%s", kept, left)
			}
		}
		if _, err := os.Stat(other); err != nil {
			t.Errorf("a sibling file in .agents/ must never be touched: %v", err)
		}
	})

	t.Run("absent and foreign", func(t *testing.T) {
		repo := t.TempDir()
		if action, _, _, err := removeAntigravityHooks(repo); err != nil || action != "absent" {
			t.Fatalf("absent: action=%q err=%v", action, err)
		}
		if err := os.MkdirAll(filepath.Join(repo, ".agents"), 0o755); err != nil {
			t.Fatal(err)
		}
		foreign := `{"mine":{"Stop":[{"command":"./notify.sh"}]}}`
		if err := os.WriteFile(agyHooksPath(repo), []byte(foreign), 0o644); err != nil {
			t.Fatal(err)
		}
		if action, _, _, err := removeAntigravityHooks(repo); err != nil || action != "skipped" {
			t.Fatalf("foreign: action=%q err=%v", action, err)
		}
		if b, _ := os.ReadFile(agyHooksPath(repo)); string(b) != foreign {
			t.Fatalf("a file with no satelle entry must be byte-untouched, got %s", b)
		}
	})
}

// The shared wrapper script is removed only when no scaffold, agy's included,
// still references it.
func TestMaybeRemoveSharedHookScriptKeepsAntigravityReference(t *testing.T) {
	repo := t.TempDir()
	if _, _, _, err := ensureAntigravityHooks(repo); err != nil {
		t.Fatal(err)
	}
	if action, _, _, err := maybeRemoveSharedHookScript(repo); err != nil || action != "skipped" {
		t.Fatalf("shared script must stay while agy references it: action=%q err=%v", action, err)
	}
	if _, _, _, err := removeAntigravityHooks(repo); err != nil {
		t.Fatal(err)
	}
	if action, _, _, err := maybeRemoveSharedHookScript(repo); err != nil || action != "removed" {
		t.Fatalf("shared script should go once nothing references it: action=%q err=%v", action, err)
	}
}

// AC4: the PreToolUse deny is top-level decision/reason for agy, and the
// wrapper carries the identical infra deny and recognises it.
func TestAntigravityDenyEnvelopeAndWrapperBody(t *testing.T) {
	var buf bytes.Buffer
	if err := emitPreToolUseDeny(&buf, "antigravity", "no story engaged"); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(buf.String()); got != `{"decision":"deny","reason":"no story engaged"}` {
		t.Fatalf("agy deny = %s", got)
	}
	if strings.Contains(buf.String(), "hookSpecificOutput") {
		t.Fatal("agy must not get Claude's envelope")
	}

	infra := infraDenyJSON("antigravity")
	var doc map[string]string
	if err := json.Unmarshal([]byte(infra), &doc); err != nil || doc["decision"] != "deny" || !strings.Contains(doc["reason"], "INFRASTRUCTURE failure") {
		t.Fatalf("agy infra deny = %s (%v)", infra, err)
	}
	body := parameterizedHookScriptBody()
	if !strings.Contains(body, "antigravity) infra='"+infra+"'") {
		t.Errorf("wrapper has no antigravity infra arm carrying %s", infra)
	}
	if !strings.Contains(body, `grok|antigravity) case "$1" in *'"decision"'*'"deny"'*`) {
		t.Errorf("structured_deny() does not recognise agy's top-level deny")
	}
}

// AC4/AC5 end to end: the real `hook gate|commitgate --harness antigravity`
// handlers deny with agy's envelope when no seat is live, and allow with one.
func TestAntigravityHookGateEndToEnd(t *testing.T) {
	repo, id := stopcheckRepo(t, seatMine) // seat stamped with the session the hooks resolve
	target := filepath.Join(repo, "internal", "foo.go")
	writeEvent := func(tool string) string {
		b, _ := json.Marshal(map[string]any{"conversationId": "agy-conv-1", "toolCall": map[string]any{
			"name": tool, "args": map[string]string{"TargetFile": target}}})
		return string(b)
	}

	for _, tool := range []string{"write_to_file", "replace_file_content"} {
		out, err := runRootIn(t, writeEvent(tool), "hook", "gate", "--harness", "antigravity")
		if err != nil {
			t.Fatalf("%s with a live seat must be allowed: %v\n%s", tool, err, out)
		}
	}

	forceRelease(t, id)
	out, err := runRootIn(t, writeEvent("write_to_file"), "hook", "gate", "--harness", "antigravity")
	if err == nil {
		t.Fatalf("with no live seat the edit must be denied:\n%s", out)
	}
	var deny struct{ Decision, Reason string }
	line := strings.TrimSpace(strings.SplitN(out, "\n", 2)[0])
	if jerr := json.Unmarshal([]byte(line), &deny); jerr != nil || deny.Decision != "deny" || strings.TrimSpace(deny.Reason) == "" {
		t.Fatalf("not agy's structured deny: %q (%v)", out, jerr)
	}

	// An unengaged commit is refused through commitgate, for both the plain and
	// the transcript-encoded CommandLine.
	for _, f := range []string{"pretooluse_run_command.json", "pretooluse_run_command_encoded.json"} {
		out, err = runRootIn(t, string(agyFixture(t, f)), "hook", "commitgate", "--harness", "antigravity")
		if err == nil || !strings.Contains(out, `"decision":"deny"`) {
			t.Fatalf("%s: a commit/push with no seat must be denied with agy's envelope: err=%v\n%s", f, err, out)
		}
	}
}

// With no SATELLE_SESSION override the hook binds agy's conversationId as the
// session identity, published for a later Acquire (as session_id is for Claude).
func TestAntigravityConversationIDBindsSession(t *testing.T) {
	t.Setenv("SATELLE_HOME", t.TempDir())
	t.Setenv(config.SessionEnv, "")
	_ = os.Unsetenv(config.SessionEnv)
	const conv = "ec33ebf9-0cba-4100-8142-c61503f6c587"
	if got := bindSessionID(agyFixture(t, "pretooluse_write_to_file.json")); got != conv {
		t.Fatalf("bindSessionID = %q, want the conversationId %q", got, conv)
	}
	if got := config.ResolveSession(); got != conv {
		t.Fatalf("the conversationId must be published for Acquire, got %q", got)
	}
}

// AC6: payload extraction from toolCall.args and conversationId.
func TestAntigravityPayloadExtraction(t *testing.T) {
	for _, tc := range []struct{ fixture, wantPath, wantCmd string }{
		{"pretooluse_write_to_file.json", "/home/u/repo/internal/x/main.go", ""},
		{"pretooluse_write_to_file_encoded.json", "/home/u/repo/internal/x/main.go", ""},
		{"pretooluse_replace_file_content.json", "/home/u/repo/internal/x/main.go", ""},
		{"pretooluse_run_command.json", "", "git commit -m x"},
		{"pretooluse_run_command_encoded.json", "", "git push origin main"},
	} {
		raw := agyFixture(t, tc.fixture)
		if got := filePathFromEvent(raw); got != tc.wantPath {
			t.Errorf("%s: filePathFromEvent = %q, want %q", tc.fixture, got, tc.wantPath)
		}
		if got := bashCommandFromEvent(raw); got != tc.wantCmd {
			t.Errorf("%s: bashCommandFromEvent = %q, want %q", tc.fixture, got, tc.wantCmd)
		}
	}

	const conv = "ec33ebf9-0cba-4100-8142-c61503f6c587"
	for _, f := range []string{"pretooluse_write_to_file.json", "pretooluse_run_command.json", "preinvocation.json", "stop.json"} {
		if got := sessionIDFromHook(agyFixture(t, f)); got != conv {
			t.Errorf("%s: sessionIDFromHook = %q, want %q", f, got, conv)
		}
	}

	// The existing harnesses' keys win, and a non-string arg is not a path.
	mixed := []byte(`{"session_id":"s","tool_input":{"file_path":"a.go","command":"ls"},"toolCall":{"args":{"TargetFile":"b.go","CommandLine":"pwd"}}}`)
	if filePathFromEvent(mixed) != "a.go" || bashCommandFromEvent(mixed) != "ls" || sessionIDFromHook([]byte(`{"session_id":"s","conversationId":"c"}`)) != "s" {
		t.Error("claude/grok keys must take precedence over agy's")
	}
	if got := filePathFromEvent([]byte(`{"toolCall":{"args":{"TargetFile":true}}}`)); got != "" {
		t.Errorf("a non-string TargetFile resolved to %q", got)
	}
}

// PreInvocation context is agy's injectSteps/ephemeralMessage, not Claude's
// hookSpecificOutput, and rides the unknown-harness budget (no agy limit yet).
func TestAntigravityContextInjection(t *testing.T) {
	hookRepo(t)
	var out, errb bytes.Buffer
	if err := runHookContext(&out, &errb, "antigravity"); err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("context is not JSON: %v\n%s", err, out.String())
	}
	if _, claude := doc["hookSpecificOutput"]; claude || len(doc) != 1 {
		t.Fatalf("agy context must carry only injectSteps: %v", keysOf(doc))
	}
	steps, _ := doc["injectSteps"].([]any)
	if len(steps) != 1 {
		t.Fatalf("injectSteps = %v", doc["injectSteps"])
	}
	step := steps[0].(map[string]any)
	if msg, _ := step["ephemeralMessage"].(string); strings.TrimSpace(msg) == "" || len(step) != 1 {
		t.Fatalf("ephemeralMessage missing or extra keys: %v", step)
	}
	// The flag alone selects the shape; the payload is not consulted.
	if got := resolveContextHarness("antigravity", agyFixture(t, "preinvocation.json"), nil); got != "antigravity" {
		t.Errorf("resolveContextHarness = %q", got)
	}
}

// Stop blocks on agy ONLY with decision "continue"; "block" would let the stop
// through silently, so every block path must use the agy value.
func TestAntigravityStopBlockDecision(t *testing.T) {
	for harness, want := range map[string]string{"antigravity": "continue", "claude": "block", "grok": "block", "codex": "block", "unknown": "block"} {
		var buf bytes.Buffer
		if err := emitStopBlock(&buf, harness, "why"); err != nil {
			t.Fatal(err)
		}
		var got stopBlockOut
		if err := json.Unmarshal(buf.Bytes(), &got); err != nil || got.Decision != want || got.Reason != "why" {
			t.Errorf("%s: block = %s (%v), want decision %q", harness, buf.String(), err, want)
		}
	}

	var note bytes.Buffer
	if err := emitStopNote(&note, "antigravity", "held elsewhere"); err != nil || strings.TrimSpace(note.String()) != "{}" {
		t.Errorf("agy allow-with-note must be an empty object, got %q (%v)", note.String(), err)
	}
	note.Reset()
	if err := emitStopNote(&note, "claude", "held elsewhere"); err != nil || !strings.Contains(note.String(), `"systemMessage":"held elsewhere"`) {
		t.Errorf("claude note unchanged, got %q (%v)", note.String(), err)
	}
}

// Every stopcheck block path emits decision=continue for agy: ungated changes,
// a delivered gate verdict, and a gate still running.
func TestAntigravityStopcheckBlockPaths(t *testing.T) {
	stop := func(t *testing.T, harness, event string) (stopBlockOut, string) {
		t.Helper()
		withHarnessFlag(t, harness)
		var out bytes.Buffer
		if err := runHookStopcheck(agyFixture(t, event), &out); err != nil {
			t.Fatal(err)
		}
		var blk stopBlockOut
		_ = json.Unmarshal([]byte(strings.TrimSpace(out.String())), &blk)
		return blk, out.String()
	}

	t.Run("ungated changes", func(t *testing.T) {
		repo, _ := stopcheckRepo(t, seatNone)
		dirtyTree(t, repo)
		blk, out := stop(t, "antigravity", "stop.json")
		if blk.Decision != "continue" || !strings.Contains(blk.Reason, "STOP BLOCKED") {
			t.Fatalf("agy ungated-change block = %q", out)
		}
		if blk, out := stop(t, "claude", "stop.json"); blk.Decision != "block" {
			t.Fatalf("claude ungated-change block must be unchanged: %q", out)
		}
	})

	t.Run("harness-imposed termination is not forced to continue", func(t *testing.T) {
		repo, _ := stopcheckRepo(t, seatNone)
		dirtyTree(t, repo)
		if _, out := stop(t, "antigravity", "stop_max_steps.json"); strings.TrimSpace(out) != "" {
			t.Fatalf("max_steps_exceeded must not be turned into a continue: %q", out)
		}
	})

	t.Run("sibling holds the seat: allow with an empty object", func(t *testing.T) {
		repo, _ := stopcheckRepo(t, seatOther)
		dirtyTree(t, repo)
		if _, out := stop(t, "antigravity", "stop.json"); strings.TrimSpace(out) != "{}" {
			t.Fatalf("agy sibling-note path = %q, want {}", out)
		}
	})

	// The gate-handoff paths (verdict delivery, gate still running) are covered in
	// gatestopwake_test.go, the one place allowed to read a gate handle.
}

// A denied PreToolUse names the harness the scaffold passed, with no payload
// fingerprint needed.
func TestAntigravityDenyPreToolUseUsesFlag(t *testing.T) {
	withHarnessFlag(t, "antigravity")
	cmd := &cobra.Command{}
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := denyPreToolUse(cmd, agyFixture(t, "pretooluse_write_to_file.json"), "r"); err == nil {
		t.Fatal("denyPreToolUse must return the reason as an error")
	}
	if strings.TrimSpace(out.String()) != `{"decision":"deny","reason":"r"}` {
		t.Fatalf("deny = %s", out.String())
	}
}

// agent-agnostic §2: until agy has a capability row, model / driver usage /
// context limit answer with an antigravity-named result or the neutral unknown
// fallback — never Claude's.
func TestAntigravityAdapterNamedFallbacks(t *testing.T) {
	if r := agentcli.ReasonForNoModel("antigravity"); !strings.Contains(r, "antigravity") || strings.Contains(strings.ToLower(r), "claude") {
		t.Errorf("model reason = %q", r)
	}
	snap := agentcli.SessionUsageSnapshot("antigravity", "conv-1", t.TempDir())
	if !strings.Contains(snap.UnavailableReason, "antigravity") || strings.Contains(strings.ToLower(snap.UnavailableReason), "claude") {
		t.Errorf("driver usage = %+v", snap)
	}
	var cfg config.Config
	if got, want := cfg.ContextLimit("antigravity"), cfg.ContextLimit("unknown"); got != want {
		t.Errorf("ContextLimit(antigravity) = %d, want the unknown fallback %d", got, want)
	}
}

// AC8 wiring: --harness accepts antigravity and its agy alias; agents targets
// expand for both and for all.
func TestAntigravityHarnessFlagAndTargets(t *testing.T) {
	got, err := parseHarnessFlag("claude, agy,antigravity")
	if err != nil || strings.Join(got, ",") != "claude,antigravity" {
		t.Fatalf("parseHarnessFlag = %v (%v)", got, err)
	}
	if _, err := parseHarnessFlag("gemini"); err == nil || !strings.Contains(err.Error(), "antigravity") {
		t.Errorf("unknown harness error should list antigravity: %v", err)
	}
	for _, name := range []string{"antigravity", "agy", "AGY"} {
		if targets, err := expandAgentTargets(name); err != nil || strings.Join(targets, ",") != "antigravity" {
			t.Errorf("expandAgentTargets(%q) = %v (%v)", name, targets, err)
		}
	}
	if all, _ := expandAgentTargets("all"); strings.Join(all, ",") != "claude,grok,codex,antigravity" {
		t.Errorf("all = %v", all)
	}

	repo := t.TempDir()
	if detectAntigravityHarness(repo, nil) {
		t.Error("no .agents/hooks.json: nothing to heal")
	}
	if err := os.MkdirAll(filepath.Join(repo, ".agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if detectAntigravityHarness(repo, nil) {
		t.Error(".agents/ alone is a shared directory, not an agy signal")
	}
	if !detectAntigravityHarness(repo, []string{"agy"}) || detectAntigravityHarness(repo, []string{"claude"}) {
		t.Error("an explicit --harness list decides")
	}
	if _, _, _, err := ensureAntigravityHooks(repo); err != nil {
		t.Fatal(err)
	}
	if !detectAntigravityHarness(repo, nil) {
		t.Error("an installed satelle .agents/hooks.json must be healed on re-init")
	}
}

// AC8: `satelle agents install|remove antigravity` end to end.
func TestAgentsInstallRemoveAntigravity(t *testing.T) {
	repo := tempRepo(t)
	t.Chdir(repo)
	t.Setenv("SATELLE_HOME", t.TempDir())
	launcher := filepath.Join(os.Getenv("SATELLE_HOME"), "agents", "bin", "satelle-antigravity")

	out, err := runRootIn(t, "", "agents", "install", "antigravity")
	if err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	for _, want := range []string{"created antigravity launcher", "created antigravity scaffold → " + antigravityHooksRel} {
		if !strings.Contains(out, want) {
			t.Errorf("install output lacks %q:\n%s", want, out)
		}
	}
	if b, _ := os.ReadFile(launcher); !strings.Contains(string(b), "exec agy") {
		t.Errorf("launcher does not exec agy:\n%s", b)
	}
	if _, err := os.Stat(agyHooksPath(repo)); err != nil {
		t.Fatalf("scaffold missing: %v", err)
	}

	out, err = runRootIn(t, "", "agents", "install", "agy")
	if err != nil {
		t.Fatalf("alias install: %v\n%s", err, out)
	}
	for _, want := range []string{"unchanged antigravity launcher", "unchanged antigravity scaffold"} {
		if !strings.Contains(out, want) {
			t.Errorf("second install should be idempotent, lacks %q:\n%s", want, out)
		}
	}

	out, err = runRootIn(t, "", "agents", "remove", "antigravity")
	if err != nil {
		t.Fatalf("remove: %v\n%s", err, out)
	}
	for _, want := range []string{"removed antigravity launcher", "removed antigravity scaffold → "} {
		if !strings.Contains(out, want) {
			t.Errorf("remove output lacks %q:\n%s", want, out)
		}
	}
	for _, gone := range []string{launcher, agyHooksPath(repo)} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Errorf("%s should be gone after remove: %v", gone, err)
		}
	}
}
