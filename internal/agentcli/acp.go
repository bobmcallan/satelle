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
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
)

// acpRunner runs an Agent Client Protocol agent over stdio (epic:agent-dispatch-transport).
// command is the spawn line only (e.g. "grok agent stdio") — no template placeholders.
// System prompt and payload are delivered via session/prompt content blocks.
// One session per Run; no cross-dispatch reuse. Does not set story status.
type acpRunner struct {
	binary string
	args   []string
	how    string // body-free spawn string for Command() evidence
}

// newACPRunner builds an acpRunner from a multi-token spawn command.
// Rejects empty/in-loop, bare single tokens, and argv that still carry template placeholders.
func newACPRunner(command string) (Runner, error) {
	fields := strings.Fields(command)
	if len(fields) == 0 || strings.EqualFold(fields[0], "in-loop") {
		return nil, fmt.Errorf("agentcli: interface=acp requires a multi-token ACP spawn command (e.g. \"grok agent stdio\"), not in-loop/empty")
	}
	if len(fields) == 1 {
		return nil, fmt.Errorf("agentcli: interface=acp command %q: bare single token rejected — use a full ACP spawn line (e.g. \"grok agent stdio\")", fields[0])
	}
	for _, tok := range fields {
		switch tok {
		case "{system}", "{payload}", "{tools}", "{model}", "{effort}", "{settings}":
			return nil, fmt.Errorf("agentcli: interface=acp command must not contain placeholder %s — system/payload/tools/effort ride the ACP session, not argv", tok)
		}
	}
	return acpRunner{
		binary: fields[0],
		args:   fields[1:],
		how:    strings.Join(fields, " "),
	}, nil
}

func (a acpRunner) Name() string    { return a.binary }
func (a acpRunner) Command() string { return a.how }

// acpEffortArgvSupported reports whether the ACP spawn is Grok-shaped and may
// receive the Grok-only --reasoning-effort argv flag (sty_aa726901). Codex ACP
// (npx -y @agentclientprotocol/codex-acp) and unknown peers are false — effort
// rides session/set_config_option only. Deliberate allowlist: an unknown flag
// can abort a non-Grok spawn.
func acpEffortArgvSupported(binary string, args []string) bool {
	base := strings.ToLower(filepath.Base(binary))
	if strings.Contains(base, "grok") {
		return true
	}
	for _, a := range args {
		if strings.Contains(strings.ToLower(a), "grok") {
			return true
		}
	}
	return false
}

func (a acpRunner) Run(ctx context.Context, req Request) ([]byte, error) {
	args := append([]string(nil), a.args...)
	// Inject --reasoning-effort into spawn ONLY for Grok-shaped ACP peers
	// (sty_aa726901). --reasoning-effort is a Grok CLI flag, not ACP; unknown
	// peers (including Codex ACP via @agentclientprotocol/codex-acp) get effort
	// solely via session/set_config_option in runSession. Prefer insertion
	// before trailing "stdio" so `grok agent --reasoning-effort high stdio`.
	if e := strings.TrimSpace(req.Effort); e != "" && acpEffortArgvSupported(a.binary, a.args) {
		injected := false
		for i, t := range args {
			if t == "stdio" {
				args = append(args[:i], append([]string{"--reasoning-effort", e}, args[i:]...)...)
				injected = true
				break
			}
		}
		if !injected {
			args = append(args, "--reasoning-effort", e)
		}
	}
	cmd := exec.CommandContext(ctx, a.binary, args...)
	if req.Dir != "" {
		cmd.Dir = req.Dir
	}
	cmd.Env = composeEnv(os.Environ(), req.Env)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("agentcli: acp: stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("agentcli: acp: stdout: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("agentcli: acp: stderr: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("agentcli: acp: start %s: %w", a.binary, err)
	}

	var stderrBuf bytes.Buffer
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		teeLines(stderr, &stderrBuf, req.Sink, "[stderr] ")
	}()

	client := newACPClient(stdin, stdout, req.Sink)
	client.setMutatorsOK(toolsAllowMutators(req.AllowedTools))
	client.setCapture(req.Capture)
	text, runErr := client.runSession(ctx, req)

	_ = client.tryCancel()
	_ = stdin.Close()
	waitErr := cmd.Wait()
	wg.Wait()

	if runErr != nil {
		if msg := strings.TrimSpace(stderrBuf.String()); msg != "" {
			return text, fmt.Errorf("agentcli: acp: %w: %s", runErr, msg)
		}
		return text, fmt.Errorf("agentcli: acp: %w", runErr)
	}
	if waitErr != nil && ctx.Err() != nil {
		return text, fmt.Errorf("agentcli: acp: %w", ctx.Err())
	}
	if waitErr != nil && len(bytes.TrimSpace(text)) == 0 {
		if msg := strings.TrimSpace(stderrBuf.String()); msg != "" {
			return text, fmt.Errorf("agentcli: acp: process: %w: %s", waitErr, msg)
		}
		return text, fmt.Errorf("agentcli: acp: process: %w", waitErr)
	}
	return text, nil
}

