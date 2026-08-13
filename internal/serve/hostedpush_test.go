package serve

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/config"
)

func testPusher(t *testing.T, p *Pusher) (context.CancelFunc, *pushRecorder) {
	t.Helper()
	if p.Debounce == 0 {
		p.Debounce = 8 * time.Millisecond
	}
	if p.Timeout == 0 {
		p.Timeout = time.Second
	}
	rec := &pushRecorder{}
	if p.Push == nil {
		p.Push = rec.push
	}
	if p.Resolve == nil {
		p.Resolve = func(_ context.Context, repoKey string) (string, bool) {
			return "/repo/" + repoKey, true
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go p.Run(ctx)
	return cancel, rec
}

type pushRecorder struct {
	mu    sync.Mutex
	paths []string
}

func (r *pushRecorder) push(_ context.Context, repoPath string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.paths = append(r.paths, repoPath)
	return nil
}

func (r *pushRecorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.paths...)
}

func (r *pushRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.paths)
}

// TestPusherSingleNotifyPushesOnce proves AC3: one ingest notification becomes
// one push after debounce, with no reconcile interval elapsed.
func TestPusherSingleNotifyPushesOnce(t *testing.T) {
	p := &Pusher{}
	_, rec := testPusher(t, p)
	p.Notify("rk")
	waitFor(t, time.Second, func() bool { return rec.count() == 1 })
	if got := rec.snapshot(); got[0] != "/repo/rk" {
		t.Fatalf("push path = %q", got[0])
	}
	time.Sleep(20 * time.Millisecond)
	if rec.count() != 1 {
		t.Fatalf("extra pushes: %v", rec.snapshot())
	}
}

// TestPusherCoalescesRapidNotifies proves D4: N mutations for one key collapse
// into one push.
func TestPusherCoalescesRapidNotifies(t *testing.T) {
	p := &Pusher{}
	_, rec := testPusher(t, p)
	for i := 0; i < 5; i++ {
		p.Notify("rk")
	}
	waitFor(t, time.Second, func() bool { return rec.count() >= 1 })
	time.Sleep(30 * time.Millisecond)
	if rec.count() != 1 {
		t.Fatalf("pushes = %d, want 1 (coalesced): %v", rec.count(), rec.snapshot())
	}
}

// TestPusherUnresolvablePushesNothing proves a missing/unguarded key is dropped.
func TestPusherUnresolvablePushesNothing(t *testing.T) {
	p := &Pusher{
		Resolve: func(context.Context, string) (string, bool) { return "", false },
	}
	_, rec := testPusher(t, p)
	p.Notify("ghost")
	time.Sleep(40 * time.Millisecond)
	if rec.count() != 0 {
		t.Fatalf("unresolvable key pushed: %v", rec.snapshot())
	}
}

// TestPusherNotifyNeverBlocks proves Notify does not wait on a stalled worker.
func TestPusherNotifyNeverBlocks(t *testing.T) {
	started := make(chan struct{})
	block := make(chan struct{})
	p := &Pusher{
		Debounce: time.Millisecond,
		Push: func(context.Context, string) error {
			select {
			case <-started:
			default:
				close(started)
			}
			<-block
			return nil
		},
	}
	testPusher(t, p)
	p.Notify("rk")
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("worker never entered Push")
	}
	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			p.Notify("rk")
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Notify blocked while worker stalled")
	}
	close(block)
}

// TestPusherCatchUpOnRun proves AC4: Run pushes every resolved target before
// any Notify arrives.
func TestPusherCatchUpOnRun(t *testing.T) {
	p := &Pusher{
		Keys: func(context.Context) []string { return []string{"a", "b"} },
	}
	_, rec := testPusher(t, p)
	waitFor(t, time.Second, func() bool { return rec.count() == 2 })
	got := map[string]bool{}
	for _, path := range rec.snapshot() {
		got[path] = true
	}
	if !got["/repo/a"] || !got["/repo/b"] {
		t.Fatalf("catch-up pushes = %v", rec.snapshot())
	}
}

// TestPusherHonoursOffSwitch proves hermetic SATELLE_SERVER_ENDPOINT=none
// never starts the worker (risk 2).
func TestPusherHonoursOffSwitch(t *testing.T) {
	t.Setenv(config.EnvServerEndpoint, "none")
	var called bool
	p := &Pusher{
		Debounce: time.Millisecond,
		Keys:     func(context.Context) []string { return []string{"rk"} },
		Resolve:  func(context.Context, string) (string, bool) { return "/repo/rk", true },
		Push: func(context.Context, string) error {
			called = true
			return nil
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	p.Run(ctx)
	if called {
		t.Fatal("pusher ran with the off-switch set")
	}
}

// TestPusherBackoffDoesNotSpin proves AC6: K failures produce fewer than K
// pushes while the fake clock stays inside the first backoff window.
func TestPusherBackoffDoesNotSpin(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	var pushes int
	p := &Pusher{
		Debounce: 5 * time.Millisecond,
		Now:      func() time.Time { mu.Lock(); defer mu.Unlock(); return now },
		Push: func(context.Context, string) error {
			mu.Lock()
			pushes++
			mu.Unlock()
			return errors.New("hosted down")
		},
	}
	testPusher(t, p)
	p.Notify("rk")
	waitFor(t, time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return pushes >= 1
	})
	for i := 0; i < 8; i++ {
		p.Notify("rk")
		time.Sleep(15 * time.Millisecond)
	}
	mu.Lock()
	got := pushes
	mu.Unlock()
	if got != 1 {
		t.Fatalf("pushes = %d, want 1 inside backoff window", got)
	}
}

// TestPusherDirtySetBounded proves AC6: many Notifies stay O(partitions).
func TestPusherDirtySetBounded(t *testing.T) {
	p := &Pusher{}
	for i := 0; i < 200; i++ {
		p.Notify("a")
		p.Notify("b")
	}
	if n := p.dirtyCount(); n != 2 {
		t.Fatalf("dirty = %d, want 2", n)
	}
}

// TestPusherSuccessResetsBackoff proves a success after failures lets the next
// Notify push immediately.
func TestPusherSuccessResetsBackoff(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	var pushes int
	var fail bool = true
	p := &Pusher{
		Debounce: 5 * time.Millisecond,
		Now: func() time.Time {
			mu.Lock()
			defer mu.Unlock()
			return now
		},
		Push: func(context.Context, string) error {
			mu.Lock()
			defer mu.Unlock()
			pushes++
			if fail {
				return errors.New("hosted down")
			}
			return nil
		},
	}
	testPusher(t, p)
	p.Notify("rk")
	waitFor(t, time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return pushes >= 1
	})

	mu.Lock()
	now = now.Add(time.Hour)
	fail = false
	mu.Unlock()
	p.Notify("rk")
	waitFor(t, time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return pushes >= 2
	})

	// Backoff reset: another Notify without advancing time must push.
	p.Notify("rk")
	waitFor(t, time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return pushes >= 3
	})
}

