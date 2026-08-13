package serve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/mirror"
)

// TestReconcileConvergesAfterDroppedPush proves AC1: a mutation whose push never
// landed leaves the mirror stale, and the reconcile loop repairs it within a
// bounded number of passes with NO operator action.
func TestReconcileConvergesAfterDroppedPush(t *testing.T) {
	ms := openTestMirror(t)
	ctx := context.Background()

	// The frame from before the dropped push: the story still at "release".
	seedStory(t, ms, "rk", "sty_1", "release")
	if got := storyStatus(t, ms, "rk", "sty_1"); got != "release" {
		t.Fatalf("seed status = %q", got)
	}

	// The repo has since moved on; only the mirror disagrees.
	var mu sync.Mutex
	var passes int
	var badPath string
	rec := &Reconciler{
		Interval: 5 * time.Millisecond,
		Targets:  func(context.Context) []string { return []string{"/repo/one"} },
		Reseed: func(ctx context.Context, repoPath string) error {
			mu.Lock()
			passes++
			if repoPath != "/repo/one" {
				badPath = repoPath
			}
			mu.Unlock()
			// What `satelle workspace add` would have delivered: the current
			// repo state, re-requested by the serving tier.
			payload := `{"id":"sty_1","status":"done"}`
			return ms.ReplaceKind(ctx, "rk", "story",
				[]mirror.ItemRow{{ID: "sty_1", Payload: payload}}, time.Now())
		},
	}
	runCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() { rec.Loop(runCtx); close(done) }()

	waitFor(t, time.Second, func() bool { return storyStatus(t, ms, "rk", "sty_1") == "done" })
	cancel()
	<-done
	mu.Lock()
	defer mu.Unlock()
	if passes == 0 {
		t.Fatal("no reconcile pass ran")
	}
	if badPath != "" {
		t.Fatalf("reseed path = %q", badPath)
	}
}

// TestReconcileSurvivesFailingRepo proves a repo that cannot be re-seeded is
// logged and never stops later passes — one broken repo must not freeze the
// repair loop for the rest.
func TestReconcileSurvivesFailingRepo(t *testing.T) {
	var mu sync.Mutex
	var logs []string
	var okCalls int
	rec := &Reconciler{
		Interval: 5 * time.Millisecond,
		Targets:  func(context.Context) []string { return []string{"/repo/bad", "/repo/good"} },
		Reseed: func(ctx context.Context, repoPath string) error {
			mu.Lock()
			defer mu.Unlock()
			if repoPath == "/repo/bad" {
				return context.DeadlineExceeded
			}
			okCalls++
			return nil
		},
		Log: func(format string, args ...any) {
			mu.Lock()
			defer mu.Unlock()
			logs = append(logs, format)
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go rec.Loop(ctx)

	waitFor(t, time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return okCalls >= 2
	})
	mu.Lock()
	defer mu.Unlock()
	var sawFailure bool
	for _, l := range logs {
		if strings.Contains(l, "mirror reconcile %s") {
			sawFailure = true
		}
	}
	if !sawFailure {
		t.Fatalf("failing repo not logged: %v", logs)
	}
}

// lineRecorder captures FORMATTED log lines. TestReconcileSurvivesFailingRepo
// deliberately keeps recording bare format strings — it only asks whether the
// failure shape was logged — but every assertion about WHICH repo and HOW MANY
// times needs the rendered line.
type lineRecorder struct {
	mu    sync.Mutex
	lines []string
}

func (l *lineRecorder) log(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func (l *lineRecorder) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.lines...)
}

// countLines returns how many recorded lines contain every one of subs.
func countLines(lines []string, subs ...string) int {
	n := 0
	for _, ln := range lines {
		all := true
		for _, s := range subs {
			if !strings.Contains(ln, s) {
				all = false
				break
			}
		}
		if all {
			n++
		}
	}
	return n
}

