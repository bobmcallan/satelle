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
	var basePath string

	serve := &cobra.Command{
		Use:   "serve",
		Short: "Run the local web server (project page) for this repo",
		Long: `serve runs the local web server. The bound repo is always served at the root
(/). Every OTHER registered project (satelle workspace add) is served by a child
process under /<slug>/, with a /projects launcher listing them all — so adding a
project is additive and never moves the bound repo. Press Ctrl-C to stop.`,
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
			}

			// --base-path means "I'm a supervised child": render <base href> under
			// the slug (the parent proxies /<slug>/ to me) and serve ONLY this repo.
			if basePath != "" {
				web.SetBasePath(basePath)
			}
			webSrv := web.New(a)
			webSrv.StartRealtime(ctx, 0) // cross-process DB poller for CLI edits

			if basePath != "" {
				return listenServe(cmd, ctx, listenAddr, webSrv.Handler,
					fmt.Sprintf("satelle serving http://%s under %s/  (Ctrl-C to stop)", listenAddr, strings.Trim(basePath, "/")))
			}

			// Supervisor: bound repo at root + a child per OTHER registered project
			// proxied under /<slug>/, plus the /projects launcher. Always adaptive —
			// with no other projects it is simply the bound repo at root.
			self, err := os.Executable()
			if err != nil {
				return fmt.Errorf("resolve own binary: %w", err)
			}
			sup := newSupervisor(ctx, cmd.OutOrStdout(), cmd.ErrOrStderr(), self, a.RepoRoot)
			defer sup.shutdown()
			sup.reconcile(childRoots(a.RepoRoot))

			// Watch the registry so workspace add/remove takes effect with no restart.
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

			sup.banner(cmd.OutOrStdout(), listenAddr)
			return listenServe(cmd, ctx, listenAddr, sup.topHandler(webSrv.Handler), "")
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

// childRoots returns the repos served as children — every registered repo except
// the bound one (which is served in-process at the root).
func childRoots(boundRepo string) []string {
	all := registeredRoots(boundRepo)
	if len(all) <= 1 {
		return nil
	}
	return all[1:]
}

// childProc is one supervised project: its child `serve`, the loopback port it
// listens on, and the prefix-stripping reverse-proxy handler in front of it.
type childProc struct {
	project web.Project
	cmd     *exec.Cmd
	handler http.Handler
}

// supervisor manages one child `serve` per non-bound registered project,
// reconciling the live set against the workspace registry so workspace
// add/remove takes effect on a running service with no restart.
type supervisor struct {
	self      string
	ctx       context.Context
	out, errw io.Writer
	boundRepo string

	mu       sync.Mutex
	children map[string]*childProc // by repo path
	bySlug   map[string]*childProc // by url slug (request routing)
	order    []string              // child repo paths in display order
	slugs    map[string]string     // path -> stable slug
	taken    map[string]bool       // assigned slugs (de-dup, seeded with reserved routes)
}

// reservedSlugs are the bound server's own first path segments; a project slug
// must not shadow them.
var reservedSlugs = []string{
	"static", "fragment", "story", "task", "doc", "workspace", "help",
	"events", "theme", "healthz", "projects",
}

func newSupervisor(ctx context.Context, out, errw io.Writer, self, boundRepo string) *supervisor {
	taken := map[string]bool{}
	for _, r := range reservedSlugs {
		taken[r] = true
	}
	return &supervisor{
		self: self, ctx: ctx, out: out, errw: errw, boundRepo: boundRepo,
		children: map[string]*childProc{}, bySlug: map[string]*childProc{},
		slugs: map[string]string{}, taken: taken,
	}
}

// snapshot returns the bound project (Root) followed by every child, in display
// order — what the /projects launcher renders.
func (s *supervisor) snapshot() []web.Project {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []web.Project{{
		Slug: web.Slugify(filepath.Base(s.boundRepo)),
		Name: filepath.Base(s.boundRepo), Path: s.boundRepo, Root: true,
	}}
	for _, p := range s.order {
		if c := s.children[p]; c != nil {
			out = append(out, c.project)
		}
	}
	return out
}

// topHandler routes /<slug>/… to the matching child's proxy, /projects to the
// launcher, and everything else to the bound repo served at the root.
func (s *supervisor) topHandler(bound http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seg := firstSegment(r.URL.Path)
		s.mu.Lock()
		c := s.bySlug[seg]
		s.mu.Unlock()
		if c != nil {
			c.handler.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/projects" {
			web.ProjectsPage(w, r, s.snapshot())
			return
		}
		bound.ServeHTTP(w, r)
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

// reconcile brings live children in line with roots: spawn for newly-registered
// repos, kill de-registered ones. Spawning runs outside the lock.
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
		delete(s.bySlug, slug)
		delete(s.slugs, p)
		delete(s.taken, slug)
		s.mu.Unlock()
		if c != nil && c.cmd.Process != nil {
			_ = c.cmd.Process.Kill()
		}
		fmt.Fprintf(s.out, "project removed: /%s/ (%s)\n", slug, p)
	}

	for _, p := range roots {
		s.mu.Lock()
		_, exists := s.children[p]
		slug := s.assignSlug(p)
		s.mu.Unlock()
		if exists {
			continue
		}
		c, err := s.spawn(p, slug)
		if err != nil {
			fmt.Fprintf(s.errw, "spawn child for %s: %v\n", p, err)
			continue
		}
		s.mu.Lock()
		s.children[p] = c
		s.bySlug[slug] = c
		s.mu.Unlock()
		fmt.Fprintf(s.out, "project added: /%s/ (%s)\n", slug, p)
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

// spawn starts a child `serve --base-path /<slug>` for one repo on a fresh
// loopback port, waits for health, and builds its prefix-stripping proxy.
func (s *supervisor) spawn(path, slug string) (*childProc, error) {
	port, err := web.AllocPort()
	if err != nil {
		return nil, fmt.Errorf("allocate port: %w", err)
	}
	child := exec.CommandContext(s.ctx, s.self, "serve",
		"--addr", "127.0.0.1", "--port", strconv.Itoa(port), "--base-path", "/"+slug)
	child.Dir = path
	child.Stdout, child.Stderr = s.errw, s.errw
	setChildDeathSignal(child) // die with the supervisor even on a hard kill
	if err := child.Start(); err != nil {
		return nil, err
	}
	if !web.WaitHealthy(s.ctx, port, 10*time.Second) {
		fmt.Fprintf(s.errw, "warning: %s (:%d) did not become healthy\n", slug, port)
	}
	target := &url.URL{Scheme: "http", Host: fmt.Sprintf("127.0.0.1:%d", port)}
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.FlushInterval = -1 // stream SSE through immediately
	return &childProc{
		cmd:     child,
		project: web.Project{Slug: slug, Name: filepath.Base(path), Path: path},
		handler: http.StripPrefix("/"+slug, proxy),
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

// banner prints where each project is reachable.
func (s *supervisor) banner(out io.Writer, listenAddr string) {
	ps := s.snapshot()
	fmt.Fprintf(out, "satelle serving %d project(s) at http://%s  (workspace add/remove is live; Ctrl-C to stop)\n", len(ps), listenAddr)
	for _, p := range ps {
		path := "/" + p.Slug + "/"
		if p.Root {
			path = "/"
		}
		fmt.Fprintf(out, "  %-22s %s\n", path, p.Path)
	}
}
