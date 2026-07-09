package agentcli

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

// syncBuffer is a concurrency-safe io.Writer: runProcess tees stdout/stderr from
// two goroutines, and these tests read the sink WHILE the subprocess is still
// running, so a plain bytes.Buffer would race.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

func TestNewRunnerMapping(t *testing.T) {
	cases := []struct {
		name    string
		want    string
		wantErr bool
	}{
		{"", CLIClaude, false}, // empty defaults to claude
		{"claude", CLIClaude, false},
		{"CLAUDE", CLIClaude, false}, // case-insensitive
		{"codex", CLICodex, false},
		{"gpt", "", true},
	}
	for _, c := range cases {
		r, err := NewRunner(c.name)
		if c.wantErr {
			if err == nil {
				t.Errorf("NewRunner(%q): expected error", c.name)
			}
			continue
		}
		if err != nil {
			t.Errorf("NewRunner(%q): %v", c.name, err)
			continue
		}
		if r.Name() != c.want {
			t.Errorf("NewRunner(%q).Name() = %q, want %q", c.name, r.Name(), c.want)
		}
	}
}

func TestCodexStubErrorsClearly(t *testing.T) {
	r, err := NewRunner(CLICodex)
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.Run(context.Background(), Request{SystemPrompt: "x", Payload: "y"})
	if err == nil {
		t.Fatal("codex Run should error until mapped")
	}
	if !strings.Contains(err.Error(), "not yet mapped") {
		t.Errorf("codex error should be explicit, got: %v", err)
	}
}

// The claude preset must deny every work-tree MUTATOR so the grant is a CEILING
// (deny wins over allow) — INCLUDING Bash (sty_892517e7): a headless reviewer
// inherits the user's ~/.claude permission allows, so without Bash on the deny
// ceiling a "read-only" reviewer can write files via a shell redirect — observed
// live (the old push-review wrote summaries). Every reviewer rubric states "you
// cannot run commands"; the denylist now makes that true. An agent that MUST
// mutate is a named agent with an explicit full-command harness (no preset
// denylist), e.g. commit-agent.
func TestDefaultClaudeHarnessHasDenylistCeiling(t *testing.T) {
	for _, deny := range []string{"--disallowedTools", "Write", "Edit", "NotebookEdit", "Bash"} {
		if !strings.Contains(DefaultClaudeHarness, deny) {
			t.Errorf("DefaultClaudeHarness must include %q (mutator ceiling): %q", deny, DefaultClaudeHarness)
		}
	}
	rest := DefaultClaudeHarness[strings.Index(DefaultClaudeHarness, "--disallowedTools ")+len("--disallowedTools "):]
	if end := strings.Index(rest, " "); end >= 0 {
		rest = rest[:end]
	}
	if !strings.Contains(rest, "Bash") {
		t.Errorf("Bash must be on the deny ceiling (reviewers judge, never run commands): %q", rest)
	}
}

// A multi-line {system} value must survive as a SINGLE argv token, and {tools}
// must substitute in place.
func TestBuildArgsSubstitutesTokens(t *testing.T) {
	tmpl := strings.Fields("-p --append-system-prompt {system} --allowedTools {tools}")
	sys := "first line\nsecond line with spaces\tand a tab"
	args := buildArgs(tmpl, Request{SystemPrompt: sys, AllowedTools: "Read,Grep,Glob"})
	if !contains(args, sys) {
		t.Errorf("multi-line {system} was not preserved as one arg: %#v", args)
	}
	if !contains(args, "Read,Grep,Glob") {
		t.Errorf("{tools} not substituted: %#v", args)
	}
	if !contains(args, "--append-system-prompt") || !contains(args, "-p") {
		t.Errorf("literal flags missing: %#v", args)
	}
}

// An empty model drops both the {model} placeholder and its preceding flag.
func TestBuildArgsDropsEmptyModelFlag(t *testing.T) {
	tmpl := strings.Fields("--allowedTools {tools} --model {model}")
	args := buildArgs(tmpl, Request{AllowedTools: "Read", Model: ""})
	for _, a := range args {
		if a == "{model}" || a == "--model" || a == "" {
			t.Errorf("empty model should drop --model {model}, got %#v", args)
		}
	}
	if !contains(args, "Read") {
		t.Errorf("non-model args should remain: %#v", args)
	}
}

