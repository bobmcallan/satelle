// Package agentcli abstracts the headless agent CLI the quality-management spine
// shells out to for isolated reviews and summaries. satellites hardcoded claude's
// flag surface (claude -p --append-system-prompt …); satelle routes every
// subprocess through a Runner driven by a CONFIG TEMPLATE, so the operator picks
// their agent and its exact argv in `.satelle/agents.toml` and no reviewer code
// names a binary or a flag directly.
//
// A harness string is a command template: the first token is the binary, the rest
// are argv tokens that may carry the placeholders {system}, {tools}, and {model}.
// At call time satelle substitutes each placeholder into its own argv token (so a
// multi-line system prompt stays a single argument) and pipes the payload on stdin.
// A bare CLI name (a single token, e.g. "claude") expands to that CLI's built-in
// PRESET template — claude's preset carries a read-only --disallowedTools denylist
// so the grant is a ceiling over the repo's settings, not just an allowlist floor.
package agentcli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"
)

// Supported agent CLI identifiers.
const (
	CLIClaude = "claude"
	CLICodex  = "codex"
)

// DefaultClaudeHarness is the claude preset template — the satellites gate argv
// reproduced behind the template seam, hardened with a --disallowedTools denylist
// so the reviewer's grant is a CEILING (deny wins over allow) over any
// permissions the repo's .claude/settings.json would otherwise inherit. {system}
// is the gate/skill body, {tools} the allow-grant, {model} the optional model
// (dropped, with its flag, when unset).
//
// The denylist keeps the work-tree MUTATORS off (Write, Edit, NotebookEdit, and
// Bash — a shell redirect writes files whatever the user's ~/.claude permission
// allows; sty_892517e7) so a
// reviewer can never modify the repo it judges — the read-only invariant. The
// reviewer's default allow-grant ({tools}) is read-only (Read, Grep, Glob) and
// needs no shell: the substrate it reasons about is materialised as markdown
// under .satelle, so it reads it directly. A repo MAY widen the grant in
// .satelle/agents.toml (transparently), but the default is read-only.
const DefaultClaudeHarness = "claude -p --output-format json --disallowedTools Write,Edit,NotebookEdit,Bash --append-system-prompt {system} --allowedTools {tools} --model {model}"

// Request is one headless agent invocation.
type Request struct {
	SystemPrompt string // {system}: appended as the system prompt (the gate/skill body)
	Payload      string // delivered on stdin (the review/summary input)
	AllowedTools string // {tools}: comma-separated tool grant
	Model        string // {model}: optional model override; "" drops the placeholder
	// Settings is {settings}: a pre-marshalled JSON object mirroring claude's
	// settings.local.json schema (env/model/permissions), already ${VAR}-resolved
	// by the caller (config.ResolveAgentEnvs / agentstep.buildRequest) — agentcli
	// never sees the raw map, only this JSON string, like Payload. "" drops the
	// placeholder AND a directly preceding flag, exactly like an empty Model, so a
	// binding with no settings emits no --settings arg. Passed INLINE (no
	// temp-file), so a secret it carries never touches disk. Values MAY be
	// secrets, so like Env it is never included in Command() evidence, logs, or
	// error strings.
	Settings string
	Dir      string // working directory for the subprocess
	// Env sets environment variables on the subprocess, layered onto the parent
	// env with these keys winning (composeEnv). Already ${VAR}-resolved by the
	// caller (config.ResolveAgentEnvs). Values MAY be secrets (an API token), so
	// Env is NEVER included in Command() evidence, logs, or error strings
	// (sty_001558ce).
	Env map[string]string
	// Sink, when non-nil, receives a live TEE of the subprocess's stdout/stderr AS
	// IT RUNS (stderr lines prefixed "[stderr] "), so a caller can write it to a
	// tailable per-dispatch log an operator watches while a long dispatch runs —
	// the only way to see a hang or an approaching timeout before the SIGKILL
	// (sty_0aa67b7f). It never affects the accumulated bytes runProcess returns for
	// the final JSON/usage parse; nil is a no-op (the default, unchanged behaviour).
	// Sink.Write errors are ignored (best-effort, like the reviewer/executor logs).
	Sink io.Writer
}

