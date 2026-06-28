package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/web"
)

func init() {
	var addr string
	var port int
	var noWatch bool
	var multi bool

	serve := &cobra.Command{
		Use:   "serve",
		Short: "Run the local web server (project page) for this repo",
		Long: `serve starts the local web server rendering the repo's project page from
the local database via the same verbs the CLI uses. It also runs the directory
monitor continuously so the authored-doc index stays fresh while serving.
Press Ctrl-C to stop.`,
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := appFrom(cmd)
			if err != nil {
				return err
			}
			if port == 0 {
				port = a.Config.ResolveWebPort()
			}
			listenAddr := fmt.Sprintf("%s:%d", addr, port)

			// Signal-cancellable context shared by the watcher and the server.
			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			// Multi-project mode (opt-in via --multi): become a supervisor — one
			// child `serve` per registered project (current repo + the workspace
			// registry) on its own port, with a homepage listing them. Single-tenant
			// (global stores) is preserved per child. Plain `serve` always stays the
			// one-repo path, so existing behaviour is unchanged; with --multi and
			// only one project it transparently falls through to that path too.
			if multi {
				if roots := multiProjectRoots(a.RepoRoot); len(roots) > 1 {
					return runMultiServe(cmd, ctx, addr, port, roots)
				}
			}

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
			}

			webSrv := web.New(a)
			webSrv.StartRealtime(ctx, 0) // cross-process DB poller for CLI edits
			srv := &http.Server{Addr: listenAddr, Handler: webSrv.Handler}
			// Shut the server down when the context is cancelled (Ctrl-C).
			go func() {
				<-ctx.Done()
				shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = srv.Shutdown(shutCtx)
			}()

			fmt.Fprintf(cmd.OutOrStdout(), "satelle serving http://%s  (Ctrl-C to stop)\n", listenAddr)
			if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				return err
			}
			return nil
		},
	}
	serve.Flags().StringVar(&addr, "addr", "127.0.0.1", "bind address")
	serve.Flags().IntVar(&port, "port", 0, "listen port (default from config)")
	serve.Flags().BoolVar(&noWatch, "no-watch", false, "disable the directory monitor while serving")
	serve.Flags().BoolVar(&multi, "multi", false, "serve every registered project (homepage + one child per repo)")
	register(serve)
}

// multiProjectRoots returns the de-duplicated, absolute repo roots to serve in
// multi-project mode: the current repo first, then every registered workspace
// repo. The current repo always leads so it stays the first homepage entry.
func multiProjectRoots(currentRepo string) []string {
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
	add(currentRepo)
	if gc, err := config.LoadGlobal(); err == nil {
		for _, r := range gc.Workspace.Repos {
			add(r)
		}
	}
	return roots
}

// childProc is one supervised project: its dedicated `serve` child and the
// project metadata (slug, port) shown on the homepage.
type childProc struct {
	project web.Project
	cmd     *exec.Cmd
}

// supervisor manages one child `serve` per registered project, reconciling the
// live set against the workspace registry so `workspace add/remove` takes effect
// on a running service with no restart.
type supervisor struct {
	self        string
	ctx         context.Context
	out         io.Writer
	errw        io.Writer
	currentRepo string

	mu       sync.Mutex
	children map[string]*childProc // keyed by repo path
	order    []string              // repo paths in display order (current repo first)
	slugs    map[string]string     // path -> stable slug
	taken    map[string]bool       // assigned slugs (for de-dup)
}

func newSupervisor(ctx context.Context, out, errw io.Writer, self, currentRepo string) *supervisor {
	return &supervisor{
		self: self, ctx: ctx, out: out, errw: errw, currentRepo: currentRepo,
		children: map[string]*childProc{}, slugs: map[string]string{}, taken: map[string]bool{},
	}
}

// snapshot returns the current project set in display order — what the homepage
// renders. Safe for concurrent reads.
func (s *supervisor) snapshot() []web.Project {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]web.Project, 0, len(s.order))
	for _, p := range s.order {
		if c := s.children[p]; c != nil {
			out = append(out, c.project)
		}
	}
	return out
}

