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
	"strings"
	"sync"
)

type streamRunner struct {
	binary string
	args   []string
	how    string
}

// newStreamRunner builds a stream-json runner from a multi-token spawn command.
// Rejects empty/in-loop and bare single tokens (same ceiling as ACP). Template
// placeholders {system} and {payload} are banned — they ride the first user
// message, not argv. {tools}/{model}/{effort} remain spawn-time flags and are
// substituted via buildArgs (empty drops the preceding flag).
func newStreamRunner(command string) (Runner, error) {
	fields := strings.Fields(command)
	if len(fields) == 0 || strings.EqualFold(fields[0], "in-loop") {
		return nil, fmt.Errorf("agentcli: interface=stream requires a multi-token spawn command (e.g. DefaultClaudeStreamCommand), not in-loop/empty")
	}
	if len(fields) == 1 {
		return nil, fmt.Errorf("agentcli: interface=stream command %q: bare single token rejected — use a full stream spawn line", fields[0])
	}
	for _, tok := range fields {
		if tok == "{system}" || tok == "{payload}" {
			return nil, fmt.Errorf("agentcli: interface=stream command must not contain placeholder %s — system/payload ride the first user message, not argv", tok)
		}
	}
	return streamRunner{binary: fields[0], args: fields[1:], how: strings.Join(fields, " ")}, nil
}

func (s streamRunner) Name() string    { return s.binary }
func (s streamRunner) Command() string { return s.how }

func (s streamRunner) Open(ctx context.Context, req Request, pol PermissionPolicy) (Session, error) {
	return openStreamSession(ctx, s, req, pol)
}

func (s streamRunner) Run(ctx context.Context, req Request) ([]byte, error) {
	pol := defaultPermissionPolicy(toolsAllowMutators(req.AllowedTools))
	sess, err := openStreamSession(ctx, s, req, pol)
	if err != nil {
		return nil, err
	}
	return runOneShot(ctx, sess, req)
}

type streamSession struct {
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	stdout    io.ReadCloser
	stderr    io.ReadCloser
	wmu       sync.Mutex
	pol       PermissionPolicy
	onEvent   EventHandler
	ev        <-chan Event
	closeEv   func()
	capMu     sync.Mutex
	captured  strings.Builder
	closed    chan struct{}
	closeOnce sync.Once
}

func openStreamSession(ctx context.Context, s streamRunner, req Request, pol PermissionPolicy) (Session, error) {
	if pol == nil {
		pol = defaultPermissionPolicy(toolsAllowMutators(req.AllowedTools))
	}
	args := buildArgs(s.args, req)
	cmd := exec.CommandContext(ctx, s.binary, args...)
	if req.Dir != "" {
		cmd.Dir = req.Dir
	}
	cmd.Env = composeEnv(os.Environ(), req.Env)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("agentcli: stream: stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("agentcli: stream: stdout: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("agentcli: stream: stderr: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("agentcli: stream: start %s: %w", s.binary, err)
	}
	onEvent, ev, closeEv := fanoutEvents(req.OnEvent)
	sess := &streamSession{
		cmd: cmd, stdin: stdin, stdout: stdout, stderr: stderr,
		pol: pol, onEvent: onEvent, ev: ev, closeEv: closeEv,
		closed: make(chan struct{}),
	}
	go sess.readLoop()
	go io.Copy(io.Discard, stderr)
	emitEvent(onEvent, newEvent(EventStart))
	return sess, nil
}

func (s *streamSession) Events() <-chan Event { return s.ev }

func (s *streamSession) Captured() []byte {
	s.capMu.Lock()
	defer s.capMu.Unlock()
	return []byte(s.captured.String())
}

func (s *streamSession) Send(ctx context.Context, turn Turn) error {
	var text string
	if sp := strings.TrimSpace(turn.System); sp != "" {
		text = sp
		if t := strings.TrimSpace(turn.Text); t != "" {
			text += "\n\n" + t
		}
	} else {
		text = turn.Text
	}
	if strings.TrimSpace(text) == "" {
		text = "{}"
	}
	msg := map[string]any{
		"type": "user",
		"message": map[string]any{
			"role":    "user",
			"content": []map[string]any{{"type": "text", "text": text}},
		},
	}
	return s.writeJSON(msg)
}

func (s *streamSession) writeJSON(v any) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = s.stdin.Write(append(b, '\n'))
	return err
}

