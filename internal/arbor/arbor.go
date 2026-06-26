// Package arbor is V5's structured logger. Thin wrapper over log/slog
// — JSON output for production (machine-parseable), text output for
// dev (human-readable). Every binary, middleware, and handler logs
// through here so output is consistent and parseable downstream
// (Fly.io logs, log aggregators).
//
// Mirrors V4's internal/arbor surface; rebuilt on slog instead of
// V4's custom shim because slog landed in stdlib (Go 1.21+).
package arbor

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync/atomic"
)

// Logger wraps slog with a focused V5 surface: structured fields via
// variadic key/value pairs, context-aware Info/Warn/Error/Debug, and
// .With() for inheriting a fixed set of fields.
type Logger struct {
	inner *slog.Logger
}

// New constructs a Logger from a level + handler shape. Pass jsonOut=true
// for structured output (prod, Fly); false for human-friendly text (dev).
func New(level slog.Level, jsonOut bool, out io.Writer) *Logger {
	if out == nil {
		out = os.Stderr
	}
	opts := &slog.HandlerOptions{Level: level}
	var h slog.Handler
	if jsonOut {
		h = slog.NewJSONHandler(out, opts)
	} else {
		h = slog.NewTextHandler(out, opts)
	}
	return &Logger{inner: slog.New(h)}
}

// ParseLevel parses a level string ("debug","info","warn","error") to
// the slog constant. Empty / unknown values resolve to info.
func ParseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// Debug / Info / Warn / Error are convenience wrappers. The variadic
// args follow slog's key/value convention: ("count", 3, "user", "alice").
func (l *Logger) Debug(msg string, args ...any) { l.inner.Debug(msg, args...) }
func (l *Logger) Info(msg string, args ...any)  { l.inner.Info(msg, args...) }
func (l *Logger) Warn(msg string, args ...any)  { l.inner.Warn(msg, args...) }
func (l *Logger) Error(msg string, args ...any) { l.inner.Error(msg, args...) }

// DebugCtx / InfoCtx / WarnCtx / ErrorCtx accept a context.Context so
// upstream interceptors can attach request-scoped fields (trace id,
// user id) — slog picks them up via Attr providers if installed.
func (l *Logger) DebugCtx(ctx context.Context, msg string, args ...any) {
	l.inner.DebugContext(ctx, msg, args...)
}
func (l *Logger) InfoCtx(ctx context.Context, msg string, args ...any) {
	l.inner.InfoContext(ctx, msg, args...)
}
func (l *Logger) WarnCtx(ctx context.Context, msg string, args ...any) {
	l.inner.WarnContext(ctx, msg, args...)
}
func (l *Logger) ErrorCtx(ctx context.Context, msg string, args ...any) {
	l.inner.ErrorContext(ctx, msg, args...)
}

// With returns a logger that includes the supplied fields on every
// subsequent record. Use for per-request / per-component scoping.
func (l *Logger) With(args ...any) *Logger {
	return &Logger{inner: l.inner.With(args...)}
}

// Fatal logs at error level then exits with code 1. Reserve for
// genuine boot-fatal conditions (DB unreachable, malformed config).
func (l *Logger) Fatal(msg string, args ...any) {
	l.inner.Error(msg, args...)
	os.Exit(1)
}

// Default is the process-global logger. Binaries set this once at
// startup via SetDefault; everything else uses the package-level
// helpers below.
var defaultLogger atomic.Pointer[Logger]

func init() {
	defaultLogger.Store(New(slog.LevelInfo, false, os.Stderr))
}

// SetDefault replaces the process-global logger.
func SetDefault(l *Logger) { defaultLogger.Store(l) }

// Default returns the current process-global logger.
func Default() *Logger { return defaultLogger.Load() }

// Package-level convenience helpers route through Default().
func Debug(msg string, args ...any)                         { Default().Debug(msg, args...) }
func Info(msg string, args ...any)                          { Default().Info(msg, args...) }
func Warn(msg string, args ...any)                          { Default().Warn(msg, args...) }
func Error(msg string, args ...any)                         { Default().Error(msg, args...) }
func Fatal(msg string, args ...any)                         { Default().Fatal(msg, args...) }
func DebugCtx(ctx context.Context, msg string, args ...any) { Default().DebugCtx(ctx, msg, args...) }
func InfoCtx(ctx context.Context, msg string, args ...any)  { Default().InfoCtx(ctx, msg, args...) }
func WarnCtx(ctx context.Context, msg string, args ...any)  { Default().WarnCtx(ctx, msg, args...) }
func ErrorCtx(ctx context.Context, msg string, args ...any) { Default().ErrorCtx(ctx, msg, args...) }
func With(args ...any) *Logger                              { return Default().With(args...) }

// Errf is a tiny convenience for callers building a single-line error
// message before logging. Equivalent to Error(fmt.Sprintf(...)) but
// preserves the package's structured-args convention when needed:
//
//	arbor.Errf("migrate: %w", err)
func Errf(format string, a ...any) { Error(fmt.Sprintf(format, a...)) }