// UsageResult is the cost of one agent invocation — token counts and, when the
// caller times the run, its wall-clock duration. Populated from a machine-readable
// agent envelope (claude's `--output-format json`); a plain-text harness yields the
// zero value, so cost capture degrades gracefully (sty_a699ad14). It never carries
// the env or any secret — only numbers.
type UsageResult struct {
	InputTokens  int
	OutputTokens int
	TotalTokens  int
	Duration     time.Duration
}

// claudeJSONEnvelope is the shape of `claude -p --output-format json` output: the
// model's text lands in `result`, with token usage alongside. Only the fields we
// record are declared; extra fields are ignored.
type claudeJSONEnvelope struct {
	Result string `json:"result"`
	Usage  struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

// UnwrapUsage splits an agent's raw stdout into the INNER result text (what verdict
// parsing consumes, unchanged) and its token UsageResult. If stdout is a claude
// `--output-format json` envelope, the model's `result` text is returned with the
// usage; otherwise stdout is returned verbatim with a zero UsageResult — so a
// plain-text or non-claude harness keeps working and simply reports no cost. The
// Duration is set by the caller (UnwrapUsage measures only tokens).
func UnwrapUsage(stdout []byte) ([]byte, UsageResult) {
	trimmed := bytes.TrimSpace(stdout)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return stdout, UsageResult{}
	}
	var env claudeJSONEnvelope
	if err := json.Unmarshal(trimmed, &env); err != nil || env.Result == "" {
		return stdout, UsageResult{}
	}
	u := UsageResult{
		InputTokens:  env.Usage.InputTokens,
		OutputTokens: env.Usage.OutputTokens,
		TotalTokens:  env.Usage.InputTokens + env.Usage.OutputTokens,
	}
	return []byte(env.Result), u
}

// Runner invokes an agent CLI headlessly and returns its stdout.
type Runner interface {
	// Name reports the agent CLI identifier (the template's binary).
	Name() string
	// Command reports the resolved command/harness template (binary + argv with the
	// {system}/{tools}/{model} placeholders intact) — a concise, payload-free
	// description of HOW the agent is invoked, recorded as invocation evidence
	// (sty_fb3e0873). It never includes the rubric body or the stdin payload.
	Command() string
	// Run executes the agent over req and returns its raw stdout.
	Run(ctx context.Context, req Request) ([]byte, error)
}

// NewRunner returns the Runner for a bare CLI NAME — the preset. An empty name
// defaults to claude; "codex" is the not-yet-mapped stub; an unknown name errors.
// Callers with a full harness template use RunnerFromHarness instead.
func NewRunner(name string) (Runner, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", CLIClaude:
		return templateFromHarness(DefaultClaudeHarness), nil
	case CLICodex:
		return codexRunner{binary: CLICodex}, nil
	default:
		return nil, fmt.Errorf("agentcli: unknown agent cli %q (want %q or %q, or a full harness template)", name, CLIClaude, CLICodex)
	}
}

// RunnerFromHarness resolves an agents-layer harness binding to a Runner. An empty
// or "in-loop" harness returns (nil, nil): no agent-CLI runner, so the caller keeps
// its configured default. A SINGLE-token harness is a preset CLI name, resolved via
// NewRunner (so "codex" still errors as a stub). A MULTI-token harness is a literal
// command template: the first token is the binary, the rest the argv template.
func RunnerFromHarness(harness string) (Runner, error) {
	fields := strings.Fields(harness)
	if len(fields) == 0 || strings.ToLower(fields[0]) == "in-loop" {
		return nil, nil
	}
	if len(fields) == 1 {
		return NewRunner(fields[0])
	}
	return templateRunner{binary: fields[0], argTemplate: fields[1:]}, nil
}

// Detect returns the first supported agent CLI found on PATH (claude preferred),
// or "" when none is installed. Used by the install-time selection.
func Detect() string {
	for _, c := range []string{CLIClaude, CLICodex} {
		if _, err := exec.LookPath(c); err == nil {
			return c
		}
	}
	return ""
}