// TestReconcileSuppressesRepeatedIdenticalFailure proves AC1: a repo that fails
// the same way on every pass is logged when it starts failing and is NOT
// re-logged identically afterwards. On this machine the un-suppressed loop wrote
// ~1,150 identical lines a day for a repo that could not heal itself.
//
// The count of RESEED ATTEMPTS is asserted alongside the count of lines, because
// the failure this test has to rule out is not "too few lines" — it is
// suppressing the WORK instead of the log line.
func TestReconcileSuppressesRepeatedIdenticalFailure(t *testing.T) {
	var mu sync.Mutex
	var badAttempts, goodAttempts int
	rec := &lineRecorder{}
	r := &Reconciler{
		Interval: 5 * time.Millisecond,
		Targets:  func(context.Context) []string { return []string{"/repo/bad", "/repo/good"} },
		Reseed: func(ctx context.Context, repoPath string) error {
			mu.Lock()
			defer mu.Unlock()
			if repoPath == "/repo/bad" {
				badAttempts++
				return errors.New("workspace add: exit status 1: stale scaffolding")
			}
			goodAttempts++
			return nil
		},
		Log: rec.log,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go r.Loop(ctx)

	waitFor(t, time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return badAttempts >= 5
	})
	cancel()

	mu.Lock()
	attempts := badAttempts
	mu.Unlock()
	lines := rec.snapshot()

	if got := countLines(lines, "/repo/bad"); got != 1 {
		t.Errorf("a stably-failing repo was logged %d times over %d passes, want 1:\n%s",
			got, attempts, strings.Join(lines, "\n"))
	}
	// The reseed itself must NOT be suppressed — the repo is still polled.
	if attempts < 5 {
		t.Errorf("bad repo re-seeded only %d times — suppression must skip the LOG, never the work", attempts)
	}
	// A healthy repo has never been logged and must not start being.
	if got := countLines(lines, "/repo/good"); got != 0 {
		t.Errorf("a healthy repo produced %d lines, want 0:\n%s", got, strings.Join(lines, "\n"))
	}
}

