package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/agentstep"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/lease"
	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// sty_7d098d50: the cursor scaffold — .cursor/hooks.json installed, merged,
// removed, drift-checked, healed and carried into a worktree.

type cursorHooksFile struct {
	Version int                         `json:"version"`
	Hooks   map[string][]map[string]any `json:"hooks"`
}

func readCursorHooks(t *testing.T, repo string) (cursorHooksFile, string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(cursorHooksRel)))
	if err != nil {
		t.Fatalf("read hooks.json: %v", err)
	}
	var f cursorHooksFile
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatalf("hooks.json is not JSON: %v\n%s", err, raw)
	}
	return f, string(raw)
}

// commandsOf returns the commands under event, in order.
func (f cursorHooksFile) commandsOf(event string) []string {
	var out []string
	for _, e := range f.Hooks[event] {
		c, _ := e["command"].(string)
		out = append(out, c)
	}
	return out
}

// AC1: `agents install cursor` writes the matcher-less satelle entries, with the
// wrapper by absolute path and the PATH-prefixed direct commands; a second install
// leaves the file byte-identical.
func TestAgentsInstallCursorWritesHooksFile(t *testing.T) {
	repo := tempRepo(t)
	t.Chdir(repo)
	out, err := runRootIn(t, "", "agents", "install", "cursor")
	if err != nil {
		t.Fatalf("agents install cursor: %v\n%s", err, out)
	}
	if !strings.Contains(out, "created cursor scaffold") || strings.Contains(out, "launcher") {
		t.Errorf("cursor has a scaffold and no launcher: %s", out)
	}
	f, raw := readCursorHooks(t, repo)
	if f.Version != 1 {
		t.Errorf("version = %d, want 1", f.Version)
	}
	if strings.Contains(raw, "matcher") {
		t.Errorf("entries must be matcher-less:\n%s", raw)
	}
	wrapperRe := regexp.MustCompile(`^sh (/.+)/\.satelle/hooks/satelle-hook\.sh (gate|commitgate) cursor$`)
	pre := f.commandsOf("preToolUse")
	if len(pre) != 2 {
		t.Fatalf("preToolUse = %v, want the gate and the commitgate", pre)
	}
	for i, verb := range []string{"gate", "commitgate"} {
		m := wrapperRe.FindStringSubmatch(pre[i])
		if m == nil || m[2] != verb {
			t.Errorf("preToolUse[%d] = %q, want an absolute wrapper %s command", i, pre[i], verb)
		}
	}
	wrapper := filepath.Join(strings.TrimPrefix(wrapperRe.FindStringSubmatch(pre[0])[1], ""), filepath.FromSlash(satelleHookScriptRel))
	if st, err := os.Stat(wrapper); err != nil || st.Mode()&0o111 == 0 {
		t.Errorf("the wrapper the gate calls must exist and be executable: %v", err)
	}
	if got := f.commandsOf("sessionStart"); len(got) != 1 || got[0] != "PATH=$HOME/.local/bin:$PATH satelle hook context --harness cursor" {
		t.Errorf("sessionStart = %v", got)
	}
	if got := f.commandsOf("stop"); len(got) != 1 || got[0] != "PATH=$HOME/.local/bin:$PATH satelle hook stopcheck --harness cursor" {
		t.Errorf("stop = %v", got)
	}
	if len(f.Hooks) != 3 {
		t.Errorf("events = %v, want preToolUse, sessionStart, stop only (no prompt reminder: beforeSubmitPrompt is unavailable)", f.Hooks)
	}

	first, _ := os.ReadFile(filepath.Join(repo, filepath.FromSlash(cursorHooksRel)))
	out, err = runRootIn(t, "", "agents", "install", "cursor")
	if err != nil || !strings.Contains(out, "unchanged cursor scaffold") {
		t.Fatalf("second install: %v\n%s", err, out)
	}
	second, _ := os.ReadFile(filepath.Join(repo, filepath.FromSlash(cursorHooksRel)))
	if !bytes.Equal(first, second) {
		t.Errorf("a second install changed the file:\n%s\n---\n%s", first, second)
	}
}

