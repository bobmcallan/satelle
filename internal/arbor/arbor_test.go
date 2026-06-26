package arbor

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestParseLevel(t *testing.T) {
	cases := map[string]slog.Level{
		"debug":   slog.LevelDebug,
		"INFO":    slog.LevelInfo,
		"warn":    slog.LevelWarn,
		"warning": slog.LevelWarn,
		"error":   slog.LevelError,
		"":        slog.LevelInfo,
		"junk":    slog.LevelInfo,
	}
	for s, want := range cases {
		if got := ParseLevel(s); got != want {
			t.Errorf("ParseLevel(%q) = %v, want %v", s, got, want)
		}
	}
}

func TestLogger_RoutesAtLevel(t *testing.T) {
	var buf bytes.Buffer
	l := New(slog.LevelInfo, false, &buf)

	l.Debug("debug-msg")
	l.Info("info-msg", "key", "value")
	l.Warn("warn-msg")
	l.Error("err-msg")

	got := buf.String()
	if strings.Contains(got, "debug-msg") {
		t.Errorf("debug emitted when level=info: %s", got)
	}
	for _, want := range []string{"info-msg", "warn-msg", "err-msg", `key=value`} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in output: %s", want, got)
		}
	}
}

func TestLogger_With_AttachesFields(t *testing.T) {
	var buf bytes.Buffer
	l := New(slog.LevelInfo, true, &buf)
	scoped := l.With("component", "test")
	scoped.Info("hello")

	got := buf.String()
	if !strings.Contains(got, `"component":"test"`) {
		t.Errorf("scoped field missing: %s", got)
	}
}

func TestDefault_Replaceable(t *testing.T) {
	original := Default()
	t.Cleanup(func() { SetDefault(original) })

	var buf bytes.Buffer
	SetDefault(New(slog.LevelDebug, false, &buf))

	Info("from-default", "k", "v")
	got := buf.String()
	if !strings.Contains(got, "from-default") || !strings.Contains(got, "k=v") {
		t.Errorf("default logger swap did not take effect: %s", got)
	}
}