// Available reports whether the named CLI's binary is on PATH.
func Available(name string) bool {
	r, err := NewRunner(name)
	if err != nil {
		return false
	}
	_, lerr := exec.LookPath(r.Name())
	return lerr == nil
}

// templateRunner executes a command template: a binary plus an argv template whose
// tokens may carry {system}/{tools}/{model} placeholders. It is the single code
// path for every agent CLI — claude, codex, or any operator-supplied binary.
type templateRunner struct {
	binary      string
	argTemplate []string
}

// templateFromHarness parses a full harness string into a templateRunner.
func templateFromHarness(harness string) templateRunner {
	fields := strings.Fields(harness)
	return templateRunner{binary: fields[0], argTemplate: fields[1:]}
}

func (t templateRunner) Name() string { return t.binary }

// Command joins the binary and argv template (placeholders intact) into the
// payload-free harness string recorded as invocation evidence.
func (t templateRunner) Command() string {
	return strings.Join(append([]string{t.binary}, t.argTemplate...), " ")
}

func (t templateRunner) Run(ctx context.Context, req Request) ([]byte, error) {
	return runProcess(ctx, t.binary, buildArgs(t.argTemplate, req), req)
}

// buildArgs substitutes the placeholders in an argv template against req. Each of
// {system}/{tools}/{model}/{settings} must be its own token, so a multi-word value
// (a multi-line system prompt, or a JSON settings object) becomes exactly one
// argument. An empty {model} or {settings} drops the placeholder AND a directly
// preceding flag token (e.g. "--model {model}"), so the default template carries
// the flag without emitting an empty value — a binding that authors no `settings`
// table emits no --settings arg at all.
func buildArgs(argTemplate []string, req Request) []string {
	args := make([]string, 0, len(argTemplate))
	for _, tok := range argTemplate {
		switch tok {
		case "{system}":
			args = append(args, req.SystemPrompt)
		case "{tools}":
			args = append(args, req.AllowedTools)
		case "{model}":
			if strings.TrimSpace(req.Model) == "" {
				if n := len(args); n > 0 && strings.HasPrefix(args[n-1], "-") {
					args = args[:n-1] // drop the now-valueless flag
				}
				continue
			}
			args = append(args, req.Model)
		case "{settings}":
			if strings.TrimSpace(req.Settings) == "" {
				if n := len(args); n > 0 && strings.HasPrefix(args[n-1], "-") {
					args = args[:n-1] // drop the now-valueless flag
				}
				continue
			}
			args = append(args, req.Settings)
		default:
			args = append(args, tok)
		}
	}
	return args
}

// codexRunner is a placeholder: codex's headless surface differs from claude's
// (no --append-system-prompt), so a faithful preset is follow-up work. It is
// selectable so the seam is exercised, but Run errors clearly until mapped — a
// repo can still use codex today by setting a full [reviewer] harness template.
type codexRunner struct{ binary string }

func (c codexRunner) Name() string { return CLICodex }

// Command reports the codex binary; its full preset argv is not yet mapped.
func (c codexRunner) Command() string { return c.binary }

func (c codexRunner) Run(ctx context.Context, req Request) ([]byte, error) {
	return nil, fmt.Errorf("agentcli: the codex preset is not yet mapped — install claude, or set [reviewer] harness to a full codex command template in .satelle/agents.toml")
}

// composeEnv layers overlay onto base ("KEY=VALUE" entries, as from os.Environ),
// with overlay keys WINNING: any base entry whose key is in overlay is dropped,
// then overlay's pairs are appended in sorted-key order. The binding-wins
// guarantee and the deterministic order are OURS — not delegated to os/exec's
// duplicate-key behaviour — so it is unit-testable without an exec. An empty
// overlay returns base unchanged (sty_001558ce).
func composeEnv(base []string, overlay map[string]string) []string {
	if len(overlay) == 0 {
		return base
	}
	out := make([]string, 0, len(base)+len(overlay))
	for _, kv := range base {
		key := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			key = kv[:i]
		}
		if _, shadowed := overlay[key]; !shadowed {
			out = append(out, kv)
		}
	}
	keys := make([]string, 0, len(overlay))
	for k := range overlay {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		out = append(out, k+"="+overlay[k])
	}
	return out
}

