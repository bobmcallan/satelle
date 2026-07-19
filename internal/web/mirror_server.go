package web

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/buildinfo"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/help"
	"github.com/bobmcallan/satelle/internal/mirror"
	"github.com/bobmcallan/satelle/internal/verb"
)

// MirrorServer is the push-fed read-only UI (sty_dbdadfa0 + epic:mirror-ui-parity).
// It never opens a per-repo runtime DB; all product state comes from the mirror store.
type MirrorServer struct {
	Handler http.Handler
	Store   *mirror.Store
	hub     *hub
}

// partitionVM is one workspace row on the mirror landing.
type partitionVM struct {
	Slug    string
	Name    string
	Path    string
	Stories int
	Tasks   int
	Docs    int
}

// mirrorWorkspaceData backs the mirror workspace landing template.
type mirrorWorkspaceData struct {
	Partitions   []partitionVM
	TotalStories int
	TopBar       topBar
	Empty        bool
}

// mirrorTmpl is a clone-source that is NEVER executed on the root value — only
// Clone()+ExecuteTemplate, so concurrent request-local basehref/footeremail
// Funcs work (html/template forbids Clone after Execute on the same *Template).
var mirrorTmpl *template.Template

func init() {
	// Full template set (shared page chrome + mirror workspace landing).
	mirrorTmpl = template.Must(template.New("web").Funcs(tmplFuncs).Parse(templatesSrc))
	template.Must(mirrorTmpl.New("mirrorWorkspace").Parse(mirrorWorkspaceSrc))
}

// NewMirror builds the RO+ingest HTTP surface over m.
func NewMirror(m *mirror.Store) *MirrorServer {
	serverStart = time.Now()
	h := newHub()
	// Do not SetChangeNotifier from web — CLI publisher + ingest doorbell only.
	verb.SetChangeNotifier(nil)

	mux := http.NewServeMux()
	mux.Handle("GET /static/", http.FileServerFS(staticFS))
	mux.HandleFunc("GET /favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		b, err := staticFS.ReadFile("static/favicon.svg")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/svg+xml")
		_, _ = w.Write(b)
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprintln(w, "ok")
	})
	mux.HandleFunc("GET /events", h.serveEvents)
	mux.HandleFunc("GET /theme", getTheme)
	// POST /theme removed (only ingest mutates). Theme persists client-side.

	ing := &mirror.IngestHandler{Store: m, OnChange: h.publish}
	ing.Mount(mux)

	s := &MirrorServer{Store: m, hub: h}

	// Workspace landing (order:3) and project surface under /r/{slug}/ (order:2).
	mux.HandleFunc("GET /{$}", s.landing)
	mux.HandleFunc("GET /workspace", s.landing) // alias; topbar Projects → /
	mux.HandleFunc("GET /r/{slug}/{$}", s.projectHome)
	mux.HandleFunc("GET /r/{slug}/fragment/stories", s.fragmentRows("workitemRows", "stories"))
	mux.HandleFunc("GET /r/{slug}/fragment/tasks", s.fragmentRows("workitemRows", "tasks"))
	mux.HandleFunc("GET /r/{slug}/fragment/docs", s.fragmentRows("docsRows", "docs"))
	mux.HandleFunc("GET /r/{slug}/fragment/story/{id}", s.itemFragment("story"))
	mux.HandleFunc("GET /r/{slug}/fragment/task/{id}", s.itemFragment("task"))
	mux.HandleFunc("GET /r/{slug}/fragment/workflow/{name}", s.workflowFragment)
	mux.HandleFunc("GET /r/{slug}/story/{id}", s.itemDetail("story"))
	mux.HandleFunc("GET /r/{slug}/task/{id}", s.itemDetail("task"))
	mux.HandleFunc("GET /r/{slug}/doc/{kind}/{name}", s.docPage)
	mux.HandleFunc("GET /r/{slug}/help", s.helpPage)
	mux.HandleFunc("GET /r/{slug}/settings", s.settingsPage)
	// No POST settings/global, oauth — RO only.

	s.Handler = mux
	return s
}

// Publish rings SSE (ingest already does this via OnChange).
func (s *MirrorServer) Publish(topic string) { s.hub.publish(topic) }

func (s *MirrorServer) resolveSlug(ctx context.Context, slug string) (string, error) {
	parts, err := s.Store.ListPartitions(ctx)
	if err != nil {
		return "", err
	}
	for _, p := range parts {
		if p.Slug == slug || p.RepoKey == slug {
			return p.RepoKey, nil
		}
	}
	return "", fmt.Errorf("unknown slug %q", slug)
}

func (s *MirrorServer) projectBase(slug string) string {
	return "/r/" + slug + "/"
}

