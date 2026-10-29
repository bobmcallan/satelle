package agentcli

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestEventKindsCoverExecutionLifecycle(t *testing.T) {
	got := []EventKind{
		EventStart, EventHeartbeat, EventMessage, EventToolStart, EventToolEnd,
		EventArtifactCandidate, EventUsage, EventCompleted, EventFailed,
	}
	want := []string{
		"start", "heartbeat", "message", "tool_start", "tool_end",
		"artifact_candidate", "usage", "completed", "failed",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d kinds, want %d", len(got), len(want))
	}
	for i := range want {
		if string(got[i]) != want[i] {
			t.Errorf("kind %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestCommandAdapterRepresentativeShapesAndFallback(t *testing.T) {
	cases := []struct {
		name string
		line string
		kind EventKind
		text string
	}{
		{"claude delta", `{"type":"content_block_delta","delta":{"type":"text_delta","text":"working"}}`, EventMessage, "working"},
		{"claude tool", `{"type":"content_block_start","content_block":{"type":"tool_use","name":"Read"}}`, EventToolStart, ""},
		{"generic grok", "plain progress", EventMessage, "plain progress"},
		{"malformed", `{"type":`, EventMessage, `{"type":`},
	}
	a := commandAdapter{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			evs := a.Adapt([]byte(tc.line), false)
			if len(evs) != 1 || evs[0].Kind != tc.kind {
				t.Fatalf("events = %#v, want one %s", evs, tc.kind)
			}
			if tc.text != "" && evs[0].Text != tc.text {
				t.Errorf("text = %q, want %q", evs[0].Text, tc.text)
			}
		})
	}
}

func TestCommandAdapterDropsHiddenReasoning(t *testing.T) {
	a := commandAdapter{}
	for _, line := range []string{
		`{"type":"thinking","thinking":"SECRET_REASONING"}`,
		`{"type":"content_block_delta","delta":{"type":"thinking_delta","thinking":"SECRET_REASONING"}}`,
	} {
		if evs := a.Adapt([]byte(line), false); len(evs) != 0 {
			t.Fatalf("hidden reasoning emitted events: %#v", evs)
		}
		if !isHiddenReasoningLine([]byte(line)) {
			t.Fatalf("hidden reasoning was not recognized: %s", line)
		}
	}
}

func TestSafeTextRedactsAndBounds(t *testing.T) {
	got := SafeText("\x1b[31mstatus\x1b[0m token=super-secret " + strings.Repeat("x", 400))
	if strings.Contains(got, "super-secret") || strings.Contains(got, "\x1b") {
		t.Fatalf("unsafe text: %q", got)
	}
	if len([]rune(got)) > 241 {
		t.Fatalf("text was not bounded: %d runes", len([]rune(got)))
	}
}

func TestRunProcessEventsHeartbeatAndFinalStdout(t *testing.T) {
	r := templateFromCommand("sh -c {system}")
	var mu sync.Mutex
	var kinds []EventKind
	var sink bytes.Buffer
	out, err := r.Run(context.Background(), Request{
		SystemPrompt:      "printf 'first\\n'; sleep 0.08; printf 'final\\n'",
		Sink:              &sink,
		HeartbeatInterval: 20 * time.Millisecond,
		OnEvent: func(ev Event) {
			mu.Lock()
			kinds = append(kinds, ev.Kind)
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "first\nfinal\n" {
		t.Fatalf("stdout changed: %q", out)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(kinds) < 4 || kinds[0] != EventStart || kinds[len(kinds)-1] != EventCompleted {
		t.Fatalf("unexpected lifecycle: %v", kinds)
	}
	foundHeartbeat := false
	for _, kind := range kinds {
		if kind == EventHeartbeat {
			foundHeartbeat = true
		}
	}
	if !foundHeartbeat {
		t.Fatalf("no heartbeat in %v", kinds)
	}
}

// An input or summary event renders as a fenced block, its text kept whole.
func TestFormatEventFencesInputAndSummaryBlocks(t *testing.T) {
	at := time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC)
	long := strings.Repeat("x", 500)
	got := FormatEvent(Event{Kind: EventInput, At: at, Text: "line one\n" + long + "\n", Meta: map[string]string{EventMetaBlock: "payload"}})
	want := "2026-10-29T00:00:00Z\tinput\tinput: payload\n=== input: payload ===\nline one\n" + long + "\n=== end input: payload ===\n"
	if got != want {
		t.Errorf("input block = %q, want %q", got, want)
	}
	if got := FormatEvent(Event{Kind: EventSummary, At: at, Text: "turns: 1"}); !strings.Contains(got, "=== summary ===\nturns: 1\n=== end summary ===\n") {
		t.Errorf("summary block = %q", got)
	}
}

// The last own event of a run is found past the engine's blocks, and a line
// inside a block is never taken for one.
func TestLastEventLineSkipsTheEngineBlocks(t *testing.T) {
	at := time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC)
	var b strings.Builder
	w := func(ev Event) { ev.At = at; b.WriteString(FormatEvent(ev)) }
	w(Event{Kind: EventInput, Text: "payload", Meta: map[string]string{EventMetaBlock: "payload"}})
	w(Event{Kind: EventMessage, Text: "hi"})
	w(Event{Kind: EventSummary, Text: "2026-10-29T00:00:00Z\tcompleted\tfake\noutcome: ok"})
	if got := LastEventLine(b.String()); !strings.Contains(got, "\tmessage\thi") {
		t.Errorf("LastEventLine = %q, want the message event", got)
	}
	if got := LastEventLine(FormatEvent(Event{Kind: EventSummary, At: at, Text: "outcome: ok"})); got != "" {
		t.Errorf("a log of blocks only has no event line, got %q", got)
	}
}
