// Mirror reconciliation (sty_e6e467fe).
//
// The mirror is push-fed: a mutating verb flushes change events plus one
// snapshot before its store closes, fail-silent and under a bounded budget. A
// push that does not land — the service was restarting, down, or unreachable —
// was previously lost forever, because nothing ever re-requested state. A story
// that reached done through a release restart therefore left the UI showing the
// pre-restart frame indefinitely, which reads as a stuck story rather than a
// stale view.
//
// CHOSEN SHAPE: reconcile on the SERVE side. The serving tier re-requests a full
// snapshot for every partition it renders — once at startup, then every
// mirror.DefaultReconcileInterval. This repairs EVERY dropped push, including
// ones the CLI never knew failed (a killed CLI, a network blip, a service that
// was simply down), and adds nothing to the mutating-verb path.
//
// REJECTED: reconcile on the CLI side (retry, or drain before the restart
// converges). It repairs only pushes the CLI survived to notice, and it puts
// retry latency into every mutating verb — exactly what the fail-silent bounded
// budget exists to prevent.
//
// The re-request EXECS THE CLI (`satelle workspace add`) rather than reading a
// repo database directly: the serve binary is forbidden from linking
// internal/cli, internal/app and internal/store (cmd/satelled/deps_test.go),
// and internal/cli imports this package, so exec is the mechanism, not a
// preference. The CLI stays the sole reader of a per-repo database and the
// mirror stays a read-only view.
package serve

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/mirror"
)

// reconcileTimeout bounds one re-seed. A snapshot of a large repo takes a second
// or two; anything beyond this is a hung child, not a slow one.
const reconcileTimeout = 2 * time.Minute

// Reconciler re-requests a snapshot for each mirrored partition on an interval.
// Every collaborator is injectable so tests drive it without a real binary.
type Reconciler struct {
	// Interval between passes; zero means mirror.DefaultReconcileInterval.
	Interval time.Duration
	// Timeout bounds a single re-seed; zero means reconcileTimeout.
	Timeout time.Duration
	// Targets lists the repo paths to re-request, newest resolution each pass so
	// a partition added while serving is picked up without a restart.
	Targets func(ctx context.Context) []string
	// Reseed re-requests one repo's snapshot; nil means reseedViaCLI.
	Reseed func(ctx context.Context, repoPath string) error
	// Snapshot execs the CLI workstate snapshot verb; nil means snapshotViaCLI.
	// Always invoked (opt-in is the CLI's decision).
	Snapshot func(ctx context.Context, repoPath string) error
	// Push execs the CLI workstate push verb; nil means pushViaCLI.
	// Always invoked (opt-in is the CLI's decision). Periodic backstop for a
	// Notify dropped between ingest and debounce fire (sty_c526753a AC4).
	Push func(ctx context.Context, repoPath string) error
	// Log receives a line when a repo STARTS failing, when its reason CHANGES,
	// and when it recovers — never once per pass per failing repo (sty_a2162ee3).
	// nil discards.
	Log func(format string, args ...any)

	// failing remembers, per repo, the reason it last failed and how many
	// consecutive passes have produced that same reason. It exists so silence
	// can mean "unchanged": a line is written on a transition, never on a
	// repetition. Lazily allocated; only pass() touches it, and pass() is only
	// ever called from Loop's single goroutine, so there is no lock here
	// because there is no concurrency to guard.
	failing map[string]*repoFailure
}

// repoFailure is one repo's current failure streak.
type repoFailure struct {
	reason string // err.Error() of the last logged failure
	passes int    // consecutive passes that produced exactly this reason
}

func (r *Reconciler) interval() time.Duration {
	if r.Interval > 0 {
		return r.Interval
	}
	return mirror.DefaultReconcileInterval
}

func (r *Reconciler) timeout() time.Duration {
	if r.Timeout > 0 {
		return r.Timeout
	}
	return reconcileTimeout
}

func (r *Reconciler) logf(format string, args ...any) {
	if r.Log != nil {
		r.Log(format, args...)
	}
}

// Loop reconciles once immediately — the pass that repairs a push dropped by the
// restart that just happened — then every interval until ctx ends. A failing
// repo is logged and never stops the loop.
func (r *Reconciler) Loop(ctx context.Context) {
	if _, disabled, set := config.ServerEndpointEnv(); set && disabled {
		// Same off-switch the CLI push path honours: no discovery, no push, and
		// so no background re-request either (hermetic tests set this).
		r.logf("mirror reconcile disabled by %s", config.EnvServerEndpoint)
		return
	}
	r.logf("mirror reconcile every %s", r.interval())
	r.pass(ctx)
	t := time.NewTicker(r.interval())
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.pass(ctx)
		}
	}
}