// TestPusherLogsOnReasonTransition proves AC6: one log line per
// failure-reason transition, plus recovered.
func TestPusherLogsOnReasonTransition(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	var reason string = "boom-a"
	var fail bool = true
	logs := &lineRecorder{}
	p := &Pusher{
		Debounce: 5 * time.Millisecond,
		Now: func() time.Time {
			mu.Lock()
			defer mu.Unlock()
			return now
		},
		Log: logs.log,
		Push: func(context.Context, string) error {
			mu.Lock()
			defer mu.Unlock()
			if !fail {
				return nil
			}
			return errors.New(reason)
		},
	}
	testPusher(t, p)
	p.Notify("rk")
	waitFor(t, time.Second, func() bool { return countLines(logs.snapshot(), "hosted push", "boom-a") == 1 })

	// Same reason, after backoff window — no new line.
	mu.Lock()
	now = now.Add(time.Hour)
	mu.Unlock()
	p.Notify("rk")
	time.Sleep(30 * time.Millisecond)
	if n := countLines(logs.snapshot(), "boom-a"); n != 1 {
		t.Fatalf("same-reason logs = %d, want 1: %v", n, logs.snapshot())
	}

	mu.Lock()
	now = now.Add(time.Hour)
	reason = "boom-b"
	mu.Unlock()
	p.Notify("rk")
	waitFor(t, time.Second, func() bool { return countLines(logs.snapshot(), "boom-b") == 1 })

	mu.Lock()
	now = now.Add(time.Hour)
	fail = false
	mu.Unlock()
	p.Notify("rk")
	waitFor(t, time.Second, func() bool { return countLines(logs.snapshot(), "recovered") == 1 })
}

func TestPusherReportRecordsEveryOutcome(t *testing.T) {
	var recs []string
	p := &Pusher{
		Record: func(path string, success bool, reason string) {
			if success {
				recs = append(recs, path+":ok")
			} else {
				recs = append(recs, path+":"+reason)
			}
		},
	}
	p.report("rk", "/repo", errors.New("boom"))
	p.report("rk", "/repo", errors.New("boom"))
	p.report("rk", "/repo", nil)
	if len(recs) != 3 || recs[0] != "/repo:boom" || recs[2] != "/repo:ok" {
		t.Fatalf("recordings = %v", recs)
	}
}

func TestResolvePushPathGuards(t *testing.T) {
	ms := openTestMirror(t)
	home := t.TempDir()
	live := t.TempDir()
	seedRepoStore(t, home, live)
	seedStory(t, ms, "rk-live", "sty_1", "plan")
	seedIdentity(t, ms, "rk-live", live)
	seedStory(t, ms, "rk-dead", "sty_2", "plan")
	seedIdentity(t, ms, "rk-dead", filepath.Join(t.TempDir(), "gone"))
	seedStory(t, ms, "rk-nopath", "sty_3", "plan")
	seedStory(t, ms, "rk-nostore", "sty_4", "plan")
	nostore := t.TempDir()
	seedIdentity(t, ms, "rk-nostore", nostore)

	resolve := resolvePushPath(ms, home)
	ctx := context.Background()
	if path, ok := resolve(ctx, "rk-live"); !ok || path != live {
		t.Fatalf("live = (%q, %v), want (%q, true)", path, ok, live)
	}
	if _, ok := resolve(ctx, "rk-dead"); ok {
		t.Fatal("missing path must not resolve")
	}
	if _, ok := resolve(ctx, "rk-nopath"); ok {
		t.Fatal("empty path must not resolve")
	}
	if _, ok := resolve(ctx, "rk-nostore"); ok {
		t.Fatal("no store here must not resolve")
	}
	if _, ok := resolve(ctx, "rk-unknown"); ok {
		t.Fatal("unknown key must not resolve")
	}
}

func TestBackoffDelayCaps(t *testing.T) {
	if got := backoffDelay(0); got != pushBackoffBase {
		t.Fatalf("attempt 0 = %s, want %s", got, pushBackoffBase)
	}
	if got := backoffDelay(1); got != 2*pushBackoffBase {
		t.Fatalf("attempt 1 = %s", got)
	}
	if got := backoffDelay(20); got != pushBackoffCap {
		t.Fatalf("attempt 20 = %s, want cap %s", got, pushBackoffCap)
	}
}