// toolsAllowMutators is true when the binding's tools grant includes a write/edit
// or unrestricted shell capability (performer). Read-only grants (reviewer:
// read_file/Read, Bash(satelle:*) only) keep mutators denied.
func toolsAllowMutators(tools string) bool {
	t := strings.ToLower(tools)
	for _, needle := range []string{"write", "edit", "search_replace", "multiedit", "notebookedit", "run_terminal"} {
		if strings.Contains(t, needle) {
			return true
		}
	}
	// Unrestricted Bash / shell — not the satelle-only pull grant.
	if strings.Contains(t, "bash") && !strings.Contains(t, "satelle") {
		return true
	}
	return false
}

// isMutatorToolKind maps ACP ToolKind values that change the tree or run arbitrary code.
func isMutatorToolKind(kind string) bool {
	switch strings.ToLower(kind) {
	case "edit", "delete", "move", "execute":
		return true
	default:
		return false
	}
}

// --- JSON-RPC ACP client ---

type acpClient struct {
	w   io.WriteCloser
	wmu sync.Mutex

	nextID atomic.Int64

	mu      sync.Mutex
	pending map[int64]chan acpRPC
	// raw accumulates every agent_message_chunk in order — CaptureFull and
	// diagnostic partials on session/prompt error (byte-identical to pre-
	// sty_844b6ab1 capture; no inserted separators).
	raw strings.Builder
	// cur is the open agent_message run; segments holds closed runs in order.
	// A tool_call / tool_call_update closes cur into segments (sty_844b6ab1).
	// agent_thought_chunk is neither captured nor segmenting.
	cur        strings.Builder
	segments   []string
	session    string
	mutatorsOK bool
	sink       io.Writer
	readerDone chan struct{}
	// capture is set from Request.Capture before runSession returns text.
	capture CaptureMode
}

type acpRPC struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func newACPClient(w io.WriteCloser, r io.Reader, sink io.Writer) *acpClient {
	c := &acpClient{
		w:          w,
		pending:    map[int64]chan acpRPC{},
		sink:       sink,
		readerDone: make(chan struct{}),
	}
	go c.readLoop(r)
	return c
}

func (c *acpClient) setMutatorsOK(v bool) {
	c.mu.Lock()
	c.mutatorsOK = v
	c.mu.Unlock()
}

func (c *acpClient) setCapture(m CaptureMode) {
	c.mu.Lock()
	c.capture = m
	c.mu.Unlock()
}

func (c *acpClient) allowMutators() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.mutatorsOK
}

func (c *acpClient) readLoop(r io.Reader) {
	defer close(c.readerDone)
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		if c.sink != nil {
			_, _ = c.sink.Write(append(append([]byte{}, line...), '\n'))
		}
		var msg acpRPC
		if err := json.Unmarshal(line, &msg); err != nil {
			continue
		}
		if msg.Method == "session/update" {
			c.handleUpdate(msg.Params)
			continue
		}
		if msg.Method == "session/request_permission" && msg.ID != nil {
			c.handlePermission(*msg.ID, msg.Params)
			continue
		}
		if msg.ID != nil {
			c.mu.Lock()
			ch := c.pending[*msg.ID]
			c.mu.Unlock()
			if ch != nil {
				select {
				case ch <- msg:
				default:
				}
			}
		}
	}
}

