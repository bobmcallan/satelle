package serve

import (
	"context"
	"encoding/json"
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
	got, err := satelleBinaryFrom(filepath.Join(dir, "satelle-serve"))
	if err != nil {
		t.Fatal(err)
	}
	if got != sibling {
		t.Fatalf("resolved %q, want the sibling %q", got, sibling)
	}
}

// TestSatelleBinaryReportsMissing proves an unresolvable CLI is a named error —
// the reconciler disables itself with a reason rather than failing silently.
func TestSatelleBinaryReportsMissing(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if _, err := satelleBinaryFrom(filepath.Join(t.TempDir(), "satelle-serve")); err == nil {
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
