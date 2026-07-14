package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/web"
)

func init() {
	var addr string
	var port int
	var noWatch bool
	var basePath string

	serve := &cobra.Command{
		Use:   "serve",
		Short: "Run the local web server — a connected-projects landing for this machine",
		Long: `serve runs the local web server. The root (/) is a connected-projects
landing: a launcher listing every registered project. Each project — including
the repo you launched from — is served by a child process under /<slug>/.
Register more with 'satelle workspace add'; the landing also links help and how
to keep the binary current. Press Ctrl-C to stop.`,
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := appFrom(cmd)
			if err != nil {
				return err
			}
			// Local mode (running as the repo-local pin) serves on a deterministic
			// per-repo port and a single project; global mode keeps the default port
			// and the workspace aggregation (sty_6b07cfb1).
			localRoot, isLocal := localPinRepoRoot()
			port = resolveServePort(port, a.Config.WebPort, localRoot, isLocal)
			listenAddr := fmt.Sprintf("%s:%d", addr, port)

			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			// Directory monitor: keep the index fresh while serving.
			if !noWatch {
				go func() {
					_ = a.Store.DocIndex.Watch(ctx, a.AuthoredDirs(), 2*time.Second,
						func(res docindex.SyncResult, err error) {
							if err != nil {
								fmt.Fprintf(cmd.ErrOrStderr(), "index: %v\n", err)
							} else if res.Indexed > 0 || res.Pruned > 0 {
								fmt.Fprintf(cmd.ErrOrStderr(), "index: +%d -%d\n", res.Indexed, res.Pruned)
							}
						})
				}()
				// Tasks are authored substrate but ingested into the workitem store
				// (not the doc index), so the doc watcher above doesn't cover them —
				// poll .satelle/tasks/ on the same cadence so a hand-edited or newly
				// created task file goes live during serve, like the other authored
				// kinds (sty_c1f9e74c). SyncTasks skips unchanged files, so this is
				// cheap and does not churn the store.
				go func() {
					t := time.NewTicker(2 * time.Second)
					defer t.Stop()
					for {
						select {
						case <-ctx.Done():
							return
						case <-t.C:
							if idx, _, err := verb.SyncTasks(ctx, a.Store.Stories, time.Now()); err != nil {
								fmt.Fprintf(cmd.ErrOrStderr(), "index: task sync: %v\n", err)
							} else if idx > 0 {
								fmt.Fprintf(cmd.ErrOrStderr(), "index: tasks +%d\n", idx)
							}
							// Keep the read-only OKF backlog reference (.satelle/stories/)
							// fresh while serving — regenerated from the store, nothing
							// reads it for decisions. Best-effort.
							if _, _, err := verb.SyncStoryBacklog(ctx, a.Store.Stories, time.Now()); err != nil {
								fmt.Fprintf(cmd.ErrOrStderr(), "index: story backlog: %v\n", err)
							}
						}
					}
				}()
			}

			// --base-path means "I'm a supervised child": render <base href> under
			// the slug (the parent proxies /<slug>/ to me) and serve ONLY this repo.
			if basePath != "" {
				web.SetBasePath(basePath)
			}
			webSrv := web.New(a)
			webSrv.StartRealtime(ctx, 0) // cross-process DB poller for CLI edits

			// Request logging → <data_dir>/logs/server.log, same rotating writer and
			// config as operations/reviewer/executor. Wrapped at the listener so each
			// serve process logs to ITS OWN repo's log and httptest servers stay
			// log-free (sty_07cec95f).
			serverLog := filepath.Join(filepath.Dir(a.DBPath), "logs", "server.log")
			logCfg := logRotation(a)

			if basePath != "" {
				return listenServe(cmd, ctx, listenAddr, web.RequestLog(webSrv.Handler, serverLog, logCfg),
					fmt.Sprintf("satelle serving http://%s under %s/  (Ctrl-C to stop)", listenAddr, strings.Trim(basePath, "/")))
			}

			// Supervisor: a connected-projects landing at / plus one child per
			// registered project (the launch repo included) proxied under /<slug>/.
			// Always adaptive — workspace add/remove reconciles live, no restart.
			self, err := os.Executable()
			if err != nil {
				return fmt.Errorf("resolve own binary: %w", err)
			}
			sup := newSupervisor(ctx, cmd.OutOrStdout(), cmd.ErrOrStderr(), self)
			sup.notify = webSrv.Publish // landing live-refresh doorbell (sty_4ea4d4df)
			defer sup.shutdown()

			// Local mode is a SINGLE project: serve only this repo and ignore the
			// workspace registry (no aggregation, no registry watcher). Global mode
			// aggregates registered repos and reconciles add/remove live.
			if isLocal {
				sup.reconcile([]string{a.RepoRoot})
			} else {
				sup.reconcile(childRoots(a.RepoRoot))
				// Watch the registry so workspace add/remove takes effect, no restart.
				go func() {
					t := time.NewTicker(3 * time.Second)
					defer t.Stop()
					prev := strings.Join(childRoots(a.RepoRoot), "\n")
					for {
						select {
						case <-ctx.Done():
							return
						case <-t.C:
							next := childRoots(a.RepoRoot)
							if key := strings.Join(next, "\n"); key != prev {
								prev = key
								sup.reconcile(next)
							}
						}
					}
				}()
			}

			sup.banner(cmd.OutOrStdout(), listenAddr)
			return listenServe(cmd, ctx, listenAddr, web.RequestLog(sup.topHandler(webSrv.Handler), serverLog, logCfg), "")
		},
	}
	serve.Flags().StringVar(&addr, "addr", "127.0.0.1", "bind address")
	serve.Flags().IntVar(&port, "port", 0, "listen port (default from config)")
	serve.Flags().BoolVar(&noWatch, "no-watch", false, "disable the directory monitor while serving")
	serve.Flags().StringVar(&basePath, "base-path", "", "mount prefix for a supervised child (internal)")
	_ = serve.Flags().MarkHidden("base-path")
	register(serve)
}

