package verb

// sty_36d6f155: the seat a story-set takes is stamped with the id the driving
// shell resolves, never one a dispatched performer's hooks left in the publish
// directory, and a same-status re-set re-stamps a seat nobody live speaks for.

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/store"
)

func clearSessionEnv(t *testing.T) {
	t.Helper()
	t.Setenv(config.SessionEnv, "")
	t.Setenv("CURSOR_CONVERSATION_ID", "")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	t.Setenv("SATELLE_HOME", t.TempDir())
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
}

func seatStamp(t *testing.T, db *store.DB, id string) string {
	t.Helper()
	l, err := db.Leases.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("lease %s: %v", id, err)
	}
	return l.SessionID
}

// AC1/AC2: a performer's hooks publish their own session id on pids the CLI's
// walk reaches (and a pid file left by an earlier process may carry the CLI's own
// pid); the seat is still stamped with the driver's id from its shell.
func TestSeatIsStampedWithTheDriversSessionNotAPerformers(t *testing.T) {
	clearSessionEnv(t)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "driver-id")
	h := newEngageHarness(t, "")

	config.PublishSession("performer-id") // a dispatched performer's hook, before the transition
	story := h.create("driven")
	h.set(story.ID, "in_progress")
	if got := seatStamp(t, h.db, story.ID); got != "driver-id" {
		t.Fatalf("seat stamp = %q, want the driver's session", got)
	}

	// A gate or performer run during the next transition publishes again; the
	// driver's re-set must not adopt it.
	config.PublishSession("performer-id")
	h.set(story.ID, "in_progress")
	if got := seatStamp(t, h.db, story.ID); got != "driver-id" {
		t.Fatalf("seat stamp after a performer published = %q, want the driver's session", got)
	}
}

// AC1/AC2: with no harness env, a pid file left by a process that held this pid
// before (pid reuse) must not become the seat's stamp.
func TestSeatIsNotStampedFromAReusedPidFile(t *testing.T) {
	clearSessionEnv(t)
	if runtime.GOOS != "linux" {
		t.Skip("process identity is asserted on /proc")
	}
	h := newEngageHarness(t, "")
	dir := filepath.Join(os.Getenv("SATELLE_HOME"), "sessions")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, strconv.Itoa(os.Getpid())), []byte("performer-id\tan-earlier-process\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	story := h.create("unstamped")
	h.set(story.ID, "in_progress")
	if got := seatStamp(t, h.db, story.ID); got != "" {
		t.Fatalf("seat stamp = %q, want none (the pid file belongs to another process)", got)
	}
}

// AC3/AC4: a seat stamped with an id no live session holds is re-stamped to the
// caller by the same-status story-set; a live holder's stamp is left alone.
func TestSameStatusSetRestampsAGhostSeat(t *testing.T) {
	clearSessionEnv(t)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "ghost-id")
	h := newEngageHarness(t, "")
	story := h.create("ghosted")
	h.set(story.ID, "in_progress")
	if got := seatStamp(t, h.db, story.ID); got != "ghost-id" {
		t.Fatalf("seat stamp = %q, want ghost-id", got)
	}

	// The caller is another session now (a new CLI process: acquire exports
	// SATELLE_SESSION in-process, so a test starts each call with it cleared);
	// ghost-id is published by no live process.
	t.Setenv(config.SessionEnv, "")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "driver-id")
	config.PublishSession("driver-id")
	h.set(story.ID, "in_progress")
	if got := seatStamp(t, h.db, story.ID); got != "driver-id" {
		t.Fatalf("seat stamp = %q, want re-stamped to the caller", got)
	}

	// driver-id is published by this live process: a third caller does not take it.
	t.Setenv(config.SessionEnv, "")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "thief-id")
	h.set(story.ID, "in_progress")
	if got := seatStamp(t, h.db, story.ID); got != "driver-id" {
		t.Fatalf("seat stamp = %q, a live holder's stamp must not be overwritten", got)
	}
}
