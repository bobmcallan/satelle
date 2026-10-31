package verb

// sty_36d6f155: the working_on row a story-set writes names the session the
// driving shell resolves, never one a dispatched performer's hooks left in the
// publish directory, and a same-status re-set from another session records it.

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

func engagementStamp(t *testing.T, _ *store.DB, id string) string {
	t.Helper()
	p, ok := LatestLocalWorkingOn(context.Background(), id)
	if !ok {
		t.Fatalf("no working_on row for %s", id)
	}
	return p.Session
}

// AC1/AC2: a performer's hooks publish their own session id on pids the CLI's
// walk reaches (and a pid file left by an earlier process may carry the CLI's own
// pid); the working_on row still names the driver's id from its shell.
func TestEngagementIsStampedWithTheDriversSessionNotAPerformers(t *testing.T) {
	clearSessionEnv(t)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "driver-id")
	h := newEngageHarness(t, "")

	config.PublishSession("performer-id") // a dispatched performer's hook, before the transition
	story := h.create("driven")
	h.set(story.ID, "in_progress")
	if got := engagementStamp(t, h.db, story.ID); got != "driver-id" {
		t.Fatalf("working_on session = %q, want the driver's session", got)
	}

	// A gate or performer run during the next transition publishes again; the
	// driver's re-set must not adopt it.
	config.PublishSession("performer-id")
	h.set(story.ID, "in_progress")
	if got := engagementStamp(t, h.db, story.ID); got != "driver-id" {
		t.Fatalf("working_on session after a performer published = %q, want the driver's session", got)
	}
}

// AC1/AC2: with no harness env, a pid file left by a process that held this pid
// before (pid reuse) must not become the working_on session.
func TestEngagementIsNotStampedFromAReusedPidFile(t *testing.T) {
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
	if got := engagementStamp(t, h.db, story.ID); got != "" {
		t.Fatalf("working_on session = %q, want none (the pid file belongs to another process)", got)
	}
}

// AC3/AC4: a same-status story-set from another session records that session;
// presence is advisory, so the newest engagement names its caller.
func TestSameStatusSetRecordsTheCallersSession(t *testing.T) {
	clearSessionEnv(t)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "ghost-id")
	h := newEngageHarness(t, "")
	story := h.create("ghosted")
	h.set(story.ID, "in_progress")
	if got := engagementStamp(t, h.db, story.ID); got != "ghost-id" {
		t.Fatalf("working_on session = %q, want ghost-id", got)
	}

	// The caller is another session now (a new CLI process: acquire exports
	// SATELLE_SESSION in-process, so a test starts each call with it cleared);
	// ghost-id is published by no live process.
	t.Setenv(config.SessionEnv, "")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "driver-id")
	config.PublishSession("driver-id")
	h.set(story.ID, "in_progress")
	if got := engagementStamp(t, h.db, story.ID); got != "driver-id" {
		t.Fatalf("working_on session = %q, want re-stamped to the caller", got)
	}

}
