package web

// Multi-project serving. The satelle web layer is single-tenant — verbs read
// package-global stores (internal/verb/wiring.go), so one process serves exactly
// one repo. To serve several registered projects, the `satelle serve` parent
// supervises one dedicated `satelle serve --single` CHILD per project (each with
// its own store, SSE hub, and loopback port) and serves this homepage at / that
// links out to each child. Full isolation, no global-store refactor.

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/workspace"
)

// Project is one served project in multi-project mode: its display name, repo
// path, URL slug, and the loopback port its dedicated child serve listens on.
type Project struct {
	Slug string
	Name string
	Path string
	Port int
}

// Slugify turns a project name into a URL-safe slug: lowercased, with any run of
// non [a-z0-9] folded to a single '-', trimmed. Empty input yields "project".
func Slugify(name string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	s := strings.Trim(b.String(), "-")
	if s == "" {
		return "project"
	}
	return s
}

// AssignSlugs gives each project a unique slug derived from its Name, in order,
// de-duplicating collisions by appending -2, -3, … It returns a new slice.
func AssignSlugs(projects []Project) []Project {
	taken := map[string]bool{}
	out := make([]Project, len(projects))
	for i, p := range projects {
		base := Slugify(p.Name)
		slug := base
		for n := 2; taken[slug]; n++ {
			slug = fmt.Sprintf("%s-%d", base, n)
		}
		taken[slug] = true
		p.Slug = slug
		out[i] = p
	}
	return out
}

// AllocPort returns a free loopback TCP port. There is a small window between
// close and the child binding it, acceptable for local supervision.
func AllocPort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// WaitHealthy polls a child's /healthz until it answers 200 or the deadline
// passes. Returns true once healthy.
func WaitHealthy(ctx context.Context, port int, timeout time.Duration) bool {
	url := fmt.Sprintf("http://127.0.0.1:%d/healthz", port)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return false
		}
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if resp, err := http.DefaultClient.Do(req); err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return true
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

// homeProject is a homepage row: a project plus its live counts and the
// per-request absolute URL to its page.
type homeProject struct {
	Name, Slug, Path, URL string
	Stories, Tasks, Docs  int
}

type homeData struct {
	TopBar   topBar
	Projects []homeProject
}

// hostOf returns the hostname portion of an HTTP Host header (no port),
// defaulting to localhost.
func hostOf(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil && h != "" {
		return h
	}
	if host != "" {
		return host
	}
	return "localhost"
}

// NewMultiHandler builds the parent (supervisor) HTTP surface: the homepage at
// /, plus the shared static assets, theme endpoints, and a health check. Each
// project itself is served by its own child on its own port; this parent only
// lists and links to them. `snapshot` returns the current project set on each
// request, so a registry change (workspace add/remove) is reflected live.
func NewMultiHandler(snapshot func() []Project) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /static/", http.FileServerFS(staticFS))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprintln(w, "ok")
	})
	mux.HandleFunc("GET /theme", getTheme)
	mux.HandleFunc("POST /theme", setTheme)
	// A benign keep-open SSE so the shared app.js EventSource connects once and
	// shows the live dot rather than reconnect-storming a 404 (the homepage has no
	// live data of its own — the project pages do, on their own ports).
	mux.HandleFunc("GET /events", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done()
	})
	mux.HandleFunc("GET /{$}", multiHome(snapshot))
	return mux
}

// multiHome serves the multi-project homepage: every served project with its
// live counts and a link to its own per-port page. Links use the request's host
// so they work over localhost or a LAN address alike.
func multiHome(snapshot func() []Project) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projects := snapshot()
		host := hostOf(r.Host)
		paths := make([]string, 0, len(projects))
		for _, p := range projects {
			paths = append(paths, p.Path)
		}
		agg := workspace.Load(r.Context(), paths)
		counts := map[string]workspace.RepoView{}
		for _, rv := range agg.Repos {
			counts[rv.Path] = rv
		}
		data := homeData{TopBar: topBar{Uptime: formatUptime(time.Since(serverStart))}}
		for _, p := range projects {
			rv := counts[p.Path]
			data.Projects = append(data.Projects, homeProject{
				Name: p.Name, Slug: p.Slug, Path: p.Path,
				URL:     fmt.Sprintf("http://%s:%d/#stories", host, p.Port),
				Stories: len(rv.Stories), Tasks: len(rv.Tasks), Docs: len(rv.Docs),
			})
		}
		render(w, "home", data)
	}
}