func (c *acpClient) handleUpdate(params json.RawMessage) {
	var p struct {
		Update struct {
			SessionUpdate string `json:"sessionUpdate"`
			Content       struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"update"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return
	}
	su := p.Update.SessionUpdate
	switch su {
	case "agent_message_chunk":
		if p.Update.Content.Text == "" {
			return
		}
		c.mu.Lock()
		c.raw.WriteString(p.Update.Content.Text)
		c.cur.WriteString(p.Update.Content.Text)
		c.mu.Unlock()
	case "tool_call", "tool_call_update":
		// Close the open message run so post-tool answer is a new segment.
		// agent_thought_chunk deliberately does NOT close — only the tool
		// fence the wire establishes (sty_844b6ab1 AC1).
		c.mu.Lock()
		c.closeSegmentLocked()
		c.mu.Unlock()
		// agent_thought_chunk and every other sessionUpdate: ignore (not captured,
		// not segmenting). AC7 locks thought exclusion at the capture boundary.
	}
}

// closeSegmentLocked pushes cur into segments when non-empty and resets cur.
// Caller must hold c.mu.
func (c *acpClient) closeSegmentLocked() {
	if c.cur.Len() == 0 {
		return
	}
	c.segments = append(c.segments, c.cur.String())
	c.cur.Reset()
}

// capturedLocked returns the text for req.Capture after flushing the open run.
// Caller must hold c.mu. answer falls back to raw when segmentation yields
// nothing but raw is non-empty (never return empty when full is non-empty).
func (c *acpClient) capturedLocked() string {
	c.closeSegmentLocked()
	full := c.raw.String()
	if c.capture == CaptureFull {
		return full
	}
	// CaptureAnswer (default): last non-empty segment.
	for i := len(c.segments) - 1; i >= 0; i-- {
		if s := c.segments[i]; s != "" {
			return s
		}
	}
	// Safety: never drop a non-empty stream if segmentation produced nothing.
	return full
}

func (c *acpClient) handlePermission(id int64, params json.RawMessage) {
	var p struct {
		ToolCall struct {
			Kind string `json:"kind"`
		} `json:"toolCall"`
		Options []struct {
			OptionID string `json:"optionId"`
			Kind     string `json:"kind"`
		} `json:"options"`
	}
	_ = json.Unmarshal(params, &p)

	deny := isMutatorToolKind(p.ToolCall.Kind) && !c.allowMutators()
	optionID := ""
	for _, o := range p.Options {
		if deny && (o.Kind == "reject_once" || o.Kind == "reject_always") {
			optionID = o.OptionID
			break
		}
		if !deny && (o.Kind == "allow_once" || o.Kind == "allow_always") {
			optionID = o.OptionID
			break
		}
	}
	if optionID == "" && len(p.Options) > 0 {
		optionID = p.Options[0].OptionID
	}
	var result any
	if deny && optionID == "" {
		result = map[string]any{"outcome": map[string]any{"outcome": "cancelled"}}
	} else {
		result = map[string]any{"outcome": map[string]any{"outcome": "selected", "optionId": optionID}}
	}
	_ = c.respond(id, result)
}

func (c *acpClient) writeMsg(v any) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = c.w.Write(append(b, '\n'))
	return err
}

func (c *acpClient) respond(id int64, result any) error {
	return c.writeMsg(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  result,
	})
}

func (c *acpClient) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := c.nextID.Add(1)
	ch := make(chan acpRPC, 1)
	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()

	msg := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
	}
	if params != nil {
		msg["params"] = params
	}
	if err := c.writeMsg(msg); err != nil {
		return nil, err
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.readerDone:
		return nil, fmt.Errorf("agent closed stdout during %s", method)
	case resp := <-ch:
		if resp.Error != nil {
			return nil, fmt.Errorf("%s: %s", method, resp.Error.Message)
		}
		return resp.Result, nil
	}
}

func (c *acpClient) tryCancel() error {
	c.mu.Lock()
	sid := c.session
	c.mu.Unlock()
	if sid == "" {
		return nil
	}
	return c.writeMsg(map[string]any{
		"jsonrpc": "2.0",
		"method":  "session/cancel",
		"params":  map[string]any{"sessionId": sid},
	})
}