// assignSlug returns a stable slug for a repo path, deriving a de-duplicated one
// on first sight and reusing it thereafter.
func (s *supervisor) assignSlug(path string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
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

// reconcile brings the live children in line with roots: spawn a child for each
// newly-registered repo, kill the child of each de-registered one. Spawning runs
// outside the lock so the homepage stays responsive.
func (s *supervisor) reconcile(roots []string) {
	want := map[string]bool{}
	for _, p := range roots {
		want[p] = true
	}
	s.mu.Lock()
	have := make([]string, 0, len(s.children))
	for p := range s.children {
		have = append(have, p)
	}
	s.mu.Unlock()

	for _, p := range have {
		if want[p] {
			continue
		}
		s.mu.Lock()
		c := s.children[p]
		slug := s.slugs[p]
		delete(s.children, p)
		delete(s.slugs, p)
		delete(s.taken, slug)
		s.mu.Unlock()
		if c != nil && c.cmd.Process != nil {
			_ = c.cmd.Process.Kill()
		}
		fmt.Fprintf(s.out, "project removed: /%s (%s)\n", slug, p)
	}

	for _, p := range roots {
		s.mu.Lock()
		_, exists := s.children[p]
		s.mu.Unlock()
		if exists {
			continue
		}
		slug := s.assignSlug(p)
		c, err := s.spawn(p, slug)
		if err != nil {
			fmt.Fprintf(s.errw, "spawn child for %s: %v\n", p, err)
			continue
		}
		s.mu.Lock()
		s.children[p] = c
		s.mu.Unlock()
		fmt.Fprintf(s.out, "project added: /%-18s → http://127.0.0.1:%d/#stories  (%s)\n", slug, c.project.Port, p)
	}

	s.mu.Lock()
	s.order = s.order[:0]
	for _, p := range roots {
		if _, ok := s.children[p]; ok {
			s.order = append(s.order, p)
		}
	}
	s.mu.Unlock()
}

// spawn starts a single-tenant child `serve` for one repo on a fresh loopback
// port and waits for it to become healthy.
func (s *supervisor) spawn(path, slug string) (*childProc, error) {
	port, err := web.AllocPort()
	if err != nil {
		return nil, fmt.Errorf("allocate port: %w", err)
	}
	// Plain `serve` (no --multi) is always single-tenant, so a child never
	// re-supervises even if its own repo is in the registry.
	child := exec.CommandContext(s.ctx, s.self, "serve",
		"--addr", "127.0.0.1", "--port", strconv.Itoa(port))
	child.Dir = path
	child.Stdout, child.Stderr = s.errw, s.errw
	setChildDeathSignal(child) // die with the supervisor even on a hard kill
	if err := child.Start(); err != nil {
		return nil, err
	}
	if !web.WaitHealthy(s.ctx, port, 10*time.Second) {
		fmt.Fprintf(s.errw, "warning: %s (:%d) did not become healthy\n", slug, port)
	}
	return &childProc{
		cmd:     child,
		project: web.Project{Slug: slug, Name: filepath.Base(path), Path: path, Port: port},
	}, nil
}

// shutdown kills every child.
func (s *supervisor) shutdown() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.children {
		if c.cmd.Process != nil {
			_ = c.cmd.Process.Kill()
		}
	}
}

// runMultiServe supervises one child `serve` per registered project on its own
// loopback port, serves the homepage on the main port, and reconciles the child
// set against the workspace registry every few seconds so `workspace add/remove`
// is picked up live. Children are killed when the context is cancelled.
func runMultiServe(cmd *cobra.Command, ctx context.Context, addr string, port int, roots []string) error {
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve own binary: %w", err)
	}
	sup := newSupervisor(ctx, cmd.OutOrStdout(), cmd.ErrOrStderr(), self, roots[0])
	defer sup.shutdown()
	sup.reconcile(roots)

	// Watch the registry: re-derive the project set periodically and reconcile on
	// change, so a `workspace add`/`remove` lands without a restart.
	go func() {
		t := time.NewTicker(3 * time.Second)
		defer t.Stop()
		prev := strings.Join(roots, "\n")
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				next := multiProjectRoots(sup.currentRepo)
				if key := strings.Join(next, "\n"); key != prev {
					prev = key
					sup.reconcile(next)
				}
			}
		}
	}()

	listenAddr := fmt.Sprintf("%s:%d", addr, port)
	srv := &http.Server{Addr: listenAddr, Handler: web.NewMultiHandler(sup.snapshot)}
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()

	fmt.Fprintf(cmd.OutOrStdout(), "satelle serving %d projects at http://%s  (workspace add/remove is live; Ctrl-C to stop)\n", len(sup.snapshot()), listenAddr)
	for _, p := range sup.snapshot() {
		fmt.Fprintf(cmd.OutOrStdout(), "  /%-18s → http://127.0.0.1:%d/#stories  (%s)\n", p.Slug, p.Port, p.Path)
	}
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
