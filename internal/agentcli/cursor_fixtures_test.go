package agentcli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// These tests read only the captured cursor-agent runs under testdata/cursor
// and pin what was observed. They use no cursor adapter code: the captures are
// the evidence the sibling cursor stories build on (sty_383ff068).

const cursorDir = "testdata/cursor"

// cursorRuns are the captures that each carry a .meta.json (version, argv, date).
var cursorRuns = []string{
	"1a-json", "1b-stream", "1c-stdin", "1d-agentsmd", "1e-rules", "1g-fail",
	"2a-plan", "2b-ask", "2c-deny", "2d-control", "2e-hooks",
	"3-abs", "3-user", "4", "5-acp", "6", "clean-6", "7-deny", "7-stop",
	"8a-stdin-codeword", "8b-stdin-plus-arg",
}

// cursorFiles are the non-meta files the sibling stories read.
var cursorFiles = []string{
	"1a-json.out", "1b-stream.out", "2e-hooks.out",
	"1c-stdin.out", "1d-agentsmd.out", "1e-rules.out", "8a-stdin-codeword.out", "8b-stdin-plus-arg.out",
	"1g-fail.out", "1g-fail.err",
	"2a-plan.out", "2a-plan.fs", "2b-ask.out", "2b-ask.fs",
	"2c-deny.out", "2c-deny.fs", "2c-cli.json", "2d-control.out", "2d-control.fs",
	"2e-hooks.json", "2e-hook.sh", "2e-hooks.log", "2e-hooks.fs",
	"3-abs-hooks.json", "3-abs-hook.sh", "3-abs-hooks.log", "3-abs.out", "3-abs.fs",
	"3-user-hooks.json", "3-user-hook.sh", "3-user-hooks.log", "3-user.out", "3-user.fs",
	"4-hooks.json", "4-hook.sh", "4-hooks.log", "4.out", "4.fs",
	"7-deny-hooks.json", "7-deny-hook.sh", "7-deny-hooks.log", "7-deny.tty.log", "7-deny.fs",
	"7-stop-hooks.json", "7-stop-hook.sh", "7-stop-hooks.log", "7-stop.tty.log", "7-stop.fs",
	"5-acp.json",
	"6.out", "6-hook.sh", "6-hook-env.txt", "6-hook-env-names.txt",
	"clean-6.out", "clean-6-hook.sh", "clean-6-hook-env.txt", "clean-6-hook-env-names.txt",
	"README.md",
}

func cursorRead(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(cursorDir, name))
	if err != nil {
		t.Fatalf("cursor fixture %s: %v", name, err)
	}
	return string(b)
}

// cursorJSONLines parses each non-empty line of a stream-json capture.
func cursorJSONLines(t *testing.T, name string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(cursorRead(t, name), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("%s: line is not JSON: %v: %.80s", name, err, line)
		}
		out = append(out, m)
	}
	return out
}

// cursorFirstJSON parses the first line of a capture (8b's file has a stray
// tail from the re-run overwriting a longer file; see README).
func cursorFirstJSON(t *testing.T, name string) map[string]any {
	t.Helper()
	lines := strings.SplitN(cursorRead(t, name), "\n", 2)
	var m map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &m); err != nil {
		t.Fatalf("%s: first line is not JSON: %v", name, err)
	}
	return m
}

func cursorResult(t *testing.T, name string) string {
	t.Helper()
	m := cursorFirstJSON(t, name)
	s, _ := m["result"].(string)
	return s
}

func cursorUsageHasSplit(t *testing.T, name string, m map[string]any) {
	t.Helper()
	u, ok := m["usage"].(map[string]any)
	if !ok {
		t.Fatalf("%s: no usage object in %v", name, m)
	}
	for _, k := range []string{"inputTokens", "outputTokens", "cacheReadTokens", "cacheWriteTokens"} {
		if _, ok := u[k].(float64); !ok {
			t.Errorf("%s: usage.%s missing or not a number: %v", name, k, u)
		}
	}
}

func cursorHookEvents(t *testing.T, name string) []string {
	t.Helper()
	var evs []string
	for _, line := range strings.Split(cursorRead(t, name), "\n") {
		if line == "" {
			continue
		}
		ev, _, _ := strings.Cut(line, "\t")
		evs = append(evs, ev)
	}
	return evs
}

func cursorCount(evs []string, want string) int {
	n := 0
	for _, e := range evs {
		if e == want {
			n++
		}
	}
	return n
}

func cursorWantEvents(t *testing.T, name string, evs []string, want ...string) {
	t.Helper()
	for _, w := range want {
		if cursorCount(evs, w) == 0 {
			t.Errorf("%s: no %s event in %v", name, w, evs)
		}
	}
}