// A set model substitutes in place, keeping its flag.
func TestBuildArgsKeepsSetModel(t *testing.T) {
	tmpl := strings.Fields("--model {model}")
	args := buildArgs(tmpl, Request{Model: "claude-opus-4-8"})
	if len(args) != 2 || args[0] != "--model" || args[1] != "claude-opus-4-8" {
		t.Errorf("set model should yield [--model claude-opus-4-8], got %#v", args)
	}
}

// An empty settings drops both the {settings} placeholder and its preceding flag
// — mirroring the empty-{model} behaviour — so a binding that authors no
// `settings` table emits no --settings arg at all.
func TestBuildArgsDropsEmptySettingsFlag(t *testing.T) {
	tmpl := strings.Fields("--allowedTools {tools} --settings {settings}")
	args := buildArgs(tmpl, Request{AllowedTools: "Read", Settings: ""})
	for _, a := range args {
		if a == "{settings}" || a == "--settings" || a == "" {
			t.Errorf("empty settings should drop --settings {settings}, got %#v", args)
		}
	}
	if !contains(args, "Read") {
		t.Errorf("non-settings args should remain: %#v", args)
	}
}

// A set settings JSON string substitutes in place as a SINGLE argv token, keeping
// its flag.
func TestBuildArgsKeepsSetSettings(t *testing.T) {
	tmpl := strings.Fields("--settings {settings}")
	json := `{"env":{"ANTHROPIC_BASE_URL":"https://api.z.ai/api/anthropic"}}`
	args := buildArgs(tmpl, Request{Settings: json})
	if len(args) != 2 || args[0] != "--settings" || args[1] != json {
		t.Errorf("set settings should yield [--settings %s], got %#v", json, args)
	}
}

// An unrecognised placeholder is passed through verbatim, not dropped or expanded.
func TestBuildArgsLeavesUnknownTokens(t *testing.T) {
	args := buildArgs(strings.Fields("--flag {unknown} value"), Request{})
	if !contains(args, "{unknown}") || !contains(args, "--flag") || !contains(args, "value") {
		t.Errorf("unknown tokens should pass through verbatim: %#v", args)
	}
}

// The payload is delivered on the subprocess's stdin (verified via `cat`, which
// echoes stdin to stdout). Exercises the full templateRunner.Run path.
func TestRunDeliversPayloadOnStdin(t *testing.T) {
	if _, err := exec.LookPath("cat"); err != nil {
		t.Skip("cat not on PATH")
	}
	r := templateRunner{binary: "cat"}
	out, err := r.Run(context.Background(), Request{Payload: "hello from stdin\n"})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if string(out) != "hello from stdin\n" {
		t.Errorf("stdin not delivered: got %q", string(out))
	}
}

// {payload} substitutes as exactly one argv token (multi-line / JSON stays one
// arg), including when empty — empty does not drop a preceding flag (unlike
// {model}/{settings}). sty_5cf4a1fb.
func TestBuildArgsSubstitutesPayload(t *testing.T) {
	tmpl := strings.Fields("-p {payload} --flag x")
	payload := "{\n  \"story\": \"id with spaces\"\n}"
	args := buildArgs(tmpl, Request{Payload: payload})
	if len(args) != 4 || args[0] != "-p" || args[1] != payload || args[2] != "--flag" || args[3] != "x" {
		t.Errorf("multi-line {payload} should be one token after -p: %#v", args)
	}

	// Empty payload still emits the token and keeps the preceding flag.
	argsEmpty := buildArgs(tmpl, Request{Payload: ""})
	if len(argsEmpty) != 4 || argsEmpty[0] != "-p" || argsEmpty[1] != "" {
		t.Errorf("empty {payload} must not drop -p: %#v", argsEmpty)
	}
}

// Command() keeps the literal {payload} token — never the expanded body
// (invocation evidence must stay payload-free). sty_5cf4a1fb AC3.
func TestCommandKeepsPayloadPlaceholder(t *testing.T) {
	r := templateFromHarness("fake-cli -p {payload} --append-system-prompt {system}")
	cmd := r.Command()
	if !strings.Contains(cmd, "{payload}") {
		t.Errorf("Command() must keep literal {payload}: %q", cmd)
	}
	if strings.Contains(cmd, `"story"`) || strings.Contains(cmd, "secret-body") {
		t.Errorf("Command() must not expand payload body: %q", cmd)
	}
	// And Run still expands for the real argv (sanity).
	args := buildArgs(r.argTemplate, Request{Payload: "secret-body", SystemPrompt: "sys"})
	if !contains(args, "secret-body") || !contains(args, "sys") {
		t.Errorf("buildArgs should expand for Run: %#v", args)
	}
}

