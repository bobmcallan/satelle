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

func (a acpRunner) Open(ctx context.Context, req Request, pol PermissionPolicy) (Session, error) {
	return openACPSession(ctx, a, req, pol)
}

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
	pol := defaultPermissionPolicy(toolsAllowMutators(req.AllowedTools))
	sess, err := openACPSession(ctx, a, req, pol)
	if err != nil {
		return nil, err
	}
	return runOneShot(ctx, sess, req)
}

type acpSession struct {
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	client    *acpClient
	pol       PermissionPolicy
	onEvent   EventHandler
	ev        <-chan Event
	closeEv   func()
	stopHB    func()
	stderrBuf *bytes.Buffer
	wg        sync.WaitGroup
	closeOnce sync.Once
	sendMu    sync.Mutex
	promptErr error
	ctx       context.Context
}

func openACPSession(ctx context.Context, a acpRunner, req Request, pol PermissionPolicy) (Session, error) {
	if pol == nil {
		pol = defaultPermissionPolicy(toolsAllowMutators(req.AllowedTools))
	}
	onEvent, stopHB := eventStream(req)
	args := append([]string(nil), a.args...)
	// Inject --reasoning-effort into spawn ONLY for Grok-shaped ACP peers
	// (sty_aa726901). --reasoning-effort is a Grok CLI flag, not ACP; unknown
	// peers (including Codex ACP via @agentclientprotocol/codex-acp) get effort
	// solely via session/set_config_option in handshake. Prefer insertion
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
		stopHB()
		return nil, fmt.Errorf("agentcli: acp: stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stopHB()
		return nil, fmt.Errorf("agentcli: acp: stdout: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		stopHB()
		return nil, fmt.Errorf("agentcli: acp: stderr: %w", err)
	}
	if err := cmd.Start(); err != nil {
		ev := newEvent(EventFailed)
		ev.Error = err.Error()
		emitEvent(onEvent, ev)
		stopHB()
		return nil, fmt.Errorf("agentcli: acp: start %s: %w", a.binary, err)
	}

	fanout, evCh, closeEv := fanoutEvents(onEvent)
	emitEvent(fanout, newStartEvent(cmd.Process.Pid))

	var stderrBuf bytes.Buffer
	sess := &acpSession{
		cmd: cmd, stdin: stdin, pol: pol,
		onEvent: fanout, ev: evCh, closeEv: closeEv, stopHB: stopHB,
		stderrBuf: &stderrBuf, ctx: ctx,
	}
	sess.wg.Add(1)
	go func() {
		defer sess.wg.Done()
		teeEventLines(stderr, &stderrBuf, req.Sink, "[stderr] ", true, commandAdapter{}, fanout)
	}()

	client := newACPClient(stdin, stdout, req.Sink, fanout)
	client.setMutatorsOK(toolsAllowMutators(req.AllowedTools))
	client.setPolicy(pol)
	client.setCapture(req.Capture)
	sess.client = client

	if err := sess.handshake(ctx, req); err != nil {
		sess.promptErr = err
		if closeErr := sess.Close(); closeErr != nil {
			return nil, closeErr
		}
		return nil, fmt.Errorf("agentcli: acp: %w", err)
	}
	return sess, nil
}

func (s *acpSession) Events() <-chan Event { return s.ev }

func (s *acpSession) Captured() []byte {
	if s.client == nil {
		return nil
	}
	s.client.mu.Lock()
	defer s.client.mu.Unlock()
	if s.promptErr != nil {
		return []byte(s.client.raw.String())
	}
	return []byte(s.client.capturedLocked())
}

func (s *acpSession) Send(ctx context.Context, turn Turn) error {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	var blocks []map[string]any
	if sp := strings.TrimSpace(turn.System); sp != "" {
		blocks = append(blocks, map[string]any{"type": "text", "text": sp})
	}
	if pay := strings.TrimSpace(turn.Text); pay != "" {
		blocks = append(blocks, map[string]any{"type": "text", "text": pay})
	}
	if len(blocks) == 0 {
		blocks = append(blocks, map[string]any{"type": "text", "text": "{}"})
	}
	c := s.client
	c.mu.Lock()
	sid := c.session
	c.mu.Unlock()
	if _, err := c.request(ctx, "session/prompt", map[string]any{
		"sessionId": sid,
		"prompt":    blocks,
	}); err != nil {
		s.promptErr = fmt.Errorf("session/prompt: %w", err)
		return s.promptErr
	}
	emitEvent(s.onEvent, newEvent(EventCompleted))
	return nil
}

func (s *acpSession) Cancel() error {
	if s.client != nil {
		return s.client.tryCancel()
	}
	return nil
}

func (s *acpSession) Close() error {
	var err error
	s.closeOnce.Do(func() {
		if s.client != nil {
			_ = s.client.tryCancel()
		}
		if s.stdin != nil {
			_ = s.stdin.Close()
		}
		var waitErr error
		if s.cmd != nil {
			waitErr = s.cmd.Wait()
		}
		s.wg.Wait()
		if s.stopHB != nil {
			s.stopHB()
		}
		text := s.Captured()
		if s.promptErr != nil {
			ev := newEvent(EventFailed)
			ev.Error = s.promptErr.Error()
			emitEvent(s.onEvent, ev)
			if s.stderrBuf != nil {
				if msg := strings.TrimSpace(s.stderrBuf.String()); msg != "" {
					err = fmt.Errorf("agentcli: acp: %w: %s", s.promptErr, msg)
				} else {
					err = fmt.Errorf("agentcli: acp: %w", s.promptErr)
				}
			} else {
				err = fmt.Errorf("agentcli: acp: %w", s.promptErr)
			}
		} else if waitErr != nil && s.ctx != nil && s.ctx.Err() != nil {
			ev := newEvent(EventFailed)
			ev.Error = s.ctx.Err().Error()
			emitEvent(s.onEvent, ev)
			err = fmt.Errorf("agentcli: acp: %w", s.ctx.Err())
		} else if waitErr != nil && len(bytes.TrimSpace(text)) == 0 {
			ev := newEvent(EventFailed)
			ev.Error = waitErr.Error()
			emitEvent(s.onEvent, ev)
			if s.stderrBuf != nil {
				if msg := strings.TrimSpace(s.stderrBuf.String()); msg != "" {
					err = fmt.Errorf("agentcli: acp: process: %w: %s", waitErr, msg)
				} else {
					err = fmt.Errorf("agentcli: acp: process: %w", waitErr)
				}
			} else {
				err = fmt.Errorf("agentcli: acp: process: %w", waitErr)
			}
		}
		if s.closeEv != nil {
			s.closeEv()
		}
	})
	return err
}

func (s *acpSession) handshake(ctx context.Context, req Request) error {
	c := s.client
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
		return fmt.Errorf("initialize: %w", err)
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
			return fmt.Errorf("authenticate: %w", err)
		}
	}

	sessRes, err := c.request(ctx, "session/new", map[string]any{
		"cwd":        cwd,
		"mcpServers": []any{},
	})
	if err != nil {
		return fmt.Errorf("session/new: %w", err)
	}
	var sessObj struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(sessRes, &sessObj); err != nil || sessObj.SessionID == "" {
		return fmt.Errorf("session/new: missing sessionId")
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
				_ = es
			} else {
				return fmt.Errorf("session/set_config_option model=%q rejected by peer: %w", m, err)
			}
		}
	}
	if e := strings.TrimSpace(req.Effort); e != "" {
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
	return nil
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
	pol        PermissionPolicy
	sink       io.Writer
	onEvent    EventHandler
	readerDone chan struct{}
	// capture is set from Request.Capture before handshake returns.
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

func newACPClient(w io.WriteCloser, r io.Reader, sink io.Writer, onEvent EventHandler) *acpClient {
	c := &acpClient{
		w:          w,
		pending:    map[int64]chan acpRPC{},
		sink:       sink,
		onEvent:    onEvent,
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

func (c *acpClient) setPolicy(pol PermissionPolicy) {
	c.mu.Lock()
	c.pol = pol
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
		var msg acpRPC
		if err := json.Unmarshal(line, &msg); err != nil {
			if c.sink != nil {
				_, _ = c.sink.Write([]byte(RedactSecrets(string(line)) + "\n"))
			}
			continue
		}
		if c.sink != nil && !isHiddenReasoningLine(line) {
			_, _ = c.sink.Write([]byte(RedactSecrets(string(line)) + "\n"))
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
			ToolCallID    string `json:"toolCallId"`
			Title         string `json:"title"`
			Kind          string `json:"kind"`
			Status        string `json:"status"`
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
		ev := newEvent(EventMessage)
		ev.Text = p.Update.Content.Text
		emitEvent(c.onEvent, ev)
	case "tool_call", "tool_call_update":
		// Close the open message run so post-tool answer is a new segment.
		// agent_thought_chunk deliberately does NOT close — only the tool
		// fence the wire establishes (sty_844b6ab1 AC1).
		c.mu.Lock()
		c.closeSegmentLocked()
		c.mu.Unlock()
		ev := newEvent(EventToolStart)
		if su == "tool_call_update" {
			ev.Kind = EventToolEnd
		}
		ev.Tool = p.Update.Title
		if ev.Tool == "" {
			ev.Tool = p.Update.Kind
		}
		if ev.Tool == "" {
			ev.Tool = p.Update.ToolCallID
		}
		ev.Status = p.Update.Status
		if ev.Status == "" && ev.Kind == EventToolStart {
			ev.Status = "running"
		}
		emitEvent(c.onEvent, ev)
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

	c.mu.Lock()
	pol := c.pol
	c.mu.Unlock()
	var deny bool
	if pol != nil {
		deny = !pol(PermissionRequest{ToolName: p.ToolCall.Kind, Kind: p.ToolCall.Kind}).Allow
	} else {
		deny = isMutatorToolKind(p.ToolCall.Kind) && !c.allowMutators()
	}
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