// TestReconcileReportsChangedReasonAndRecovery proves AC2 and AC3: silence means
// UNCHANGED, never lost. A different failure reason and a recovery are both
// still reported, and the first failure appears without waiting for any summary
// interval.
func TestReconcileReportsChangedReasonAndRecovery(t *testing.T) {
	const (
		reasonA = "workspace add: exit status 1: stale scaffolding"
		reasonB = "workspace add: exit status 1: binary is ahead of the deployed stamp"
	)
	var mu sync.Mutex
	calls := 0
	rec := &lineRecorder{}
	r := &Reconciler{
		Interval: 5 * time.Millisecond,
		Targets:  func(context.Context) []string { return []string{"/repo/one"} },
		Reseed: func(ctx context.Context, repoPath string) error {
			mu.Lock()
			defer mu.Unlock()
			calls++
			switch {
			case calls <= 3:
				return errors.New(reasonA)
			case calls <= 6:
				return errors.New(reasonB)
			default:
				return nil
			}
		},
		Log: rec.log,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	go r.Loop(ctx)

	// AC3: the FIRST failure is visible immediately — before the reason ever
	// changes, and with no summary interval to wait out.
	waitFor(t, time.Second, func() bool {
		return countLines(rec.snapshot(), "/repo/one", reasonA) == 1
	})
	mu.Lock()
	early := calls
	mu.Unlock()
	if early > 3 {
		t.Errorf("first failure took %d passes to surface — it must be logged in the pass that produced it", early)
	}

	waitFor(t, 2*time.Second, func() bool {
		return countLines(rec.snapshot(), "recovered") == 1
	})
	cancel()

	all := rec.snapshot()
	// Lines about THIS repo. Loop's one-off "mirror reconcile every %s" startup
	// notice is not a per-repo report and is deliberately excluded.
	var lines []string
	for _, ln := range all {
		if strings.Contains(ln, "/repo/one") {
			lines = append(lines, ln)
		}
	}
	// Each transition reported exactly once: start, reason change, recovery.
	for _, c := range []struct {
		what string
		subs []string
	}{
		{"the first failure", []string{reasonA}},
		{"the changed reason", []string{reasonB}},
		{"the recovery", []string{"recovered"}},
	} {
		if got := countLines(lines, c.subs...); got != 1 {
			t.Errorf("%s was logged %d times, want exactly 1:\n%s", c.what, got, strings.Join(all, "\n"))
		}
	}
	// Three transitions over ~7+ passes: nothing else was written.
	if len(lines) != 3 {
		t.Errorf("want exactly 3 per-repo lines (start, change, recovery), got %d:\n%s",
			len(lines), strings.Join(all, "\n"))
		return
	}
	// Order matters: a recovery reported before the reason it recovered from
	// would read as a repo that healed and then broke.
	if !(strings.Contains(lines[0], reasonA) && strings.Contains(lines[1], reasonB) &&
		strings.Contains(lines[2], "recovered")) {
		t.Errorf("lines out of order:\n%s", strings.Join(lines, "\n"))
	}
	// The recovery line carries how long the repo was broken, which is the fact
	// suppression would otherwise have thrown away.
	if !strings.Contains(lines[2], "failed pass(es)") {
		t.Errorf("recovery line does not say how many passes failed: %q", lines[2])
	}
}

// TestReconcileForgetsRepoThatLeavesTargets proves the pruning half: a repo that
// drops out of Targets while failing must not emit a "recovered" line if it
// comes back healthy. It was not being polled across the gap, so the loop has
// nothing to report a recovery FROM.
func TestReconcileForgetsRepoThatLeavesTargets(t *testing.T) {
	rec := &lineRecorder{}
	r := &Reconciler{Log: rec.log, Timeout: time.Second}

	var mu sync.Mutex
	phase := 0
	r.Targets = func(context.Context) []string {
		mu.Lock()
		defer mu.Unlock()
		if phase == 1 {
			return nil // partition removed
		}
		return []string{"/repo/gone"}
	}
	r.Reseed = func(ctx context.Context, repoPath string) error {
		mu.Lock()
		defer mu.Unlock()
		if phase == 0 {
			return errors.New("workspace add: exit status 1: stale scaffolding")
		}
		return nil
	}

	ctx := context.Background()
	r.pass(ctx) // fails, logged
	mu.Lock()
	phase = 1
	mu.Unlock()
	r.pass(ctx) // not targeted — entry must be pruned
	mu.Lock()
	phase = 2
	mu.Unlock()
	r.pass(ctx) // targeted again and healthy

	lines := rec.snapshot()
	if got := countLines(lines, "recovered"); got != 0 {
		t.Errorf("a repo that left Targets reported a recovery across the gap:\n%s", strings.Join(lines, "\n"))
	}
	if got := countLines(lines, "stale scaffolding"); got != 1 {
		t.Errorf("the original failure was logged %d times, want 1:\n%s", got, strings.Join(lines, "\n"))
	}
}

// TestReconcileHonoursOffSwitch proves the loop respects the same
// SATELLE_SERVER_ENDPOINT=none off-switch as the CLI push path, so a hermetic
// environment gets no background process spawns.
func TestReconcileHonoursOffSwitch(t *testing.T) {
	t.Setenv(config.EnvServerEndpoint, "none")
	var called bool
	rec := &Reconciler{
		Interval: time.Millisecond,
		Targets:  func(context.Context) []string { return []string{"/repo/one"} },
		Reseed: func(ctx context.Context, repoPath string) error {
			called = true
			return nil
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	rec.Loop(ctx) // returns immediately when disabled
	if called {
		t.Fatal("reconcile ran with the off-switch set")
	}
}

// TestMirrorTargetsSkipsMissingPaths proves the loop is driven by partitions the
// mirror actually renders, and never re-seeds a path that no longer exists — a
// registry accumulates dead temp dirs and a background loop must not spawn a
// process per pass for each of them.
func TestMirrorTargetsSkipsMissingPaths(t *testing.T) {
	ms := openTestMirror(t)
	ctx := context.Background()
	home := t.TempDir()
	live := t.TempDir()
	seedRepoStore(t, home, live)

	seedStory(t, ms, "rk-live", "sty_1", "plan")
	seedIdentity(t, ms, "rk-live", live)
	seedStory(t, ms, "rk-dead", "sty_2", "plan")
	seedIdentity(t, ms, "rk-dead", filepath.Join(t.TempDir(), "gone"))
	seedStory(t, ms, "rk-nopath", "sty_3", "plan")

	got := mirrorTargets(ms, home)(ctx)
	if len(got) != 1 || got[0] != live {
		t.Fatalf("targets = %v, want [%s]", got, live)
	}
}

// TestMirrorTargetsSkipsRepoWithNoStoreHere proves the loop refuses to re-seed a
// partition this service's home has no database for. Without that check a
// service running under a different SATELLE_HOME would re-seed from an empty
// store and WIPE the view it is meant to repair.
func TestMirrorTargetsSkipsRepoWithNoStoreHere(t *testing.T) {
	ms := openTestMirror(t)
	repo := t.TempDir()
	seedStory(t, ms, "rk", "sty_1", "plan")
	seedIdentity(t, ms, "rk", repo)

	if got := mirrorTargets(ms, t.TempDir())(context.Background()); len(got) != 0 {
		t.Fatalf("targets = %v, want none — this home holds no store for that repo", got)
	}
}

// seedRepoStore places the home-keyed database file the service stats to decide
// whether the CLI can answer for a repo.
func seedRepoStore(t *testing.T, globalDir, repoPath string) {
	t.Helper()
	dir := filepath.Join(globalDir, config.RepoKey(repoPath))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "satelle.db"), []byte("stub"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestSatelleBinaryPrefersSibling proves the service reconciles through the CLI
// installed beside it rather than whatever happens to be on PATH — otherwise a
// dev build would repair the mirror using a different binary than it serves.
func TestSatelleBinaryPrefersSibling(t *testing.T) {
	dir := t.TempDir()
	name := "satelle"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	sibling := filepath.Join(dir, name)
	if err := os.WriteFile(sibling, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := satelleBinaryFrom(filepath.Join(dir, "satelled"))
	if err != nil {
		t.Fatal(err)
	}
	if got != sibling {
		t.Fatalf("resolved %q, want the sibling %q", got, sibling)
	}
}

func TestPassInvokesSnapshot(t *testing.T) {
	var snap []string
	r := &Reconciler{
		Targets: func(ctx context.Context) []string { return []string{"/repo"} },
		Reseed:  func(ctx context.Context, repoPath string) error { return nil },
		Snapshot: func(ctx context.Context, repoPath string) error {
			snap = append(snap, repoPath)
			return nil
		},
	}
	r.pass(context.Background())
	if len(snap) != 1 || snap[0] != "/repo" {
		t.Fatalf("snapshot = %v", snap)
	}
}

func TestReportRecordsWithoutChangingLogRules(t *testing.T) {
	var recs []string
	logs := &lineRecorder{}
	r := &Reconciler{
		Log: logs.log,
		Record: func(path string, success bool, reason string) {
			recs = append(recs, path+":"+reason)
		},
	}
	r.report("/repo", errors.New("boom"))
	r.report("/repo", errors.New("boom"))
	r.report("/repo", errors.New("boom"))
	if n := countLines(logs.snapshot(), "boom"); n != 1 {
		t.Fatalf("log lines = %d, want 1: %v", n, logs.snapshot())
	}
	if len(recs) != 3 {
		t.Fatalf("recordings = %d, want 3", len(recs))
	}
	r.report("/repo", nil)
	if n := countLines(logs.snapshot(), "recovered"); n != 1 {
		t.Fatalf("recovered logs = %d: %v", n, logs.snapshot())
	}
	if recs[len(recs)-1] != "/repo:" {
		t.Fatalf("success recording = %q", recs[len(recs)-1])
	}
}

func TestPassInvokesPush(t *testing.T) {
	var pushed []string
	r := &Reconciler{
		Targets: func(ctx context.Context) []string { return []string{"/repo"} },
		Reseed:  func(ctx context.Context, repoPath string) error { return nil },
		Push: func(ctx context.Context, repoPath string) error {
			pushed = append(pushed, repoPath)
			return nil
		},
	}
	r.pass(context.Background())
	if len(pushed) != 1 || pushed[0] != "/repo" {
		t.Fatalf("push = %v", pushed)
	}
}

// TestSatelleBinaryReportsMissing proves an unresolvable CLI is a named error —
// the reconciler disables itself with a reason rather than failing silently.
func TestSatelleBinaryReportsMissing(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if _, err := satelleBinaryFrom(filepath.Join(t.TempDir(), "satelled")); err == nil {
		t.Fatal("expected an error when no satelle can be resolved")
	}
}

func openTestMirror(t *testing.T) *mirror.Store {
	t.Helper()
	ms, err := mirror.Open(filepath.Join(t.TempDir(), "mirror.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ms.Close() })
	return ms
}

// seedStory writes a story row the way an ingested snapshot would.
func seedStory(t *testing.T, ms *mirror.Store, repoKey, id, status string) {
	t.Helper()
	ctx := context.Background()
	if _, err := ms.TouchPartition(ctx, repoKey, repoKey, time.Now()); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(map[string]any{"id": id, "status": status})
	if err != nil {
		t.Fatal(err)
	}
	if err := ms.ReplaceKind(ctx, repoKey, "story", []mirror.ItemRow{{ID: id, Payload: string(payload)}}, time.Now()); err != nil {
		t.Fatal(err)
	}
}

func seedIdentity(t *testing.T, ms *mirror.Store, repoKey, repoRoot string) {
	t.Helper()
	payload, err := json.Marshal(mirror.IdentityMeta{RepoRoot: repoRoot})
	if err != nil {
		t.Fatal(err)
	}
	if err := ms.ReplaceKind(context.Background(), repoKey, "identity",
		[]mirror.ItemRow{{ID: "meta", Payload: string(payload)}}, time.Now()); err != nil {
		t.Fatal(err)
	}
}

func storyStatus(t *testing.T, ms *mirror.Store, repoKey, id string) string {
	t.Helper()
	payload, err := ms.GetItem(context.Background(), repoKey, "story", id)
	if err != nil {
		return ""
	}
	var row struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(payload), &row); err != nil {
		t.Fatal(err)
	}
	return row.Status
}

func waitFor(t *testing.T, budget time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(budget)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("condition not met within %s", budget)
}