// mirrorRender executes a named template with request-local basehref + footeremail.
func mirrorRender(w http.ResponseWriter, name, base, footerEmail string, data any) {
	t, err := mirrorTmpl.Clone()
	if err != nil {
		httpError(w, err)
		return
	}
	t = t.Funcs(template.FuncMap{
		"basehref":    func() string { return base },
		"footeremail": func() string { return footerEmail },
	})
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, name, data); err != nil {
		httpError(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = buf.WriteTo(w)
}

// landing renders the workspace over mirror partitions (order:3).
func (s *MirrorServer) landing(w http.ResponseWriter, r *http.Request) {
	parts, err := s.Store.ListPartitions(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var pvm []partitionVM
	total := 0
	for _, p := range parts {
		slug := p.Slug
		if slug == "" {
			slug = p.RepoKey
		}
		id := mirrorIdentity(r.Context(), s.Store, p.RepoKey)
		name := id.ProjectName
		if name == "" {
			name = slug
		}
		path := id.RepoRoot
		if path == "" {
			path = p.RepoKey
		}
		stories, _ := s.Store.ListItems(r.Context(), p.RepoKey, "story")
		tasks, _ := s.Store.ListItems(r.Context(), p.RepoKey, "task")
		docs, _ := s.Store.ListItems(r.Context(), p.RepoKey, "doc")
		total += len(stories)
		pvm = append(pvm, partitionVM{
			Slug: slug, Name: name, Path: path,
			Stories: len(stories), Tasks: len(tasks), Docs: len(docs),
		})
	}
	mirrorRender(w, "mirrorWorkspace", "/", "", mirrorWorkspaceData{
		Partitions:   pvm,
		TotalStories: total,
		TopBar:       mirrorTopBar("projects", ""),
		Empty:        len(pvm) == 0,
	})
}

func (s *MirrorServer) projectHome(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	repoKey, err := s.resolveSlug(r.Context(), slug)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	data, id, err := mirrorLoadPanels(r.Context(), s.Store, repoKey, slug)
	if err != nil {
		httpError(w, err)
		return
	}
	data.Projects = s.crumbProjects(r.Context(), slug)
	mirrorRender(w, "page", s.projectBase(slug), id.FooterEmail, data)
}

func (s *MirrorServer) crumbProjects(ctx context.Context, currentSlug string) []crumbProject {
	parts, err := s.Store.ListPartitions(ctx)
	if err != nil || len(parts) < 2 {
		return nil
	}
	nameCount := map[string]int{}
	type pair struct{ slug, name, path string }
	var list []pair
	for _, p := range parts {
		slug := p.Slug
		if slug == "" {
			slug = p.RepoKey
		}
		id := mirrorIdentity(ctx, s.Store, p.RepoKey)
		name := id.ProjectName
		if name == "" {
			name = slug
		}
		path := id.RepoRoot
		if path == "" {
			path = p.RepoKey
		}
		nameCount[name]++
		list = append(list, pair{slug, name, path})
	}
	out := make([]crumbProject, 0, len(list))
	for _, p := range list {
		// Template href="/{{.Slug}}/" — prefix with r/ for mirror paths.
		out = append(out, crumbProject{
			Name:      p.name,
			Slug:      "r/" + p.slug,
			Path:      p.path,
			Current:   p.slug == currentSlug,
			Ambiguous: nameCount[p.name] > 1,
		})
	}
	return out
}

func (s *MirrorServer) fragmentRows(tmplName, topic string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug := r.PathValue("slug")
		repoKey, err := s.resolveSlug(r.Context(), slug)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		data, id, err := mirrorLoadPanels(r.Context(), s.Store, repoKey, slug)
		if err != nil {
			httpError(w, err)
			return
		}
		base := s.projectBase(slug)
		var payload any
		switch topic {
		case "stories":
			payload = data.Stories
		case "tasks":
			payload = data.Tasks
		case "docs":
			payload = data.DocKinds
		default:
			http.NotFound(w, r)
			return
		}
		mirrorRender(w, tmplName, base, id.FooterEmail, payload)
	}
}

func (s *MirrorServer) itemFragment(group string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug := r.PathValue("slug")
		id := r.PathValue("id")
		repoKey, err := s.resolveSlug(r.Context(), slug)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		d, idMeta, err := mirrorLoadDetail(r.Context(), s.Store, repoKey, group, id)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		mirrorRender(w, "itemDetail", s.projectBase(slug), idMeta.FooterEmail, d)
	}
}

func (s *MirrorServer) itemDetail(group string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug := r.PathValue("slug")
		id := r.PathValue("id")
		repoKey, err := s.resolveSlug(r.Context(), slug)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		d, idMeta, err := mirrorLoadDetail(r.Context(), s.Store, repoKey, group, id)
		if err != nil {
			http.Error(w, "not found: "+id, http.StatusNotFound)
			return
		}
		d.Standalone = true
		mirrorRender(w, "detailPage", s.projectBase(slug), idMeta.FooterEmail, d)
	}
}