// pass re-requests every target serially — a background repair must not spawn a
// process per partition at once on an operator's machine.
//
// WHY NO BACKOFF (sty_a2162ee3, AC5). Every target is re-seeded on every pass,
// including one that has failed identically for a day. That is a decision, not
// an oversight:
//
//   - This poll is the ONLY signal that a repo recovered. Nothing notifies the
//     service when an operator heals a repo, so backing off to (say) an hour
//     would delay the recovery report by up to an hour — trading a log problem
//     for a stale-view problem, which is the defect this file exists to fix.
//   - The cost being avoided is not the one that hurt. A handful of repos, one
//     bounded serial subprocess each, every interval, is negligible; the log
//     volume was the whole complaint and report() removes that outright.
//   - Backoff needs its own probe schedule, reset rule and documented bound —
//     more machinery than the problem has left.
//
// If spawn cost ever does become the binding constraint, r.failing already
// carries the consecutive-pass count a backoff would key on, so it is a
// separable follow-up rather than a rewrite.
func (r *Reconciler) pass(ctx context.Context) {
	if r.Targets == nil {
		return
	}
	reseed := r.Reseed
	if reseed == nil {
		reseed = reseedViaCLI
	}
	snapshot := r.Snapshot
	if snapshot == nil {
		if r.Reseed == nil {
			snapshot = snapshotViaCLI
		} else {
			// Tests inject Reseed; do not exec the CLI unless they also set Snapshot.
			snapshot = func(context.Context, string) error { return nil }
		}
	}
	push := r.Push
	if push == nil {
		if r.Reseed == nil {
			push = pushViaCLI
		} else {
			// Tests inject Reseed; do not exec the CLI unless they also set Push.
			push = func(context.Context, string) error { return nil }
		}
	}
	seen := map[string]bool{}
	for _, path := range r.Targets(ctx) {
		if ctx.Err() != nil {
			return
		}
		seen[path] = true
		runCtx, cancel := context.WithTimeout(ctx, r.timeout())
		err := reseed(runCtx, path)
		if serr := snapshot(runCtx, path); serr != nil {
			if err != nil {
				err = fmt.Errorf("%v; snapshot: %w", err, serr)
			} else {
				err = serr
			}
		}
		if perr := push(runCtx, path); perr != nil {
			if err != nil {
				err = fmt.Errorf("%v; push: %w", err, perr)
			} else {
				err = perr
			}
		}
		cancel()
		r.report(path, err)
	}
	// Forget repos this pass no longer targets (partition removed, directory
	// gone). Keeping the entry would emit a bogus "recovered" line across a gap
	// the repo was not even being polled over.
	for path := range r.failing {
		if !seen[path] {
			delete(r.failing, path)
		}
	}
}

// report turns one reseed outcome into at most one log line. A repo is reported
// when it STARTS failing, when the reason CHANGES, and when it recovers; a
// repetition of the same reason is counted and not written, so silence means
// unchanged rather than lost.
//
// It never touches whether the reseed ran — suppression is about the log line
// only. A repo whose line is suppressed is still re-seeded on every pass.
func (r *Reconciler) report(path string, err error) {
	prev := r.failing[path]
	if err == nil {
		if prev != nil {
			r.logf("mirror reconcile %s: recovered after %d failed pass(es)", path, prev.passes)
			delete(r.failing, path)
		}
		// Steady-state success has never been logged and must not start being.
		return
	}
	reason := err.Error()
	if prev != nil && prev.reason == reason {
		prev.passes++
		return
	}
	// First failure, or the reason changed. The format is byte-identical to the
	// line this loop has always written, so an operator's grep still matches.
	r.logf("mirror reconcile %s: %v", path, err)
	if r.failing == nil {
		r.failing = map[string]*repoFailure{}
	}
	r.failing[path] = &repoFailure{reason: reason, passes: 1}
}

// mirrorTargets returns the repo paths of every partition the mirror renders
// whose directory still exists AND whose store this machine home can answer for.
// Driving from PARTITIONS rather than the workspace registry is deliberate: the
// registry accumulates paths that were never seeded (or no longer exist), and a
// background loop must never create or seed a store on their behalf.
func mirrorTargets(ms *mirror.Store, globalDir string) func(context.Context) []string {
	return func(ctx context.Context) []string {
		details, err := ms.ListPartitionDetails(ctx)
		if err != nil {
			return nil
		}
		var out []string
		for _, d := range details {
			path := strings.TrimSpace(d.Path)
			if path == "" {
				continue
			}
			if st, err := os.Stat(path); err != nil || !st.IsDir() {
				continue
			}
			if !storeExists(globalDir, path) {
				continue
			}
			out = append(out, path)
		}
		return out
	}
}

// storeExists reports whether a repo database for path exists under this
// service's home. It STATS the file and never opens it — the CLI stays the sole
// reader. The check exists because a service whose SATELLE_HOME differs from the
// one that seeded a partition would otherwise re-seed it from an empty store and
// wipe the very view it is meant to repair.
func storeExists(globalDir, repoPath string) bool {
	candidates := []string{
		filepath.Join(globalDir, config.RepoKey(repoPath), "satelle.db"),
		filepath.Join(repoPath, ".satelle", "satelle.db"), // legacy in-repo layout
	}
	for _, c := range candidates {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return true
		}
	}
	return false
}

// reseedViaCLI runs `satelle workspace add` inside repoPath. That verb already
// builds the full snapshot and posts it to the ingest endpoint, so reconcile
// needs no second code path and no new transport.
func reseedViaCLI(ctx context.Context, repoPath string) error {
	bin, err := satelleBinary()
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, bin, "workspace", "add")
	cmd.Dir = repoPath
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s workspace add: %w: %s", bin, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// snapshotViaCLI runs `satelle sync workstate snapshot` inside repoPath.
// The CLI no-ops when work-state areas are local.
func snapshotViaCLI(ctx context.Context, repoPath string) error {
	bin, err := satelleBinary()
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, bin, "sync", "workstate", "snapshot")
	cmd.Dir = repoPath
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s sync workstate snapshot: %w: %s", bin, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// satelleBinary resolves the CLI as a sibling of this executable first — the
// service runs the installed satelled and its satelle is installed beside
// it, so a dev build must not reconcile through whatever is on PATH.
func satelleBinary() (string, error) {
	self, err := os.Executable()
	if err != nil {
		self = ""
	}
	return satelleBinaryFrom(self)
}

func satelleBinaryFrom(self string) (string, error) {
	name := "satelle"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if self != "" {
		cand := filepath.Join(filepath.Dir(self), name)
		if st, err := os.Stat(cand); err == nil && !st.IsDir() {
			return cand, nil
		}
	}
	bin, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("no satelle binary beside the service or on PATH: %w", err)
	}
	return bin, nil
}