func (c *acpClient) runSession(ctx context.Context, req Request) ([]byte, error) {
	cwd := req.Dir
	if cwd == "" {
		cwd, _ = os.Getwd()
	}

	initRes, err := c.request(ctx, "initialize", map[string]any{
		"protocolVersion": 1,
		"clientInfo":      map[string]any{"name": "satelle", "version": "0"},
		"clientCapabilities": map[string]any{
			"fs":       map[string]any{"readTextFile": true, "writeTextFile": false},
			"terminal": false,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("initialize: %w", err)
	}

	// Authenticate only for Grok-shaped methods that reuse an existing CLI
	// session (cached_token / xai.api_key). Agent CLIs (Claude, Grok, Codex)
	// own their own login/configuration — satelle never supplies API keys or
	// drives Codex api-key / chat-gpt flows. Unknown advertised methods are
	// skipped so session/new uses the peer's already-authenticated CLI state.
	var initObj struct {
		AuthMethods []struct {
			ID string `json:"id"`
		} `json:"authMethods"`
	}
	_ = json.Unmarshal(initRes, &initObj)
	methodID := ""
	for _, m := range initObj.AuthMethods {
		if m.ID == "cached_token" || m.ID == "xai.api_key" {
			methodID = m.ID
			break
		}
	}
	if methodID != "" {
		if _, err := c.request(ctx, "authenticate", map[string]any{
			"methodId": methodID,
			"_meta":    map[string]any{"headless": true},
		}); err != nil {
			// Some fake peers skip auth; only fail if we elected a known method.
			return nil, fmt.Errorf("authenticate: %w", err)
		}
	}

	sessRes, err := c.request(ctx, "session/new", map[string]any{
		"cwd":        cwd,
		"mcpServers": []any{},
	})
	if err != nil {
		return nil, fmt.Errorf("session/new: %w", err)
	}
	var sessObj struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(sessRes, &sessObj); err != nil || sessObj.SessionID == "" {
		return nil, fmt.Errorf("session/new: missing sessionId")
	}
	c.mu.Lock()
	c.session = sessObj.SessionID
	c.mu.Unlock()

	// Optional model config (sty_a476a2f8). A peer that explicitly rejects the
	// model value fails the run (so a reported model is the model that ran).
	// Peers that do not implement set_config_option (Method not found / unsupported)
	// are a soft miss — log and continue; the binding still spawned with its
	// default model (same as before for those peers).
	if m := strings.TrimSpace(req.Model); m != "" {
		if _, err := c.request(ctx, "session/set_config_option", map[string]any{
			"sessionId": sessObj.SessionID,
			"configId":  "model",
			"value":     m,
		}); err != nil {
			es := err.Error()
			if strings.Contains(strings.ToLower(es), "method not found") ||
				strings.Contains(strings.ToLower(es), "not supported") ||
				strings.Contains(strings.ToLower(es), "unknown method") {
				// soft: peer lacks the method — cannot confirm model change
				_ = es
			} else {
				return nil, fmt.Errorf("session/set_config_option model=%q rejected by peer: %w", m, err)
			}
		}
	}
	// Effort: try common config ids; ignore failures (peers vary).
	if e := strings.TrimSpace(req.Effort); e != "" {
		// Prefer reasoning_effort; also try effort for peers that use that id.
		_, _ = c.request(ctx, "session/set_config_option", map[string]any{
			"sessionId": sessObj.SessionID,
			"configId":  "reasoning_effort",
			"value":     e,
		})
		_, _ = c.request(ctx, "session/set_config_option", map[string]any{
			"sessionId": sessObj.SessionID,
			"configId":  "effort",
			"value":     e,
		})
	}

	// Pack system + payload as text content blocks (no argv placeholders).
	var blocks []map[string]any
	if sp := strings.TrimSpace(req.SystemPrompt); sp != "" {
		blocks = append(blocks, map[string]any{"type": "text", "text": sp})
	}
	if pay := strings.TrimSpace(req.Payload); pay != "" {
		blocks = append(blocks, map[string]any{"type": "text", "text": pay})
	}
	if len(blocks) == 0 {
		blocks = append(blocks, map[string]any{"type": "text", "text": "{}"})
	}

	if _, err := c.request(ctx, "session/prompt", map[string]any{
		"sessionId": sessObj.SessionID,
		"prompt":    blocks,
	}); err != nil {
		// Error path: return full raw accumulation for diagnostics — truncating
		// to the answer segment would lose the reason the run failed.
		c.mu.Lock()
		partial := c.raw.String()
		c.mu.Unlock()
		return []byte(partial), fmt.Errorf("session/prompt: %w", err)
	}

	c.mu.Lock()
	out := c.capturedLocked()
	c.mu.Unlock()
	return []byte(out), nil
}