// Dual delivery: when the harness includes {payload}, the child receives the
// same bytes on argv AND on stdin. Uses a tiny sh script so we need no fixture
// binary. sty_5cf4a1fb AC1/AC2.
func TestRunDualDeliversPayloadArgvAndStdin(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not on PATH")
	}
	// argv[1] is the {payload} token; stdin is the dual channel. Print both so
	// the test can assert equality without a custom binary.
	r := templateRunner{
		binary:      "sh",
		argTemplate: []string{"-c", `printf 'ARGV:%s\nSTDIN:' "$1"; cat`, "_", "{payload}"},
	}
	payload := `{"id":"sty_test","from":"a","to":"b"}`
	out, err := r.Run(context.Background(), Request{Payload: payload})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	got := string(out)
	wantArgv := "ARGV:" + payload + "\n"
	wantStdin := "STDIN:" + payload
	if !strings.HasPrefix(got, wantArgv) {
		t.Errorf("argv channel missing/wrong:\n got %q\nwant prefix %q", got, wantArgv)
	}
	if !strings.Contains(got, wantStdin) {
		t.Errorf("stdin channel missing/wrong:\n got %q\nwant contain %q", got, wantStdin)
	}
}

// Stdin-only harness (no {payload} token) still receives the body on stdin and
// does not invent an argv payload — Claude-style templates stay unchanged.
func TestRunStdinOnlyHarnessUnchanged(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not on PATH")
	}
	// No {payload} in template: only echo marker + cat stdin.
	r := templateRunner{
		binary:      "sh",
		argTemplate: []string{"-c", `printf 'NOARG\n'; cat`},
	}
	payload := "stdin-only-body\n"
	out, err := r.Run(context.Background(), Request{Payload: payload})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if string(out) != "NOARG\n"+payload {
		t.Errorf("stdin-only harness: got %q", out)
	}
}

// TestRunInjectsEnv drives the whole runProcess env seam against a real
// subprocess: req.Env must reach the child's environment (sty_001558ce).
func TestRunInjectsEnv(t *testing.T) {
	if _, err := exec.LookPath("printenv"); err != nil {
		t.Skip("printenv not on PATH")
	}
	r := templateRunner{binary: "printenv", argTemplate: []string{"SATELLE_TEST_TOKEN"}}
	out, err := r.Run(context.Background(), Request{Env: map[string]string{"SATELLE_TEST_TOKEN": "sk-injected"}})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if strings.TrimSpace(string(out)) != "sk-injected" {
		t.Errorf("env not injected into child: got %q", string(out))
	}
}

func TestComposeEnv(t *testing.T) {
	base := []string{"PATH=/bin", "HOME=/root", "ANTHROPIC_BASE_URL=https://api.anthropic.com"}

	// Empty overlay returns base unchanged (same slice).
	if got := composeEnv(base, nil); len(got) != len(base) {
		t.Fatalf("empty overlay changed base: %v", got)
	}

	// Overlay key WINS: the base ANTHROPIC_BASE_URL entry is dropped, ours appended;
	// disjoint base keys (PATH, HOME) survive; a new key is added. Sorted overlay order.
	overlay := map[string]string{
		"ANTHROPIC_BASE_URL":   "https://api.z.ai/api/anthropic",
		"ANTHROPIC_AUTH_TOKEN": "sk-secret",
	}
	got := composeEnv(base, overlay)
	seen := map[string]int{}
	for _, kv := range got {
		i := strings.IndexByte(kv, '=')
		seen[kv[:i]]++
	}
	if seen["ANTHROPIC_BASE_URL"] != 1 {
		t.Errorf("ANTHROPIC_BASE_URL appears %d times, want 1 (no duplicate)", seen["ANTHROPIC_BASE_URL"])
	}
	if !contains(got, "ANTHROPIC_BASE_URL=https://api.z.ai/api/anthropic") {
		t.Errorf("overlay did not win: %v", got)
	}
	if !contains(got, "ANTHROPIC_AUTH_TOKEN=sk-secret") {
		t.Errorf("new overlay key missing: %v", got)
	}
	if !contains(got, "PATH=/bin") || !contains(got, "HOME=/root") {
		t.Errorf("disjoint base keys lost: %v", got)
	}
}