// AC2: install keeps the user's version, unknown keys and entries; remove strips
// only satelle's, deletes a satelle-only file, and keeps the wrapper while another
// scaffold references it.
func TestAgentsCursorInstallRemoveKeepsUserConfig(t *testing.T) {
	t.Run("file with a user entry", func(t *testing.T) {
		repo := tempRepo(t)
		t.Chdir(repo)
		dir := filepath.Join(repo, ".cursor")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		user := `{"version":2,"theme":"dark","hooks":{"afterFileEdit":[{"command":"./fmt.sh"}],"stop":[{"command":"/mine/stop.sh"}]}}`
		if err := os.WriteFile(filepath.Join(dir, "hooks.json"), []byte(user), 0o644); err != nil {
			t.Fatal(err)
		}
		cli := `{"permissions":{"allow":["Shell(ls)"]}}`
		if err := os.WriteFile(filepath.Join(dir, "cli.json"), []byte(cli), 0o644); err != nil {
			t.Fatal(err)
		}
		if out, err := runRootIn(t, "", "agents", "install", "cursor"); err != nil {
			t.Fatalf("install: %v\n%s", err, out)
		}
		f, raw := readCursorHooks(t, repo)
		if f.Version != 2 || !strings.Contains(raw, `"theme"`) || !strings.Contains(raw, "./fmt.sh") {
			t.Errorf("install lost the user's version/key/entry:\n%s", raw)
		}
		if got := f.commandsOf("stop"); len(got) != 2 || got[0] != "/mine/stop.sh" {
			t.Errorf("the user's stop entry must stay first, got %v", got)
		}

		out, err := runRootIn(t, "", "agents", "remove", "cursor")
		if err != nil || !strings.Contains(out, "updated cursor scaffold") {
			t.Fatalf("remove: %v\n%s", err, out)
		}
		f, raw = readCursorHooks(t, repo)
		if f.Version != 2 || !strings.Contains(raw, "./fmt.sh") || !strings.Contains(raw, "/mine/stop.sh") || !strings.Contains(raw, `"theme"`) {
			t.Errorf("remove lost the user's content:\n%s", raw)
		}
		if strings.Contains(raw, "satelle") {
			t.Errorf("a satelle entry survived remove:\n%s", raw)
		}
		if got, _ := os.ReadFile(filepath.Join(dir, "cli.json")); string(got) != cli {
			t.Errorf(".cursor/cli.json was touched: %s", got)
		}
		if out, err := runRootIn(t, "", "agents", "remove", "cursor"); err != nil || !strings.Contains(out, "skipped cursor scaffold") {
			t.Errorf("second remove should leave the file alone: %v\n%s", err, out)
		}
	})

	t.Run("satelle-only file", func(t *testing.T) {
		repo := tempRepo(t)
		t.Chdir(repo)
		if out, err := runRootIn(t, "", "agents", "install", "cursor"); err != nil {
			t.Fatalf("install: %v\n%s", err, out)
		}
		out, err := runRootIn(t, "", "agents", "remove", "cursor")
		if err != nil || !strings.Contains(out, "removed cursor scaffold") {
			t.Fatalf("remove: %v\n%s", err, out)
		}
		if _, err := os.Stat(filepath.Join(repo, ".cursor")); !os.IsNotExist(err) {
			t.Errorf(".cursor must be gone entirely: %v", err)
		}
		if _, err := os.Stat(filepath.Join(repo, filepath.FromSlash(satelleHookScriptRel))); !os.IsNotExist(err) {
			t.Errorf("nothing else references the wrapper, so it goes too: %v", err)
		}
		if out, err := runRootIn(t, "", "agents", "remove", "cursor"); err != nil || !strings.Contains(out, "absent cursor scaffold") {
			t.Errorf("removing again is a no-op: %v\n%s", err, out)
		}
	})

	t.Run("wrapper stays while another scaffold references it", func(t *testing.T) {
		repo := tempRepo(t)
		t.Chdir(repo)
		for _, h := range []string{"claude", "cursor"} {
			if out, err := runRootIn(t, "", "agents", "install", h); err != nil {
				t.Fatalf("install %s: %v\n%s", h, err, out)
			}
		}
		if out, err := runRootIn(t, "", "agents", "remove", "cursor"); err != nil {
			t.Fatalf("remove: %v\n%s", err, out)
		}
		if _, err := os.Stat(filepath.Join(repo, filepath.FromSlash(satelleHookScriptRel))); err != nil {
			t.Errorf("claude still references the wrapper: %v", err)
		}
		// And the reverse: cursor alone keeps it when claude goes.
		if out, err := runRootIn(t, "", "agents", "install", "cursor"); err != nil {
			t.Fatal(err, out)
		}
		if out, err := runRootIn(t, "", "agents", "remove", "claude"); err != nil {
			t.Fatalf("remove claude: %v\n%s", err, out)
		}
		if _, err := os.Stat(filepath.Join(repo, filepath.FromSlash(satelleHookScriptRel))); err != nil {
			t.Errorf("cursor still references the wrapper: %v", err)
		}
	})
}