func (s *MirrorServer) workflowFragment(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	name := r.PathValue("name")
	repoKey, err := s.resolveSlug(r.Context(), slug)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	doc, err := findDoc(r.Context(), s.Store, repoKey, "workflows", name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	spec := parseWorkflow(doc.Body)
	bindings := map[string]bindingVM{}
	for _, st := range spec.States {
		if st.Agent != "" {
			bindings[st.Name] = bindingVM{Agent: st.Agent, Skill: st.Skill}
		}
	}
	id := mirrorIdentity(r.Context(), s.Store, repoKey)
	mirrorRender(w, "workflowDetail", s.projectBase(slug), id.FooterEmail, workflowDetailVM{
		Name:       doc.Name,
		Headline:   doc.Headline,
		Scope:      workflowScope(doc.Doc),
		AppliesTo:  frontmatterList(doc.Body, "applies_to"),
		Spec:       spec,
		Diagram:    workflowDiagram(spec, bindings),
		Body:       strings.TrimSpace(stripDocFrontmatter(doc.Body)),
		Provenance: doc.Provenance,
		Source:     doc.Source,
	})
}

func (s *MirrorServer) docPage(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	kind, name := r.PathValue("kind"), r.PathValue("name")
	repoKey, err := s.resolveSlug(r.Context(), slug)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	doc, err := findDoc(r.Context(), s.Store, repoKey, kind, name)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	id := mirrorIdentity(r.Context(), s.Store, repoKey)
	mirrorRender(w, "docPage", s.projectBase(slug), id.FooterEmail, docPageData{
		TopBar:     mirrorTopBar("", id.FooterEmail),
		Kind:       kind,
		Name:       doc.Name,
		Headline:   doc.Headline,
		HTML:       renderMarkdown(doc.Body),
		Provenance: doc.Provenance,
		Source:     doc.Source,
	})
}

func (s *MirrorServer) helpPage(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	repoKey, err := s.resolveSlug(r.Context(), slug)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	id := mirrorIdentity(r.Context(), s.Store, repoKey)
	topics := make([]helpTopic, 0)
	for _, t := range help.List() {
		topics = append(topics, helpTopic{Name: t.Name, Title: t.Title, Body: t.Body})
	}
	mirrorRender(w, "help", s.projectBase(slug), id.FooterEmail, helpPageData{
		Topics: topics,
		TopBar: mirrorTopBar("help", id.FooterEmail),
	})
}

func (s *MirrorServer) settingsPage(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	repoKey, err := s.resolveSlug(r.Context(), slug)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	data, id, err := mirrorSettingsData(r.Context(), s.Store, repoKey)
	if err != nil {
		httpError(w, err)
		return
	}
	mirrorRender(w, "settings", s.projectBase(slug), id.FooterEmail, data)
}

// MirrorDefaultPath is a convenience for tests.
func MirrorDefaultPath() string {
	return mirror.DefaultPath(config.GlobalDir())
}

// Silence unused buildinfo if footer version comes from template func only.
var _ = buildinfo.Version

const mirrorWorkspaceSrc = `{{define "mirrorWorkspace"}}<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>satelle · workspace</title>
<script>(function(){try{var t=localStorage.getItem('satelle-theme');if(t==='dark'||t==='light')document.documentElement.setAttribute('data-theme',t);}catch(e){}})();</script>
<base href="{{basehref}}">
{{template "favicon"}}
<link rel="stylesheet" href="/static/app.css">
</head>
<body data-page="projects">
{{template "topbar" .TopBar}}
<div class="wrap">
  <nav class="crumbs"><a href="/">workspace</a> <span class="sep">/</span> <span class="cur">projects</span></nav>
  <header class="app">
    <h1>workspace</h1>
    <div class="meta">{{len .Partitions}} partition{{if ne (len .Partitions) 1}}s{{end}} · push-fed mirror</div>
  </header>
  {{if .Empty}}
  <div class="empty">No partitions yet — run <code>satelle workspace add</code> from a repo with <code>[server] endpoint</code> configured to seed the mirror.</div>
  {{else}}
  <table class="panel-table">
    <thead><tr><th>Project</th><th>Path</th><th>Stories</th><th>Tasks</th><th>Docs</th></tr></thead>
    <tbody>{{range .Partitions}}
      <tr class="row">
        <td><a class="wi-title" href="/r/{{.Slug}}/">{{.Name}}</a></td>
        <td class="meta mono">{{.Path}}</td>
        <td>{{.Stories}}</td>
        <td>{{.Tasks}}</td>
        <td>{{.Docs}}</td>
      </tr>{{end}}
    </tbody>
  </table>
  {{end}}
  {{template "footer"}}
</div>
<script src="/static/app.js"></script>
</body>
</html>{{end}}`