// TestRunInheritsParentEnvNoFilter pins the NO-FILTERING contract: the dispatched
// subprocess inherits the launching shell's env UNCHANGED (no denylist, no
// allowlist), with only the binding's agents.toml `env` overlay added. A sentinel
// set in the parent (the test process) passes straight through to the child, and
// the overlay var is injected on top. Auth separation does not need a filter —
// claude runs in the repo dir and reads .claude/settings.local.json, whose env
// block overrides any inherited ANTHROPIC_* (verified out-of-band). This test is
// the regression guard against re-adding a secret env filter: the "found and fixed
// twice" recurrence (ANTHROPIC_* denylist, then a PATH/HOME allowlist) was the
// filter itself being the bug.
func TestRunInheritsParentEnvNoFilter(t *testing.T) {
	if _, err := exec.LookPath("printenv"); err != nil {
		t.Skip("printenv not on PATH")
	}
	// A sentinel in the PARENT env (the launching shell). No filter drops it.
	t.Setenv("SATELLE_TEST_SENTINEL", "inherited-from-parent")
	// The binding's agents.toml `env` overlay — the ONLY env satelle adds.
	r := templateRunner{binary: "printenv", argTemplate: []string{"SATELLE_TEST_SENTINEL", "SATELLE_TEST_OVERLAY"}}
	out, err := r.Run(context.Background(), Request{Env: map[string]string{"SATELLE_TEST_OVERLAY": "injected-overlay"}})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	got := strings.TrimSpace(string(out))
	// printenv prints one value per line for set vars; both must be present — the
	// sentinel (inherited, no filter) and the overlay (injected).
	if !strings.Contains(got, "inherited-from-parent") {
		t.Errorf("parent sentinel dropped — a secret env filter was re-introduced: %q", got)
	}
	if !strings.Contains(got, "injected-overlay") {
		t.Errorf("binding overlay not injected: %q", got)
	}
}

func TestUnwrapUsage(t *testing.T) {
	// A claude --output-format json envelope: result text extracted, usage captured.
	env := `{"type":"result","result":"the verdict text","usage":{"input_tokens":54,"output_tokens":59}}`
	text, u := UnwrapUsage([]byte(env))
	if string(text) != "the verdict text" {
		t.Errorf("result not extracted: %q", text)
	}
	if u.InputTokens != 54 || u.OutputTokens != 59 || u.TotalTokens != 113 {
		t.Errorf("usage = %+v, want 54/59/113", u)
	}
	// Plain text (a non-json harness) passes through verbatim with zero usage.
	raw := "Verdict: accept.\n"
	text, u = UnwrapUsage([]byte(raw))
	if string(text) != raw || u.TotalTokens != 0 {
		t.Errorf("plain text should pass through with zero usage: %q %+v", text, u)
	}
	// A JSON object that is NOT the envelope (no result, no text) passes through
	// untouched, so verdict parsing still sees the original bytes (never a silent empty).
	other := `{"decision":"accept"}`
	text, u = UnwrapUsage([]byte(other))
	if string(text) != other || u.TotalTokens != 0 {
		t.Errorf("non-envelope json should pass through: %q %+v", text, u)
	}
	// Grok --output-format json envelope: model reply is in .text (often nested
	// decision JSON). Without unwrap, parseDecision cannot see a decision inside
	// the escaped string (dogfood sty_5cf4a1fb).
	grokEnv := `{"text":"{\"decision\":\"accept\",\"notes\":\"dogfood\"}","stopReason":"EndTurn","sessionId":"x"}`
	text, u = UnwrapUsage([]byte(grokEnv))
	if string(text) != `{"decision":"accept","notes":"dogfood"}` {
		t.Errorf("grok text not extracted: %q", text)
	}
	if u.TotalTokens != 0 {
		t.Errorf("grok envelope has no usage fields in this shape: %+v", u)
	}
}