// AC12: `all` includes cursor.
func TestAgentsInstallAllIncludesCursor(t *testing.T) {
	repo := tempRepo(t)
	t.Chdir(repo)
	if out, err := runRootIn(t, "", "agents", "install", "all"); err != nil {
		t.Fatalf("install all: %v\n%s", err, out)
	}
	for _, rel := range []string{cursorHooksRel, ".claude/settings.json", grokHooksRel, piExtensionRel} {
		if _, err := os.Stat(filepath.Join(repo, filepath.FromSlash(rel))); err != nil {
			t.Errorf("`all` did not write %s: %v", rel, err)
		}
	}
}

// AC9: the help names both cursor unavailables, and so does harness.toml.
func TestAgentsHelpRecordsCursorUnavailables(t *testing.T) {
	out, err := runRootIn(t, "", "agents", "--help")
	if err != nil {
		t.Fatal(err)
	}
	flat := strings.Join(strings.Fields(out), " ")
	for _, want := range []string{
		"cursor — stop: unavailable in print mode (no stop event dispatched",
		"cursor — prompt reminder (beforeSubmitPrompt): unavailable — print mode does not dispatch it; interactive context delivery unproven",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("agents --help lacks %q:\n%s", want, out)
		}
	}
	toml, err := os.ReadFile("../config/substrate/config/harness.toml")
	if err != nil {
		t.Fatal(err)
	}
	flatToml := strings.Join(strings.Fields(strings.ReplaceAll(string(toml), "#", " ")), " ")
	for _, want := range []string{
		"stop: unavailable in print mode (no stop event is dispatched",
		"prompt reminder (beforeSubmitPrompt): unavailable — print mode does not dispatch it, and interactive context delivery is unproven",
	} {
		if !strings.Contains(flatToml, want) {
			t.Errorf("harness.toml lacks %q", want)
		}
	}
	for _, f := range agentcli.HarnessFactsTable() {
		if f.Harness == agentcli.HarnessCursor && !strings.Contains(f.PromptContextNote, "beforeSubmitPrompt") && !strings.Contains(f.PromptContext.Cell(), "beforeSubmitPrompt") {
			t.Errorf("the cursor facts row does not name the unavailable prompt reminder: %+v", f.PromptContext)
		}
	}
}

// AC10 (config half): cursor's wiring and context table are embedded.
func TestEmbeddedCursorHarnessTable(t *testing.T) {
	var c config.Config
	if got := c.GateWiring("cursor"); len(got) != 1 || got[0] != ".cursor/hooks.json" {
		t.Errorf("GateWiring(cursor) = %v", got)
	}
	if got := c.SessionContextEvent("cursor"); got != "sessionStart" {
		t.Errorf("SessionContextEvent(cursor) = %q, want cursor's own spelling", got)
	}
	if got := c.ContextLimit("cursor"); got != 9500 {
		t.Errorf("ContextLimit(cursor) = %d, want 9500", got)
	}
}

// staleCursorRepo is a repo with a cursor scaffold written for ANOTHER root, plus
// one user entry.
func staleCursorRepo(t *testing.T) string {
	t.Helper()
	repo := tempRepo(t)
	t.Chdir(repo)
	if err := writeHookScripts(repo); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, ".cursor"), 0o755); err != nil {
		t.Fatal(err)
	}
	stale := `{"version":1,"hooks":{"preToolUse":[{"command":"sh /elsewhere/.satelle/hooks/satelle-hook.sh gate cursor"}],` +
		`"afterFileEdit":[{"command":"./fmt.sh"}]}}`
	if err := os.WriteFile(filepath.Join(repo, filepath.FromSlash(cursorHooksRel)), []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}
	return repo
}

