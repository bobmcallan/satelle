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
// internal/cli, internal/app and internal/store (cmd/satelle-serve/deps_test.go),
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
	// Log receives one line per failure (and the startup notice); nil discards.
	Log func(format string, args ...any)
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
func (r *Reconciler) pass(ctx context.Context) {
	if r.Targets == nil {
		return
	}
	reseed := r.Reseed
	if reseed == nil {
		reseed = reseedViaCLI
	}
	for _, path := range r.Targets(ctx) {
		if ctx.Err() != nil {
			return
		}
		runCtx, cancel := context.WithTimeout(ctx, r.timeout())
		err := reseed(runCtx, path)
		cancel()
		if err != nil {
			r.logf("mirror reconcile %s: %v", path, err)
		}
	}
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

// satelleBinary resolves the CLI as a sibling of this executable first — the
// service runs the installed satelle-serve and its satelle is installed beside
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