// TestRunStreamsOutputBeforeExit pins AC1/AC3: a non-nil Sink observes stdout
// INCREMENTALLY — a line is visible on the sink while the subprocess is still
// sleeping between echoes, not only after it exits.
func TestRunStreamsOutputBeforeExit(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not on PATH")
	}
	r := templateRunner{binary: "sh", argTemplate: []string{"-c", "echo before-sleep; sleep 0.3; echo after-sleep"}}
	sink := &syncBuffer{}
	done := make(chan []byte, 1)
	go func() {
		out, err := r.Run(context.Background(), Request{Sink: sink})
		if err != nil {
			t.Errorf("run: %v", err)
		}
		done <- out
	}()

	deadline := time.Now().Add(3 * time.Second)
	for !strings.Contains(sink.String(), "before-sleep") {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for streamed output to reach the sink")
		}
		time.Sleep(5 * time.Millisecond)
	}
	// The subprocess is still sleeping — it must not have finished yet, proving the
	// sink saw the first line BEFORE exit, not merely as a post-exit flush.
	select {
	case out := <-done:
		t.Fatalf("process already exited when the sink observed the first line — not incremental: %q", out)
	default:
	}

	out := <-done
	if !strings.Contains(string(out), "before-sleep") || !strings.Contains(string(out), "after-sleep") {
		t.Errorf("final accumulated stdout incomplete: %q", out)
	}
	if !strings.Contains(sink.String(), "after-sleep") {
		t.Errorf("sink should also carry the second line once it's written: %q", sink.String())
	}
}

// TestRunStreamsStderrWithPrefixWithoutLeakingIntoStdout pins the [stderr]
// interleaving decision: stderr reaches the sink prefixed, but never contaminates
// the returned stdout bytes the caller parses as the result.
func TestRunStreamsStderrWithPrefixWithoutLeakingIntoStdout(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not on PATH")
	}
	r := templateRunner{binary: "sh", argTemplate: []string{"-c", "echo out-line; echo err-line 1>&2"}}
	sink := &syncBuffer{}
	out, err := r.Run(context.Background(), Request{Sink: sink})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(string(out), "out-line") {
		t.Errorf("stdout missing its own line: %q", out)
	}
	if strings.Contains(string(out), "err-line") {
		t.Errorf("stderr must not leak into the returned stdout buffer: %q", out)
	}
	if !strings.Contains(sink.String(), "[stderr] err-line") {
		t.Errorf("stderr should reach the sink prefixed [stderr]: %q", sink.String())
	}
}

// TestRunKillRetainsStreamedOutputOnTimeout pins AC4: a ctx deadline SIGKILLs the
// child (exec.CommandContext), but whatever was already streamed to the sink
// before the kill is retained for diagnosis, not lost with an in-memory buffer.
func TestRunKillRetainsStreamedOutputOnTimeout(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not on PATH")
	}
	// "exec sleep 5" replaces the shell's own process image rather than forking a
	// grandchild, so killing the direct child pid actually kills the sleeper too —
	// a bare "sleep 5" as a separate grandchild would keep the pipe's write end
	// open (inherited fd) until it finished on its own, making Wait() hang for the
	// full 5s regardless of the kill (a pre-existing os/exec characteristic, not
	// something this change introduces).
	r := templateRunner{binary: "sh", argTemplate: []string{"-c", "echo partial; exec sleep 5"}}
	sink := &syncBuffer{}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	_, err := r.Run(ctx, Request{Sink: sink})
	if err == nil {
		t.Fatal("expected the timeout to kill the child and surface an error")
	}
	if !strings.Contains(sink.String(), "partial") {
		t.Errorf("sink should retain the streamed line from before the kill: %q", sink.String())
	}
}

// TestRunWithoutSinkUnchanged pins AC2's backward-compat half: a nil Sink (every
// existing caller) behaves exactly as before — no streaming machinery visible,
// same accumulated stdout returned.
func TestRunWithoutSinkUnchanged(t *testing.T) {
	if _, err := exec.LookPath("cat"); err != nil {
		t.Skip("cat not on PATH")
	}
	r := templateRunner{binary: "cat"}
	out, err := r.Run(context.Background(), Request{Payload: "hello\n"})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if string(out) != "hello\n" {
		t.Errorf("nil-sink run should behave as before: got %q", out)
	}
}