// AC12: the drift check reports a non-canonical or missing satelle entry, and init
// heals it without touching the user's entry.
func TestCursorDriftAndInitHeal(t *testing.T) {
	repo := staleCursorRepo(t)
	findings := DetectScaffoldDrift(repo)
	var kinds []string
	for _, f := range findings {
		if f.Path == cursorHooksRel {
			kinds = append(kinds, f.Kind+": "+f.Detail)
		}
	}
	joined := strings.Join(kinds, "\n")
	for _, want := range []string{
		"command: preToolUse command is not the canonical form",
		"missing: preToolUse entry for \"" + satelleHookScriptRel + " commitgate\" is missing",
		"missing: sessionStart entry",
		"missing: stop entry",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("drift lacks %q; got:\n%s", want, joined)
		}
	}
	if warn := formatScaffoldDriftWarning(findings); !strings.Contains(warn, cursorHooksRel) {
		t.Errorf("the SessionStart drift warning does not name %s:\n%s", cursorHooksRel, warn)
	}

	var out strings.Builder
	if err := runInitTest(t, &out, repo); err != nil {
		t.Fatalf("init: %v", err)
	}
	if !strings.Contains(out.String(), "cursor scaffold") {
		t.Errorf("init did not report the cursor heal:\n%s", out.String())
	}
	for _, f := range DetectScaffoldDrift(repo) {
		if f.Path == cursorHooksRel {
			t.Errorf("init left drift: %+v", f)
		}
	}
	f, raw := readCursorHooks(t, repo)
	if !strings.Contains(raw, "./fmt.sh") {
		t.Errorf("heal lost the user's entry:\n%s", raw)
	}
	if got := f.commandsOf("preToolUse"); len(got) != 2 || !strings.Contains(got[0], repo) {
		t.Errorf("preToolUse = %v, want the stale gate rewritten for this root and a commitgate added", got)
	}
	// Idempotent.
	before, _ := os.ReadFile(filepath.Join(repo, filepath.FromSlash(cursorHooksRel)))
	if err := runInitTest(t, io.Discard, repo); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(repo, filepath.FromSlash(cursorHooksRel)))
	if !bytes.Equal(before, after) {
		t.Error("a second init changed the healed file")
	}
}