func TestCursorFixturesPresentWithProvenance(t *testing.T) {
	for _, f := range cursorFiles {
		if _, err := os.Stat(filepath.Join(cursorDir, f)); err != nil {
			t.Errorf("missing fixture %s: %v", f, err)
		}
	}
	for _, run := range cursorRuns {
		raw, err := os.ReadFile(filepath.Join(cursorDir, run+".meta.json"))
		if err != nil {
			t.Errorf("missing meta for %s: %v", run, err)
			continue
		}
		// argv is an array, or a single shell string for the piped-stdin runs.
		var meta struct {
			Version string          `json:"cursor_agent_version"`
			Date    string          `json:"date"`
			Argv    json.RawMessage `json:"argv"`
		}
		if err := json.Unmarshal(raw, &meta); err != nil {
			t.Errorf("%s.meta.json: %v", run, err)
			continue
		}
		if argv := string(meta.Argv); meta.Version == "" || meta.Date == "" || !strings.Contains(argv, "cursor-agent") {
			t.Errorf("%s.meta.json must record version, date and argv: %+v", run, meta)
		}
	}
}

func TestCursorFixturesListedAndRedacted(t *testing.T) {
	readme := cursorRead(t, "README.md")
	entries, err := os.ReadDir(cursorDir)
	if err != nil {
		t.Fatal(err)
	}
	email := regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+(\.[A-Za-z0-9-]+)+`)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if name != "README.md" && !strings.Contains(readme, name) {
			t.Errorf("README.md does not list %s", name)
		}
		body := strings.ReplaceAll(cursorRead(t, name), "redacted@example.invalid", "")
		if m := email.FindString(body); m != "" {
			t.Errorf("%s: unredacted email address %q", name, m)
		}
	}
}

func TestCursorInstructionDelivery(t *testing.T) {
	cases := []struct {
		file, token string
		want        bool
	}{
		{"1d-agentsmd.out", "ZEBRA-AGENTS", true},
		{"1e-rules.out", "ZEBRA-RULES", true},
		{"8a-stdin-codeword.out", "STDIN-ZEBRA", true},
		{"8b-stdin-plus-arg.out", "STDIN-OKAPI", false},
	}
	for _, c := range cases {
		if got := strings.Contains(cursorResult(t, c.file), c.token); got != c.want {
			t.Errorf("%s: result contains %s = %v, want %v", c.file, c.token, got, c.want)
		}
	}
}

func TestCursorJSONAndStreamJSON(t *testing.T) {
	j := cursorFirstJSON(t, "1a-json.out")
	if id, _ := j["session_id"].(string); id == "" {
		t.Errorf("1a-json: no session_id: %v", j)
	}
	cursorUsageHasSplit(t, "1a-json.out", j)

	for _, name := range []string{"1b-stream.out", "2e-hooks.out"} {
		lines := cursorJSONLines(t, name)
		init := lines[0]
		if init["type"] != "system" || init["subtype"] != "init" {
			t.Fatalf("%s: first event is not system/init: %v", name, init)
		}
		sid, _ := init["session_id"].(string)
		model, _ := init["model"].(string)
		if sid == "" || model == "" {
			t.Errorf("%s: init lacks session_id or model: %v", name, init)
		}
		if model == "composer-2.5" {
			t.Errorf("%s: init model %q is the requested id, expected the resolved display name", name, model)
		}
		final := lines[len(lines)-1]
		if final["type"] != "result" {
			t.Fatalf("%s: last event is not a result: %v", name, final)
		}
		if final["session_id"] != sid {
			t.Errorf("%s: result session_id %v != init %v", name, final["session_id"], sid)
		}
		cursorUsageHasSplit(t, name, final)
	}

	if out := strings.TrimSpace(cursorRead(t, "1g-fail.out")); json.Valid([]byte(out)) {
		t.Errorf("1g-fail.out stdout should not be JSON: %q", out)
	}
	if e := cursorRead(t, "1g-fail.err"); !strings.Contains(e, "Cannot use this model") || !strings.Contains(e, "exit=1") {
		t.Errorf("1g-fail.err should report the unknown model and exit=1")
	}
}

func TestCursorReadOnlyModes(t *testing.T) {
	for _, run := range []string{"2a-plan", "2b-ask", "2c-deny"} {
		fs := cursorRead(t, run+".fs")
		for _, want := range []string{"written.txt: absent", "shell.txt: absent"} {
			if !strings.Contains(fs, want) {
				t.Errorf("%s.fs: want %q, got %q", run, want, fs)
			}
		}
	}
	fs := cursorRead(t, "2d-control.fs")
	for _, want := range []string{"written.txt: PRESENT", "shell.txt: PRESENT"} {
		if !strings.Contains(fs, want) {
			t.Errorf("2d-control.fs: want %q, got %q", want, fs)
		}
	}
}

func TestCursorPrintModeHooks(t *testing.T) {
	evs := cursorHookEvents(t, "4-hooks.log")
	cursorWantEvents(t, "4-hooks.log", evs,
		"sessionStart", "preToolUse", "beforeShellExecution", "afterFileEdit", "postToolUse", "sessionEnd")
	if n := cursorCount(evs, "stop"); n != 0 {
		t.Errorf("4-hooks.log: stop fired %d times in print mode", n)
	}
	for _, line := range strings.Split(strings.TrimSpace(cursorRead(t, "4-hooks.log")), "\n") {
		if !strings.Contains(line, `"model":`) {
			t.Errorf("4-hooks.log: payload carries no model: %.80s", line)
		}
	}

	if got := strings.TrimSpace(cursorRead(t, "2e-hooks.log")); got != "" {
		t.Errorf("2e-hooks.log (relative-path hook) should be empty, got %.80s", got)
	}

	fs := cursorRead(t, "3-abs.fs")
	for _, want := range []string{"written.txt: absent", "shell.txt: absent"} {
		if !strings.Contains(fs, want) {
			t.Errorf("3-abs.fs: want %q, got %q", want, fs)
		}
	}
	fs = cursorRead(t, "4.fs")
	for _, want := range []string{"written.txt: PRESENT", "shell.txt: absent"} {
		if !strings.Contains(fs, want) {
			t.Errorf("4.fs: want %q, got %q", want, fs)
		}
	}
}

func TestCursorInteractiveHooks(t *testing.T) {
	evs := cursorHookEvents(t, "7-stop-hooks.log")
	cursorWantEvents(t, "7-stop-hooks.log", evs,
		"sessionStart", "beforeSubmitPrompt", "preToolUse", "beforeShellExecution", "postToolUse",
		"afterFileEdit", "afterAgentResponse", "stop", "sessionEnd")
	if n := cursorCount(evs, "stop"); n < 2 {
		t.Errorf("7-stop-hooks.log: want at least two stop events, got %d", n)
	}
	if !strings.Contains(cursorRead(t, "7-stop.tty.log"), "FOLLOWUP-SEEN") {
		t.Errorf("7-stop.tty.log: the followup_message never reached the agent")
	}
	fs := cursorRead(t, "7-deny.fs")
	for _, want := range []string{"written.txt: absent", "shell.txt: absent"} {
		if !strings.Contains(fs, want) {
			t.Errorf("7-deny.fs: want %q, got %q", want, fs)
		}
	}
}

// cursorToolOutcome returns the result key ("rejected", "error" or "success")
// and text of the completed tool_call whose call is the named kind.
func cursorToolOutcome(t *testing.T, name, kind string) (string, string) {
	t.Helper()
	for _, m := range cursorJSONLines(t, name) {
		if m["type"] != "tool_call" || m["subtype"] != "completed" {
			continue
		}
		tc, _ := m["tool_call"].(map[string]any)
		call, ok := tc[kind].(map[string]any)
		if !ok {
			continue
		}
		res, _ := call["result"].(map[string]any)
		for key, v := range res {
			body, _ := json.Marshal(v)
			return key, string(body)
		}
	}
	t.Fatalf("%s: no completed %s", name, kind)
	return "", ""
}

// TestCursorDogfoodAC13 pins the captured dogfood of satelle's installed cursor
// wiring (sty_7d098d50 AC13): refusals with satelle's reason, an engaged write,
// the session context, and the stop followup re-prompting the agent.
func TestCursorDogfoodAC13(t *testing.T) {
	refusals := []struct{ run, kind, reason string }{
		{"14-refuse-write", "editToolCall", "you're mutating the tree without a performing story"},
		{"14-refuse-delete", "deleteToolCall", "you're mutating the tree without a performing story"},
		{"14-refuse-shell-rm", "shellToolCall", "you're mutating the tree without a performing story"},
		{"14-refuse-git-commit", "shellToolCall", "refusing to commit/push with no engaged story"},
	}
	for _, r := range refusals {
		key, body := cursorToolOutcome(t, r.run+".out", r.kind)
		if key != "rejected" && key != "error" {
			t.Errorf("%s: %s result is %q, want a refusal", r.run, r.kind, key)
		}
		if !strings.Contains(body, "satelle: ") || !strings.Contains(body, r.reason) {
			t.Errorf("%s: refusal does not carry satelle's reason %q: %.200s", r.run, r.reason, body)
		}
		// the model saw the reason: it is quoted in the run's final result
		if res := cursorResult0(t, r.run+".out"); !strings.Contains(res, r.reason) {
			t.Errorf("%s: final result does not quote the reason: %.200s", r.run, res)
		}
	}
	if key, _ := cursorToolOutcome(t, "14-engaged-write.out", "editToolCall"); key != "success" {
		t.Errorf("14-engaged-write: edit result is %q, want success", key)
	}
	if res := cursorResult0(t, "14-context.out"); !strings.Contains(res, "# Always-resident principles (satelle)") {
		t.Errorf("14-context: reply %q does not quote the session context line", res)
	}
	tty := cursorRead(t, "14-stop.tty.txt")
	for _, want := range []string{"STOP BLOCKED", "follow-ups", "NOTED satelle: STOP BLOCKED", "UNGATED"} {
		if !strings.Contains(tty, want) {
			t.Errorf("14-stop.tty.txt: want %q", want)
		}
	}
	hooks := cursorRead(t, "14-transcript.md")
	for _, want := range []string{
		"satelle-hook.sh gate cursor", "satelle-hook.sh commitgate cursor",
		"satelle hook context --harness cursor", "satelle hook stopcheck --harness cursor",
	} {
		if !strings.Contains(hooks, want) {
			t.Errorf("14-transcript.md: installed hooks.json lacks %q", want)
		}
	}
	if n := strings.Count(hooks, "absent .claude/settings.json; absent .grok/hooks/satelle.json; absent .pi/extensions/satelle.ts"); n != 7 {
		t.Errorf("14-transcript.md: other-harness absence recorded %d times, want 7 (once per cursor-agent run)", n)
	}
}

// cursorResult0 is the "result" of the last JSON line (a stream-json run's
// final result event).
func cursorResult0(t *testing.T, name string) string {
	t.Helper()
	lines := cursorJSONLines(t, name)
	s, _ := lines[len(lines)-1]["result"].(string)
	return s
}

func TestCursorACP(t *testing.T) {
	var cap struct {
		Frames []struct {
			Dir string         `json:"dir"`
			Msg map[string]any `json:"msg"`
		} `json:"frames"`
	}
	if err := json.Unmarshal([]byte(cursorRead(t, "5-acp.json")), &cap); err != nil {
		t.Fatal(err)
	}
	result := func(id float64) map[string]any {
		for _, f := range cap.Frames {
			if f.Dir == "in" && f.Msg["id"] == id {
				r, _ := f.Msg["result"].(map[string]any)
				return r
			}
		}
		t.Fatalf("5-acp.json: no response to request %v", id)
		return nil
	}

	sess := result(2)
	models, _ := sess["models"].(map[string]any)
	if m, _ := models["currentModelId"].(string); m == "" {
		t.Errorf("session/new: no models.currentModelId: %v", sess)
	}
	modes, _ := sess["modes"].(map[string]any)
	have := map[string]bool{}
	avail, _ := modes["availableModes"].([]any)
	for _, a := range avail {
		if am, ok := a.(map[string]any); ok {
			id, _ := am["id"].(string)
			have[id] = true
		}
	}
	for _, want := range []string{"agent", "plan", "ask"} {
		if !have[want] {
			t.Errorf("session/new: mode %q not offered: %v", want, have)
		}
	}

	prompt := result(5)
	if s, _ := prompt["stopReason"].(string); s == "" {
		t.Errorf("session/prompt: no stopReason: %v", prompt)
	}
	if _, has := prompt["usage"]; has {
		t.Errorf("session/prompt: ACP result unexpectedly carries usage: %v", prompt)
	}
}

func TestCursorDetectionEnvironment(t *testing.T) {
	out := cursorResult(t, "clean-6.out")
	for _, want := range []string{"CURSOR_AGENT=1", "CURSOR_INVOKED_AS=cursor-agent"} {
		if !strings.Contains(out, want) {
			t.Errorf("clean-6.out: want %q in %q", want, out)
		}
	}
	clean := cursorRead(t, "clean-6-hook-env.txt")
	for _, want := range []string{"CURSOR_INVOKED_AS", "CURSOR_VERSION", "CLAUDE_PROJECT_DIR"} {
		if !strings.Contains(clean, want) {
			t.Errorf("clean-6-hook-env.txt: want %q in %q", want, clean)
		}
	}
	inherited := cursorRead(t, "6-hook-env.txt")
	for _, want := range []string{"CLAUDECODE=1", "AI_AGENT=claude-code"} {
		if !strings.Contains(inherited, want) {
			t.Errorf("6-hook-env.txt: want %q in %q", want, inherited)
		}
	}
}