func (s *streamSession) Cancel() error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	if s.cmd != nil && s.cmd.Process != nil {
		return s.cmd.Process.Kill()
	}
	return nil
}

func (s *streamSession) Close() error {
	var err error
	s.closeOnce.Do(func() {
		_ = s.stdin.Close()
		waitErr := s.cmd.Wait()
		close(s.closed)
		if s.closeEv != nil {
			s.closeEv()
		}
		if waitErr != nil && len(bytes.TrimSpace(s.Captured())) == 0 {
			err = fmt.Errorf("agentcli: stream: process: %w", waitErr)
		}
	})
	return err
}

func (s *streamSession) readLoop() {
	sc := bufio.NewScanner(s.stdout)
	sc.Buffer(make([]byte, 0, 64*1024), 4<<20)
	for sc.Scan() {
		line := sc.Bytes()
		var raw map[string]any
		if json.Unmarshal(line, &raw) != nil {
			continue
		}
		typ, _ := raw["type"].(string)
		switch typ {
		case "assistant":
			if txt := streamAssistantText(raw); txt != "" {
				ev := newEvent(EventMessage)
				ev.Text = SafeText(txt)
				emitEvent(s.onEvent, ev)
			}
		case "control_request":
			s.handleControl(raw)
		case "result":
			if res, ok := raw["result"].(string); ok {
				s.capMu.Lock()
				s.captured.Reset()
				s.captured.WriteString(res)
				s.capMu.Unlock()
			}
			if ur := usageFromMap(raw); ur != nil {
				ev := newEvent(EventUsage)
				ev.Usage = ur
				emitEvent(s.onEvent, ev)
			}
			// Per-turn result: capture + completed so one-shot drain unblocks.
			// Keep reading so a second Send can land without respawning.
			emitEvent(s.onEvent, newEvent(EventCompleted))
		case "error":
			ev := newEvent(EventFailed)
			ev.Error = fmt.Sprint(raw["error"])
			emitEvent(s.onEvent, ev)
			return
		}
	}
	emitEvent(s.onEvent, newEvent(EventCompleted))
	if s.closeEv != nil {
		s.closeEv()
	}
}

func streamAssistantText(raw map[string]any) string {
	msg, _ := raw["message"].(map[string]any)
	if msg == nil {
		return ""
	}
	content, _ := msg["content"].([]any)
	var b strings.Builder
	for _, c := range content {
		m, _ := c.(map[string]any)
		if m["type"] == "text" {
			if t, ok := m["text"].(string); ok {
				b.WriteString(t)
			}
		}
	}
	return b.String()
}

func (s *streamSession) handleControl(raw map[string]any) {
	reqID, _ := raw["request_id"].(string)
	if reqID == "" {
		reqID, _ = raw["requestId"].(string)
	}
	req, _ := raw["request"].(map[string]any)
	subtype, _ := req["subtype"].(string)
	if subtype == "" {
		subtype, _ = req["type"].(string)
	}
	tool, _ := req["tool_name"].(string)
	if tool == "" {
		tool, _ = req["toolName"].(string)
	}
	behavior := "allow"
	if subtype == "can_use_tool" || subtype == "permission" {
		dec := s.pol(PermissionRequest{ToolName: tool, Kind: toolNameKind(tool)})
		if !dec.Allow {
			behavior = "deny"
		}
	}
	_ = s.writeJSON(map[string]any{
		"type": "control_response",
		"response": map[string]any{
			"subtype":    "success",
			"request_id": reqID,
			"response":   map[string]any{"behavior": behavior},
		},
	})
}