// AC12: neither init nor the drift check creates cursor wiring where there is none,
// and a hooks.json with no satelle entry is the user's, not drift.
func TestCursorNotCreatedWhereAbsent(t *testing.T) {
	repo := tempRepo(t)
	t.Chdir(repo)
	if err := runInitTest(t, io.Discard, repo); err != nil {
		t.Fatalf("init: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, ".cursor")); !os.IsNotExist(err) {
		t.Errorf("init created .cursor in a repo without one: %v", err)
	}
	for _, f := range DetectScaffoldDrift(repo) {
		if strings.Contains(f.Path, ".cursor") {
			t.Errorf("drift reported cursor in a repo without it: %+v", f)
		}
	}

	if err := os.MkdirAll(filepath.Join(repo, ".cursor"), 0o755); err != nil {
		t.Fatal(err)
	}
	user := []byte(`{"version":1,"hooks":{"stop":[{"command":"/mine/stop.sh"}]}}`)
	path := filepath.Join(repo, filepath.FromSlash(cursorHooksRel))
	if err := os.WriteFile(path, user, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runInitTest(t, io.Discard, repo); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); !bytes.Equal(got, user) {
		t.Errorf("init rewrote a user-only hooks.json: %s", got)
	}
	for _, f := range DetectScaffoldDrift(repo) {
		if strings.Contains(f.Path, ".cursor") {
			t.Errorf("a user-only hooks.json is not drift: %+v", f)
		}
	}
}

// AC7: `hook context --harness cursor` answers {"additional_context":…} on cursor's
// own sessionStart event, within the embedded cursor budget, and degrades to a
// read instruction (naming cursor) when the budget is exceeded.
func TestHookContextCursorShapeAndBudget(t *testing.T) {
	repo := sessionContextRepo(t)
	sessionStart := func(session string) []byte {
		// The sessionStart payload of the capture (testdata/cursor/9-hooks.log).
		raw, err := os.ReadFile(filepath.Join(cursorFixtureDir, "9-hooks.log"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(raw), `"hook_event_name":"sessionStart"`) {
			t.Fatalf("capture 9 has no sessionStart payload: %s", raw)
		}
		return []byte(`{"hook_event_name":"sessionStart","session_id":"` + session + `","cursor_version":"2026.10.01-e373342"}`)
	}
	run := func(raw []byte) (stdout, stderr string) {
		var out, errb bytes.Buffer
		if err := runHookContext(&out, &errb, "cursor", raw); err != nil {
			t.Fatalf("runHookContext must fail open, got %v", err)
		}
		return out.String(), errb.String()
	}

	stdout, _ := run(sessionStart("cur-fits"))
	var doc map[string]any
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("output is not one JSON object: %q (%v)", stdout, err)
	}
	if len(doc) != 1 {
		t.Errorf("keys = %v, want only additional_context", doc)
	}
	ctx, _ := doc["additional_context"].(string)
	if !strings.Contains(ctx, "CONSTITUTION-MARKER-sty_507d3d9c") || !strings.Contains(ctx, "satelle-agent-goals") {
		t.Errorf("additional_context lacks the session set:\n%s", ctx)
	}
	if limit := (config.Config{}).ContextLimit("cursor"); len(ctx) > limit {
		t.Errorf("context is %d bytes, over cursor's budget", len(ctx))
	}

	// Cursor's event spelling is what matches: Claude's PascalCase event emits nothing.
	if out, _ := run([]byte(`{"hook_event_name":"SessionStart","cursor_version":"x"}`)); strings.TrimSpace(out) != "" {
		t.Errorf("a payload on another event must emit nothing, got %q", out)
	}

	// Over budget: an index plus a read instruction, and stderr names cursor and the limit.
	writeHarnessTOML(t, repo, "[harness.cursor]\ncontext_limit_bytes = 700\n")
	stdout, stderr := run(sessionStart("cur-tight"))
	doc = nil
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("degraded output is not JSON: %q (%v)", stdout, err)
	}
	ctx, _ = doc["additional_context"].(string)
	if len(ctx) > 700 {
		t.Errorf("degraded context is %d bytes, want at most 700:\n%s", len(ctx), ctx)
	}
	if strings.Contains(ctx, "invent process the workflow did not configure") {
		t.Errorf("a 700-byte budget inlined a principle body:\n%s", ctx)
	}
	if !strings.Contains(ctx, "read") {
		t.Errorf("degraded context has no read instruction:\n%s", ctx)
	}
	if !strings.Contains(stderr, "cursor harness limit (700 bytes)") {
		t.Errorf("stderr does not name the cursor budget: %q", stderr)
	}
}

// AC8: a stop that Claude would block is answered with followup_message; a clean
// stop, and the stop of a turn the followup already continued, print nothing.
func TestStopcheckCursorAnswersWithFollowup(t *testing.T) {
	prev := hookHarnessFlag
	hookHarnessFlag = ""
	t.Cleanup(func() { hookHarnessFlag = prev })
	stop := func(loop int) []byte {
		// The shape of the captured interactive stop (testdata/cursor/7-stop-hooks.log).
		b, _ := json.Marshal(map[string]any{
			"hook_event_name": "stop", "status": "completed", "loop_count": loop,
			"cursor_version": "2026.10.01-e373342",
		})
		return b
	}

	t.Run("ungated tree", func(t *testing.T) {
		repo, _ := stopcheckRepo(t, seatNone)
		dirtyTree(t, repo)
		var buf bytes.Buffer
		if err := runHookStopcheck(stop(0), &buf); err != nil {
			t.Fatal(err)
		}
		var out map[string]string
		if err := json.Unmarshal(buf.Bytes(), &out); err != nil || len(out) != 1 {
			t.Fatalf("stdout = %q (%v), want exactly followup_message", buf.String(), err)
		}
		for _, want := range []string{"STOP BLOCKED", "edits were made UNGATED", "internal/foo.go"} {
			if !strings.Contains(out["followup_message"], want) {
				t.Errorf("followup lacks %q: %q", want, out["followup_message"])
			}
		}
		// The same condition under Claude blocks with the same reason.
		var claude bytes.Buffer
		if err := runHookStopcheck([]byte("{}"), &claude); err != nil {
			t.Fatal(err)
		}
		var blk stopBlockOut
		if err := json.Unmarshal(claude.Bytes(), &blk); err != nil || blk.Reason != out["followup_message"] {
			t.Errorf("cursor and claude reasons differ:\ncursor: %q\nclaude: %q", out["followup_message"], blk.Reason)
		}
	})
	t.Run("clean tree", func(t *testing.T) {
		stopcheckRepo(t, seatNone)
		var buf bytes.Buffer
		if err := runHookStopcheck(stop(0), &buf); err != nil || buf.Len() != 0 {
			t.Fatalf("a clean stop must print nothing that re-prompts: out=%q err=%v", buf.String(), err)
		}
	})
	t.Run("already continued", func(t *testing.T) {
		repo, _ := stopcheckRepo(t, seatNone)
		dirtyTree(t, repo)
		var buf bytes.Buffer
		if err := runHookStopcheck(stop(1), &buf); err != nil || buf.Len() != 0 {
			t.Fatalf("a stop after a followup must not re-block: out=%q err=%v", buf.String(), err)
		}
	})
	t.Run("sibling's seat is not a re-prompt", func(t *testing.T) {
		repo, _ := stopcheckRepo(t, seatOther)
		dirtyTree(t, repo)
		var buf bytes.Buffer
		if err := runHookStopcheck(stop(0), &buf); err != nil || buf.Len() != 0 {
			t.Fatalf("an allow-with-note would become a followup turn on cursor: out=%q err=%v", buf.String(), err)
		}
	})
}

