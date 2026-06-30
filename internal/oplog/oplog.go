// Package oplog is satelle's flat-file OPERATION log — an append-only,
// plain-text record of every state-mutating CLI operation, written under
// <dataDir>/logs/operations.log (sty_be257fef).
//
// WHY a file, not the ledger: the SQLite store and the evidence ledger are the
// durable, queryable record — but they are BINARY and unqueryable at a gate. An
// isolated reviewer (satelle-code-ac-review and friends) is read-only, cannot run
// commands, and sees only the working tree, so a DB mutation (e.g. a sprint/tag
// reconciliation) is invisible to it. This log is the reviewer's READ SURFACE: a
// reviewer can Read/Grep operations.log to confirm a mutation happened, closing
// the gap that previously forced a story re-scope.
//
// DECISIONS (sty_be257fef AC#4):
//   - Tracked vs gitignored: GITIGNORED. The log is local operational evidence,
//     not project history (the ledger DB is the durable record). Tracking it would
//     produce churny diffs and merge conflicts. A reviewer runs in the SAME working
//     tree where the operation happened, so the local file is present for it. The
//     repo's .gitignore carries `.satelle/logs/`.
//   - Rotation/size: one line per mutation (tens of bytes), so growth is slow.
//     A single operations.log with no rotation is enough for the local single-dev
//     loop; size-based rotation is deferred until a real need appears.
//   - Redaction: only METADATA is logged — timestamp, actor, operation, story id,
//     and the before/after of scalar/tag fields. Story bodies and acceptance text
//     are NEVER written, so no large or sensitive content lands in the log.
//   - Relationship to the ledger: COMPLEMENTARY, not a replacement. The ledger
//     stays the authoritative, correlated event store; oplog is a flat mirror of
//     mutations specifically so a read-only reviewer has a file it can scan.
package oplog

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Logger appends operation lines to <dataDir>/logs/operations.log. Best-effort:
// every write swallows its error (logging must never break a mutation), exactly
// like the ledger append helper. A nil *Logger is a no-op, so callers need not
// guard — an unwired log simply records nothing.
type Logger struct {
	mu   sync.Mutex
	path string
}

// New returns a Logger writing under dataDir/logs/operations.log. dataDir is the
// repo's .satelle directory (the same dir that holds satelle.db).
func New(dataDir string) *Logger {
	return &Logger{path: filepath.Join(dataDir, "logs", "operations.log")}
}

// Path reports the log file path (used by tests and tooling).
func (l *Logger) Path() string {
	if l == nil {
		return ""
	}
	return l.path
}

// Append writes one tab-separated line: <rfc3339>\t<actor>\t<op>\t<storyID>\t<detail>.
// detail carries the before/after of changed fields (never bodies). A nil Logger,
// a directory-create failure, or an open failure is silently ignored.
func (l *Logger) Append(now time.Time, actor, op, storyID, detail string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(l.path), 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	// Keep each record on ONE line so a reviewer's grep returns whole operations.
	clean := func(s string) string { return strings.ReplaceAll(s, "\n", " ") }
	fmt.Fprintf(f, "%s\t%s\t%s\t%s\t%s\n",
		now.UTC().Format(time.RFC3339), clean(actor), clean(op), clean(storyID), clean(detail))
}