// listenServe runs an HTTP server on addr with handler until ctx is cancelled.
func listenServe(cmd *cobra.Command, ctx context.Context, addr string, handler http.Handler, banner string) error {
	srv := &http.Server{Addr: addr, Handler: handler}
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()
	if banner != "" {
		fmt.Fprintln(cmd.OutOrStdout(), banner)
	}
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// registeredRoots returns the de-duplicated, absolute repo roots: the bound repo
// first, then every registered workspace repo.
func registeredRoots(boundRepo string) []string {
	seen := map[string]bool{}
	var roots []string
	add := func(p string) {
		if p == "" {
			return
		}
		if abs, err := filepath.Abs(p); err == nil {
			p = abs
		}
		if seen[p] {
			return
		}
		seen[p] = true
		roots = append(roots, p)
	}
	add(boundRepo)
	if gc, err := config.LoadGlobal(); err == nil {
		for _, r := range gc.Workspace.Repos {
			add(r)
		}
	}
	return roots
}

// childRoots returns every repo served as a child — the launch repo first, then
// each registered workspace repo. ALL projects are children under /<slug>/; the
// supervisor itself serves only the / landing and shared chrome.
func childRoots(boundRepo string) []string {
	return registeredRoots(boundRepo)
}

// childProc is one supervised project: its child `serve`, the loopback port it
// listens on, and the prefix-stripping reverse-proxy handler in front of it.
// pid + exited support post-boot supervision (sty_5faf46f1).
type childProc struct {
	project web.Project
	cmd     *exec.Cmd
	handler http.Handler
	pid     int
	port    int
	exited  <-chan error
}

// supervisor manages one child `serve` per registered project, reconciling the
// live set against the workspace registry and supervising each child for life:
// crash → log + teardown + respawn (or park after consecutive fast failures).
// sty_5faf46f1.
type supervisor struct {
	self      string
	ctx       context.Context
	out, errw io.Writer

	mu       sync.Mutex
	children map[string]*childProc         // by repo path (live healthy only)
	bySlug   map[string]*childProc         // by url slug (request routing)
	order    []string                      // child repo paths in display order
	slugs    map[string]string             // path -> stable slug
	taken    map[string]bool               // assigned slugs (de-dup, seeded with reserved routes)
	failed   map[string]string             // path -> error (errored landing rows)
	managing map[string]context.CancelFunc // path -> supervise-loop cancel (authoritative set)
	// notify doorbells the bound server's /events hub when the served set
	// changes, so an OPEN landing tab refreshes without a manual reload.
	notify func(topic string)
}

// reservedSlugs are the bound server's own first path segments; a project slug
// must not shadow them.
var reservedSlugs = []string{
	"static", "fragment", "story", "task", "doc", "workspace", "help", "settings", "oauth",
	"events", "theme", "healthz", "projects",
}

// Supervisor lifecycle constants (no config knobs — smallest honest surface).
const (
	childHealthWindow  = 10 * time.Second
	fastFailWindow     = 5 * time.Second
	maxFastFailures    = 5
	respawnBackoffBase = 250 * time.Millisecond
	respawnBackoffCap  = 30 * time.Second
)

func newSupervisor(ctx context.Context, out, errw io.Writer, self string) *supervisor {
	taken := map[string]bool{}
	for _, r := range reservedSlugs {
		taken[r] = true
	}
	return &supervisor{
		self: self, ctx: ctx, out: out, errw: errw,
		children: map[string]*childProc{}, bySlug: map[string]*childProc{},
		slugs: map[string]string{}, taken: taken, failed: map[string]string{},
		managing: map[string]context.CancelFunc{},
	}
}

// snapshot returns every healthy served project in display order — only live
// children; dead/parked projects appear via snapshotFailed, never here.
func (s *supervisor) snapshot() []web.Project {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []web.Project
	for _, p := range s.order {
		if c := s.children[p]; c != nil {
			out = append(out, c.project)
		}
	}
	return out
}

// topHandler routes /<slug>/… to the matching child's proxy and serves the
// connected-projects landing at / (with /projects kept as a redirect for older
// links). Shared chrome — /static, /healthz, /theme, /events, /workspace — falls
// through to the supervisor's in-process handler.
func (s *supervisor) topHandler(shared http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seg := firstSegment(r.URL.Path)
		s.mu.Lock()
		c := s.bySlug[seg]
		s.mu.Unlock()
		if c != nil {
			c.handler.ServeHTTP(w, r)
			return
		}
		switch r.URL.Path {
		case "/":
			web.ProjectsPage(w, r, s.snapshot(), s.snapshotFailed())
		case "/projects":
			http.Redirect(w, r, "/", http.StatusFound)
		default:
			shared.ServeHTTP(w, r)
		}
	})
}

