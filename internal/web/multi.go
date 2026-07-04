package web

// Multi-project serving. The satelle web layer is single-tenant — verbs read
// package-global stores (internal/verb/wiring.go), so one process serves exactly
// one repo. `satelle serve` therefore serves a connected-projects LANDING at the
// root and supervises one child `serve --base-path /<slug>` per registered
// project (the launch repo included), reverse-proxying /<slug>/ to each. Adding a
// project is purely additive; the / landing lists them all.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/workspace"
)

// Project is one served project: its display name, repo path, and URL slug.
// Every project is uniform — served under its own /<slug>/.
type Project struct {
	Slug string
	Name string
	Path string
}

// ProjectsHeader carries the supervisor's live project list (slug+name only) into
// each proxied request, so a supervised child's breadcrumb can render the project
// switcher without knowing the registry or recomputing slugs (sty_2bc00a9d). The
// supervisor is the single source of the slug table (request routing), so the child
// never disagrees with it.
const ProjectsHeader = "X-Satelle-Projects"

// EncodeProjects serializes each project's slug+name (Path omitted) for
// ProjectsHeader: JSON, base64url-wrapped so a non-ASCII repo basename never yields
// a spec-hostile header value. Empty on marshal error.
func EncodeProjects(projects []Project) string {
	slim := make([]slimProject, len(projects))
	for i, p := range projects {
		slim[i] = slimProject{Slug: p.Slug, Name: p.Name}
	}
	b, err := json.Marshal(slim)
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// DecodeProjects reverses EncodeProjects. ANY error (absent, malformed, spoofed)
// yields nil, so a bad header degrades to no switcher rather than a 500.
func DecodeProjects(header string) []Project {
	if header == "" {
		return nil
	}
	b, err := base64.RawURLEncoding.DecodeString(header)
	if err != nil {
		return nil
	}
	var slim []slimProject
	if json.Unmarshal(b, &slim) != nil {
		return nil
	}
	out := make([]Project, len(slim))
	for i, s := range slim {
		out[i] = Project{Slug: s.Slug, Name: s.Name}
	}
	return out
}

type slimProject struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
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
// passes. Returns true once healthy. A child's routes are mounted at the root
// (only its rendered <base href> carries the slug), so /healthz is unprefixed.
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

// projectRow is a launcher row: a project plus its live counts and the path to
// its page.
type projectRow struct {
	Name, Slug, Path, URL string
	Stories, Tasks, Docs  int
}

// FailedProject is a registered project whose child could not be served — shown
// on the landing as an errored row rather than silently omitted (sty_4ea4d4df),
// so "added but not shown" is diagnosable.
type FailedProject struct {
	Name, Path, Err string
}

type projectsData struct {
	TopBar   topBar
	Projects []projectRow
	Failed   []FailedProject
}

// ProjectsPage renders the / landing: every served project with live counts and
// a link to its page at /<slug>/.
func ProjectsPage(w http.ResponseWriter, r *http.Request, projects []Project, failed []FailedProject) {
	paths := make([]string, 0, len(projects))
	for _, p := range projects {
		paths = append(paths, p.Path)
	}
	agg := workspace.Load(r.Context(), paths)
	counts := map[string]workspace.RepoView{}
	for _, rv := range agg.Repos {
		counts[rv.Path] = rv
	}
	data := projectsData{TopBar: topBar{Uptime: formatUptime(time.Since(serverStart))}, Failed: failed}
	for _, p := range projects {
		rv := counts[p.Path]
		url := "/" + p.Slug + "/#stories"
		data.Projects = append(data.Projects, projectRow{
			Name: p.Name, Slug: p.Slug, Path: p.Path, URL: url,
			Stories: len(rv.Stories), Tasks: len(rv.Tasks), Docs: len(rv.Docs),
		})
	}
	render(w, "projects", data)
}
