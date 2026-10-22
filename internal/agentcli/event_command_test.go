package agentcli

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestStreamToolStartCarriesCommand (sty_23ceeca2 AC4): the stream transport's
// tool_use block input becomes Event.Command for a shell tool and stays empty for
// a tool whose input holds no command.
func TestStreamToolStartCarriesCommand(t *testing.T) {
	skipWithoutPython3(t)
	peer := writeFakeStreamPeer(t)
	r, err := newStreamRunner(peer + " --output-format stream-json")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var seen []Event
	done := make(chan struct{}, 1)
	sess, err := openStreamSession(context.Background(), r.(streamRunner), Request{
		AllowedTools: "Read,Grep,Glob",
		Env:          map[string]string{"STREAM_TOOL": "1"},
		OnEvent: func(ev Event) {
			mu.Lock()
			seen = append(seen, ev)
			mu.Unlock()
			if ev.Kind == EventCompleted {
				select {
				case done <- struct{}{}:
				default:
				}
			}
		},
	}, defaultPermissionPolicy(false))
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	if err := sess.Send(context.Background(), Turn{System: "sys", Text: "one"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("no completed event")
	}
	mu.Lock()
	defer mu.Unlock()
	var got []string
	for _, ev := range seen {
		if ev.Kind == EventToolStart {
			got = append(got, ev.Tool+"="+ev.Command)
		}
	}
	if want := "Read=,Bash=false"; strings.Join(got, ",") != want {
		t.Fatalf("tool_start tool=command = %v, want %s", got, want)
	}
}

// TestACPToolCallCarriesCommand: an ACP tool_call whose rawInput holds a command
// (grok's run_terminal_command shape) reports it on the event; one without does
// not.
func TestACPToolCallCarriesCommand(t *testing.T) {
	var got []Event
	c := &acpClient{onEvent: func(ev Event) { got = append(got, ev) }}
	c.handleUpdate([]byte(`{"sessionId":"s","update":{"sessionUpdate":"tool_call","toolCallId":"c1","title":"run","kind":"execute","rawInput":{"command":"go test ./..."}}}`))
	c.handleUpdate([]byte(`{"sessionId":"s","update":{"sessionUpdate":"tool_call","toolCallId":"c2","title":"list_dir","kind":"read","rawInput":{"path":"."}}}`))
	if len(got) != 2 {
		t.Fatalf("events = %d, want 2", len(got))
	}
	if got[0].Kind != EventToolStart || got[0].Command != "go test ./..." {
		t.Errorf("shell tool_call event = %+v, want tool_start with the command", got[0])
	}
	if got[1].Command != "" {
		t.Errorf("non-shell tool_call carried command %q", got[1].Command)
	}
}

func TestToolCommandArgvAndRedaction(t *testing.T) {
	if got := toolCommand(map[string]any{"command": []any{"go", "test"}}); got != "go test" {
		t.Errorf("argv command = %q, want 'go test'", got)
	}
	if got := toolCommand(map[string]any{"command": "curl -H 'authorization: abc123'"}); strings.Contains(got, "abc123") {
		t.Errorf("command not redacted: %q", got)
	}
	if got := toolCommand("not an object"); got != "" {
		t.Errorf("non-object input = %q, want empty", got)
	}
}
