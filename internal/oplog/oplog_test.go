package oplog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAppendWritesReadableLines(t *testing.T) {
	dir := t.TempDir()
	l := New(dir)
	when := time.Date(2026, 6, 30, 10, 0, 0, 0, time.UTC)
	l.Append(when, "executor", "story-set", "sty_123", "tags: [a] -> [a,b]")
	l.Append(when, "executor", "story-create", "sty_456", "status: backlog")

	b, err := os.ReadFile(filepath.Join(dir, "logs", "operations.log"))
	if err != nil {
		t.Fatalf("operation log not written: %v", err)
	}
	s := string(b)
	for _, want := range []string{"sty_123", "story-set", "tags: [a] -> [a,b]", "sty_456", "2026-06-30T10:00:00Z"} {
		if !strings.Contains(s, want) {
			t.Errorf("log missing %q:\n%s", want, s)
		}
	}
	if n := strings.Count(s, "\n"); n != 2 {
		t.Errorf("want 2 one-line records, got %d:\n%s", n, s)
	}
}

func TestAppendNewlineSafe(t *testing.T) {
	dir := t.TempDir()
	l := New(dir)
	// A detail carrying a newline must stay on one line so a grep returns the whole record.
	l.Append(time.Date(2026, 6, 30, 10, 0, 0, 0, time.UTC), "executor", "story-set", "sty_1", "a\nb")
	b, _ := os.ReadFile(filepath.Join(dir, "logs", "operations.log"))
	if strings.Count(string(b), "\n") != 1 {
		t.Errorf("multi-line detail broke the one-record-per-line invariant:\n%s", b)
	}
}

func TestNilLoggerIsNoOp(t *testing.T) {
	var l *Logger
	l.Append(time.Date(2026, 6, 30, 10, 0, 0, 0, time.UTC), "a", "b", "c", "d") // must not panic
	if l.Path() != "" {
		t.Error("nil logger Path should be empty")
	}
}