// The dispatched subprocess inherits the launching shell's environment UNCHANGED,
// with only the binding's agents.toml `env` overlay added (composeEnv, overlay
// wins). There is NO secret filtering here — no denylist, no allowlist — on
// purpose. The env-leak bug was "found and fixed" twice (an ANTHROPIC_* denylist,
// then a PATH/HOME allowlist); each fix was satelle secretly deciding which parent
// vars reach the child, so the next new var re-triggered another fix. The filter
// was the bug. The separation auth needs does not require a filter: the subprocess
// runs with cwd = the repo root (agentstep sets Request.Dir = repoRoot), so
// `claude -p` reads .claude/settings(.local).json, whose `env` block OVERRIDES any
// inherited ANTHROPIC_* — verified: a bogus ANTHROPIC_* in the launching env is
// ignored when claude runs in the repo dir (and CLAUDE_CODE_*/AI_AGENT markers do
// not affect claude's auth). A binding that wants a different provider (e.g.
// [retrospective] on the GLM endpoint) sets it explicitly in its agents.toml `env`;
// that overlay is the ONLY env satelle contributes beyond the inherited shell.

// runProcess runs binary with args, feeding req.Payload on stdin in req.Dir, and
// returns the accumulated stdout. It streams via StdoutPipe/StderrPipe rather than
// buffering with cmd.Output(), so a non-nil req.Sink observes both streams
// INCREMENTALLY, before the process exits — a hang or an approaching timeout is
// visible on the sink, and a SIGKILL (ctx deadline) leaves whatever was already
// streamed on disk (sty_0aa67b7f). The returned bytes are the exact accumulated
// stdout regardless of req.Sink, so the caller's final JSON/usage parse is
// unaffected. A non-zero exit surfaces stderr in the error, as before.
func runProcess(ctx context.Context, binary string, args []string, req Request) ([]byte, error) {
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Stdin = strings.NewReader(req.Payload)
	if req.Dir != "" {
		cmd.Dir = req.Dir
	}
	cmd.Env = composeEnv(os.Environ(), req.Env)

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("agentcli: %s: %w", binary, err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("agentcli: %s: %w", binary, err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("agentcli: %s: %w", binary, err)
	}

	var stdout, stderr bytes.Buffer
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); teeLines(stdoutPipe, &stdout, req.Sink, "") }()
	go func() { defer wg.Done(); teeLines(stderrPipe, &stderr, req.Sink, "[stderr] ") }()
	wg.Wait()

	if err := cmd.Wait(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return stdout.Bytes(), fmt.Errorf("agentcli: %s: %w: %s", binary, err, msg)
		}
		return stdout.Bytes(), fmt.Errorf("agentcli: %s: %w", binary, err)
	}
	return stdout.Bytes(), nil
}

// teeLines reads r line-by-line until EOF (the pipe closing, whether from a clean
// exit or a SIGKILL), accumulating each RAW line into buf — the exact bytes a
// non-streaming read would have produced — and, when sink is non-nil, writing an
// immediate copy (with prefix, for stderr) to sink so a tailing observer sees it
// before the process finishes. Reading line-by-line rather than with io.Copy keeps
// stderr's "[stderr] " prefix aligned to real lines without a token-length limit
// (unlike bufio.Scanner). Sink write errors are ignored (best-effort).
func teeLines(r io.Reader, buf *bytes.Buffer, sink io.Writer, prefix string) {
	reader := bufio.NewReader(r)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			buf.Write(line)
			if sink != nil {
				if prefix != "" {
					_, _ = sink.Write([]byte(prefix))
				}
				_, _ = sink.Write(line)
			}
		}
		if err != nil {
			return
		}
	}
}