func firstSegment(path string) string {
	p := strings.TrimPrefix(path, "/")
	if i := strings.IndexByte(p, '/'); i >= 0 {
		return p[:i]
	}
	return p
}

// assignSlug returns a stable, de-duplicated slug for a repo path.
// Caller must hold s.mu.
func (s *supervisor) assignSlug(path string) string {
	if slug, ok := s.slugs[path]; ok {
		return slug
	}
	base := web.Slugify(filepath.Base(path))
	slug := base
	for n := 2; s.taken[slug]; n++ {
		slug = fmt.Sprintf("%s-%d", base, n)
	}
	s.slugs[path] = slug
	s.taken[slug] = true
	return slug
}

// reconcile brings supervised projects in line with roots: start a supervise
// loop for newly registered repos, cancel loops for de-registered ones.
func (s *supervisor) reconcile(roots []string) {
	want := map[string]bool{}
	for _, p := range roots {
		want[p] = true
	}

	// Stop supervisors for roots no longer wanted.
	s.mu.Lock()
	var remove []string
	for p := range s.managing {
		if !want[p] {
			remove = append(remove, p)
		}
	}
	// Also clear orphan failed rows (parked after supervise exited) not wanted.
	for p := range s.failed {
		if !want[p] {
			if _, managed := s.managing[p]; !managed {
				delete(s.failed, p)
			}
		}
	}
	s.mu.Unlock()

	for _, p := range remove {
		slug := s.slugOf(p)
		s.stopManaging(p)
		fmt.Fprintf(s.out, "project removed: /%s/ (%s)\n", slug, p)
	}

	// Start a supervise loop for each wanted root not already managed.
	started := make([]string, 0)
	for _, p := range roots {
		s.mu.Lock()
		_, managed := s.managing[p]
		slug := s.assignSlug(p)
		s.mu.Unlock()
		if managed {
			continue
		}
		// childCtx is cancelled by stopManaging / shutdown; intentional teardown
		// must not trigger respawn (sty_5faf46f1).
		childCtx, cancel := context.WithCancel(s.ctx)
		s.mu.Lock()
		s.managing[p] = cancel
		s.mu.Unlock()
		go s.supervise(childCtx, p, slug)
		started = append(started, p)
	}

	// Block until each newly-started path has a first outcome (healthy or failed)
	// so the landing/banner and callers see a settled set — same UX as the
	// pre-async spawn path. Respawn loops continue in the background after.
	if len(started) > 0 {
		s.waitSettled(started, childHealthWindow+2*time.Second)
	}
	s.rebuildOrder(roots)
}