// AC11: with .cursor/hooks.json in [worktree] include, the worktree the real
// `satelle story worktree --base` opens carries the main tree's file, the
// absent-wiring guard passes there, and the gate governs against the worktree's
// own story; an init heal leaves the link alone.
func TestCursorWiringCarriedIntoWorktree(t *testing.T) {
	repo := tempRepo(t)
	t.Chdir(repo)
	t.Setenv("SATELLE_SERVER_ENDPOINT", "none")
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(repo, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(".gitignore", ".satelle/\n.cursor/\n")
	write("README.md", "x\n")
	write("internal/foo.go", "package internal\n")
	write(".satelle/satelle.toml", "[review]\ngate_create = false\n\n[worktree]\ninclude = [\".cursor/hooks.json\"]\nbranch = \"work/{id}\"\npath = \"../trees/{id}\"\n")
	for _, c := range [][]string{
		{"init", "-q", "-b", "main"}, {"config", "user.email", "t@example.com"}, {"config", "user.name", "t"},
		{"add", "-A"}, {"commit", "-q", "-m", "init"},
	} {
		gitIn(t, repo, c...)
	}
	if out, err := runRootIn(t, "", "agents", "install", "cursor"); err != nil {
		t.Fatalf("install cursor: %v\n%s", err, out)
	}

	out, err := runRoot(t, "story", "worktree", "sty_cur1", "--base", "main")
	if err != nil {
		t.Fatalf("story worktree: %v\n%s", err, out)
	}
	var res struct {
		Path    string `json:"path"`
		Entries []struct {
			Path   string `json:"path"`
			Status string `json:"status"`
		} `json:"entries"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil || res.Path == "" {
		t.Fatalf("story worktree output: %v\n%s", err, out)
	}
	wt := res.Path

	mainHooks := filepath.Join(repo, filepath.FromSlash(cursorHooksRel))
	wtHooks := filepath.Join(wt, filepath.FromSlash(cursorHooksRel))
	if st, err := os.Lstat(wtHooks); err != nil || st.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the worktree's hooks.json must be a link: %v", err)
	}
	gotTarget, _ := filepath.EvalSymlinks(wtHooks)
	wantTarget, _ := filepath.EvalSymlinks(mainHooks)
	if gotTarget != wantTarget {
		t.Fatalf("worktree hooks.json resolves to %s, want the main tree's %s", gotTarget, wantTarget)
	}

	// The absent-wiring guard passes in the worktree (and would not without the link).
	cfg, _, err := config.Load(filepath.Join(repo, ".satelle", "satelle.toml"))
	if err != nil {
		t.Fatal(err)
	}
	guard := agentstep.WiringGuardFrom(cfg)
	if missing := guard.Missing(wt, agentcli.HarnessCursor, agentcli.HarnessCursor); len(missing) != 0 {
		t.Errorf("the guard finds the worktree unwired: %v", missing)
	}
	bare := filepath.Join(filepath.Dir(wt), "bare")
	if err := os.MkdirAll(bare, 0o755); err != nil {
		t.Fatal(err)
	}
	if missing := guard.Missing(bare, agentcli.HarnessCursor, agentcli.HarnessCursor); len(missing) == 0 {
		t.Error("control: a tree without the link must be reported unwired")
	}

	// An init heal skips the link: the main tree's file is not rewritten through it.
	stale := `{"version":1,"hooks":{"preToolUse":[{"command":"sh /elsewhere/.satelle/hooks/satelle-hook.sh gate cursor"}]}}`
	if err := os.WriteFile(mainHooks, []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := healCursorHooks(io.Discard, wt); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := ensureCursorHooks(wt); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(mainHooks); string(got) != stale {
		t.Errorf("a heal in the worktree rewrote the main tree's file through the link:\n%s", got)
	}
	if st, err := os.Lstat(wtHooks); err != nil || st.Mode()&os.ModeSymlink == 0 {
		t.Errorf("the link was replaced: %v", err)
	}
	if action, _, _, err := removeCursorHooks(wt); err != nil || action != "skipped" {
		t.Errorf("remove through the link: action=%q err=%v, want skipped", action, err)
	}
	if err := os.WriteFile(mainHooks, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := runRootIn(t, "", "agents", "install", "cursor"); err != nil {
		t.Fatalf("reinstall: %v\n%s", err, out)
	}

	// The gate, invoked with the worktree as CLAUDE_PROJECT_DIR and cwd, governs
	// against the worktree's story: an executor-state seat there allows a Write
	// inside the worktree.
	wfDir := filepath.Join(repo, ".satelle", "workflows")
	writeRoute(t, wfDir,
		`["*"]
obligations = ["raised", "coded", "closed"]
`,
		`[raised]
status = "backlog"
start = true

[coded]
status = "in_progress"
agent = "executor"
requires = ["raised"]

[closed]
status = "done"
terminal = true
requires = ["coded"]
`)
	db, err := store.Open(runtimeDBPath(t))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := db.DocIndex.Sync(ctx, map[string]string{"workflows": wfDir}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	sty, err := db.Stories.Create(ctx, workitem.CreateInput{
		Kind: workitem.KindStory, Title: "worktree story", Body: "goal",
		AcceptanceCriteria: "1. ok", Status: "in_progress", Category: "chore",
	}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	// The seat records the worktree it was engaged from — the git toplevel the hook
	// resolves in that tree — not CanonicalRepoRoot(wt), which names the MAIN tree.
	wtTree, err := filepath.EvalSymlinks(wt)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := db.Leases.AcquireWith(ctx, lease.AcquireOpts{
		ItemID: sty.ID, Kind: "story", Owner: lease.ResolveOwner(), State: "in_progress",
		StorySeat: true, Worktree: wtTree,
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Leases.Confirm(ctx, sty.ID, "in_progress"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	t.Setenv("SATELLE_PROJECT_DIR", "")
	// A stamped session routes an unstamped seat by tree (pickSessionSeatFor), so the
	// seat in the worktree governs the worktree and not the main tree. An unstamped
	// session with one live seat would be handed that seat from either tree.
	t.Setenv(config.SessionEnv, "cursor-worktree-session")
	prev := hookHarnessFlag
	t.Cleanup(func() { hookHarnessFlag = prev })
	t.Chdir(wt)
	t.Setenv("CLAUDE_PROJECT_DIR", wt)
	inWT := cursorEnvelope("Write", map[string]any{"file_path": filepath.Join(wt, "internal", "foo.go")}, wt)
	if denied, out := decideHook(t, "gate", "cursor", inWT); denied {
		t.Errorf("gate cursor in the worktree denied a Write while its story is in an executor state:\n%s", out)
	}

	// The control: while the worktree's seat is still held, the main tree has no
	// engaged story of its own and the same Write there is denied. It runs before
	// the seat is released, so the denial is the main tree's, not a released seat's.
	t.Chdir(repo)
	t.Setenv("CLAUDE_PROJECT_DIR", repo)
	inMain := cursorEnvelope("Write", map[string]any{"file_path": filepath.Join(repo, "internal", "foo.go")}, repo)
	if denied, out := decideHook(t, "gate", "cursor", inMain); !denied {
		t.Errorf("gate cursor in the main tree allowed a Write with nothing engaged there: %q", out)
	}
}