// waitSettled blocks until every path is in children or failed, or timeout.
func (s *supervisor) waitSettled(paths []string, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		pending := 0
		for _, p := range paths {
			_, live := s.children[p]
			_, fail := s.failed[p]
			if !live && !fail {
				pending++
			}
		}
		s.mu.Unlock()
		if pending == 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// slugOf returns the assigned slug for path (empty if never assigned).
func (s *supervisor) slugOf(path string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.slugs[path]
}

// stopManaging cancels the supervise loop for path (no respawn) and clears
// registry entries. Safe if path is not managed.
func (s *supervisor) stopManaging(path string) {
	s.mu.Lock()
	cancel := s.managing[path]
	delete(s.managing, path)
	c := s.children[path]
	slug := s.slugs[path]
	delete(s.children, path)
	delete(s.bySlug, slug)
	delete(s.slugs, path)
	delete(s.taken, slug)
	delete(s.failed, path)
	// drop from order
	order := s.order[:0]
	for _, p := range s.order {
		if p != path {
			order = append(order, p)
		}
	}
	s.order = order
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if c != nil && c.cmd != nil && c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
	if s.notify != nil {
		s.notify("projects")
	}
}

// rebuildOrder sets display order from roots that currently have a live child.
func (s *supervisor) rebuildOrder(roots []string) {
	s.mu.Lock()
	s.order = s.order[:0]
	for _, p := range roots {
		if _, ok := s.children[p]; ok {
			s.order = append(s.order, p)
		}
	}
	s.mu.Unlock()
}

// supervise owns one project's lifecycle: spawn → watch → respawn or park.
// Cancel of ctx is intentional teardown (reconcile/shutdown) — no respawn.
func (s *supervisor) supervise(ctx context.Context, path, slug string) {
	defer func() {
		// Drop managing entry when the loop ends (park or intentional cancel).
		s.mu.Lock()
		// Only clear if we still own this cancel (stopManaging may have raced).
		if cancel, ok := s.managing[path]; ok {
			// If ctx is done because WE were cancelled via stopManaging, cancel is
			// already invoked; still delete the entry.
			_ = cancel
			delete(s.managing, path)
		}
		// Ensure no live route remains after the loop ends.
		if c := s.children[path]; c != nil {
			delete(s.children, path)
			delete(s.bySlug, slug)
		}
		s.mu.Unlock()
	}()

	var consecutiveFast int
	backoff := respawnBackoffBase

	for {
		if ctx.Err() != nil {
			return
		}

		c, err := s.spawn(ctx, path, slug)
		if err != nil {
			reason := err.Error()
			fmt.Fprintf(s.errw, "spawn child for /%s/ (%s): %v\n", slug, path, err)
			s.markFailed(path, reason)
			consecutiveFast++
			if consecutiveFast >= maxFastFailures {
				park := fmt.Sprintf("parked after %d consecutive spawn failures: %s", consecutiveFast, reason)
				fmt.Fprintf(s.errw, "child parked: /%s/ %s\n", slug, park)
				s.markFailed(path, park)
				return
			}
			if !s.sleepBackoff(ctx, backoff) {
				return
			}
			backoff = nextBackoff(backoff)
			continue
		}

		// Healthy: register route, clear failed, reset counters.
		s.mu.Lock()
		s.children[path] = c
		s.bySlug[slug] = c
		delete(s.failed, path)
		// Keep order consistent with registration order when possible.
		found := false
		for _, p := range s.order {
			if p == path {
				found = true
				break
			}
		}
		if !found {
			s.order = append(s.order, path)
		}
		s.mu.Unlock()
		// stderr so the journal (and tests) see pid alongside exit lines.
		fmt.Fprintf(s.errw, "child healthy: /%s/ pid=%d (:%d)\n", slug, c.pid, c.port)
		if s.notify != nil {
			s.notify("projects")
		}
		healthyAt := time.Now()
		backoff = respawnBackoffBase // reset after a healthy boot

		// Watch until exit or intentional cancel.
		select {
		case <-ctx.Done():
			// Intentional teardown — kill is best-effort; stopManaging may have done it.
			if c.cmd != nil && c.cmd.Process != nil {
				_ = c.cmd.Process.Kill()
			}
			return
		case exitErr := <-c.exited:
			status := "ok"
			if exitErr != nil {
				status = exitErr.Error()
			}
			// Intentional teardown (reconcile/shutdown) also kills the process —
			// do not treat that as a crash/respawn signal.
			if ctx.Err() != nil {
				fmt.Fprintf(s.errw, "child stopped: /%s/ pid=%d status=%s\n", slug, c.pid, status)
				return
			}
			fmt.Fprintf(s.errw, "child exited: /%s/ pid=%d status=%s\n", slug, c.pid, status)

			// Tear down the route; land in failed until respawn succeeds (AC4).
			s.mu.Lock()
			delete(s.children, path)
			delete(s.bySlug, slug)
			// trim order
			order := s.order[:0]
			for _, p := range s.order {
				if p != path {
					order = append(order, p)
				}
			}
			s.order = order
			s.failed[path] = fmt.Sprintf("exited: %s", status)
			s.mu.Unlock()
			if s.notify != nil {
				s.notify("projects")
			}

			// Fast-failure = healthy lifetime under the window.
			if time.Since(healthyAt) < fastFailWindow {
				consecutiveFast++
			} else {
				consecutiveFast = 0
			}
			if consecutiveFast >= maxFastFailures {
				park := fmt.Sprintf("parked after %d consecutive fast failures: %s", consecutiveFast, status)
				fmt.Fprintf(s.errw, "child parked: /%s/ %s\n", slug, park)
				s.markFailed(path, park)
				return
			}
			if !s.sleepBackoff(ctx, backoff) {
				return
			}
			backoff = nextBackoff(backoff)
		}
	}
}

func nextBackoff(cur time.Duration) time.Duration {
	next := cur * 2
	if next > respawnBackoffCap {
		return respawnBackoffCap
	}
	if next < respawnBackoffBase {
		return respawnBackoffBase
	}
	return next
}

func (s *supervisor) sleepBackoff(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func (s *supervisor) markFailed(path, reason string) {
	s.mu.Lock()
	// Ensure no live route for a failed path.
	if c := s.children[path]; c != nil {
		slug := c.project.Slug
		delete(s.children, path)
		delete(s.bySlug, slug)
		order := s.order[:0]
		for _, p := range s.order {
			if p != path {
				order = append(order, p)
			}
		}
		s.order = order
	}
	s.failed[path] = reason
	s.mu.Unlock()
	if s.notify != nil {
		s.notify("projects")
	}
}

// snapshotFailed lists registered projects whose child failed, died, or parked.
func (s *supervisor) snapshotFailed() []web.FailedProject {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []web.FailedProject
	for p, e := range s.failed {
		out = append(out, web.FailedProject{Name: filepath.Base(p), Path: p, Err: e})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// spawn starts a child `serve --base-path /<slug>` for one repo on a fresh
// loopback port, waits for health, and builds its prefix-stripping proxy.
// Returns an error when the child never becomes healthy (no dead proxy).
// childCtx scopes the process so intentional cancel kills only this instance.
func (s *supervisor) spawn(childCtx context.Context, path, slug string) (*childProc, error) {
	port, err := web.AllocPort()
	if err != nil {
		return nil, fmt.Errorf("allocate port: %w", err)
	}
	child := exec.CommandContext(childCtx, s.self, "serve",
		"--addr", "127.0.0.1", "--port", strconv.Itoa(port), "--base-path", "/"+slug)
	child.Dir = path
	child.Stdout, child.Stderr = s.errw, s.errw
	setChildDeathSignal(child) // die with the supervisor even on a hard kill
	if err := child.Start(); err != nil {
		return nil, err
	}
	pid := 0
	if child.Process != nil {
		pid = child.Process.Pid
	}
	// Reap the child and expose its exit for the health wait and post-boot watch.
	exited := make(chan error, 1)
	go func() { exited <- child.Wait() }()
	if !waitHealthyOrExit(childCtx, port, childHealthWindow, exited) {
		// Kill a still-running unhealthy child so we don't leak it.
		if child.Process != nil {
			_ = child.Process.Kill()
		}
		// Drain exit so we don't leave a zombie wait.
		select {
		case <-exited:
		case <-time.After(2 * time.Second):
		}
		return nil, fmt.Errorf("child /%s/ (:%d) did not become healthy in %s", slug, port, childHealthWindow)
	}
	target := &url.URL{Scheme: "http", Host: fmt.Sprintf("127.0.0.1:%d", port)}
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.FlushInterval = -1 // stream SSE through immediately
	return &childProc{
		cmd:     child,
		pid:     pid,
		port:    port,
		exited:  exited,
		project: web.Project{Slug: slug, Name: filepath.Base(path), Path: path},
		handler: http.StripPrefix("/"+slug, s.withProjects(proxy)),
	}, nil
}

// withProjects injects the live project snapshot (slug+name) into each proxied
// request as web.ProjectsHeader, so the child's breadcrumb can render the project
// switcher (sty_2bc00a9d). Read at request time so a workspace add/remove reflects
// on the next request. Set (never Add) overwrites any inbound value, so a client
// cannot spoof the project list through the supervisor.
func (s *supervisor) withProjects(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Set(web.ProjectsHeader, web.EncodeProjects(s.snapshot()))
		// The true client address for the settings-write loopback gate — Set (not
		// Add) so an inbound value cannot spoof it (same guarantee as above).
		r.Header.Set(web.ClientAddrHeader, r.RemoteAddr)
		next.ServeHTTP(w, r)
	})
}

// waitHealthyOrExit waits for the child's /healthz like web.WaitHealthy, but
// returns immediately (unhealthy) when the child process exits first — a child
// that refused to boot must not hold the supervisor for the full health window.
func waitHealthyOrExit(ctx context.Context, port int, timeout time.Duration, exited <-chan error) bool {
	hctx, cancel := context.WithCancel(ctx)
	defer cancel()
	healthy := make(chan bool, 1)
	go func() { healthy <- web.WaitHealthy(hctx, port, timeout) }()
	select {
	case ok := <-healthy:
		return ok
	case <-exited:
		return false
	case <-ctx.Done():
		return false
	}
}

// shutdown cancels every supervise loop (no respawn) and kills lingering processes.
func (s *supervisor) shutdown() {
	s.mu.Lock()
	cancels := make([]context.CancelFunc, 0, len(s.managing))
	for p, cancel := range s.managing {
		cancels = append(cancels, cancel)
		delete(s.managing, p)
	}
	for _, c := range s.children {
		if c.cmd != nil && c.cmd.Process != nil {
			_ = c.cmd.Process.Kill()
		}
	}
	s.children = map[string]*childProc{}
	s.bySlug = map[string]*childProc{}
	s.mu.Unlock()
	for _, cancel := range cancels {
		if cancel != nil {
			cancel()
		}
	}
}

// banner prints the landing URL and where each project is reachable.
func (s *supervisor) banner(out io.Writer, listenAddr string) {
	ps := s.snapshot()
	fmt.Fprintf(out, "satelle serving %d project(s) at http://%s/  (landing at /; workspace add/remove is live; Ctrl-C to stop)\n", len(ps), listenAddr)
	for _, p := range ps {
		fmt.Fprintf(out, "  %-22s %s\n", "/"+p.Slug+"/", p.Path)
	}
}
