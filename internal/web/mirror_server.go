package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/buildinfo"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/mirror"
	"github.com/bobmcallan/satelle/internal/verb"
)

// MirrorServer is the push-fed read-only UI (sty_dbdadfa0). It never opens a
// per-repo runtime DB; all product state comes from the mirror store.
type MirrorServer struct {
	Handler http.Handler
	Store   *mirror.Store
	hub     *hub
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
	// POST /theme removed (AC4: only ingest mutates).

	ing := &mirror.IngestHandler{Store: m, OnChange: h.publish}
	ing.Mount(mux)

	s := &MirrorServer{Store: m, hub: h}
	// Use /r/{slug}/ prefix so it cannot conflict with /static/ (Go ServeMux).
	mux.HandleFunc("GET /{$}", s.landing)
	mux.HandleFunc("GET /r/{slug}/{$}", s.projectHome)
	mux.HandleFunc("GET /r/{slug}/story/{id}", s.itemJSON("story"))
	mux.HandleFunc("GET /r/{slug}/task/{id}", s.itemJSON("task"))
	mux.HandleFunc("GET /r/{slug}/fragment/stories", s.fragmentList("story"))
	mux.HandleFunc("GET /r/{slug}/fragment/tasks", s.fragmentList("task"))

	s.Handler = mux
	return s
}

// Publish rings SSE (ingest already does this via OnChange).
func (s *MirrorServer) Publish(topic string) { s.hub.publish(topic) }

func (s *MirrorServer) landing(w http.ResponseWriter, r *http.Request) {
	parts, err := s.Store.ListPartitions(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>satelle · workspace</title>
<link rel="stylesheet" href="/static/app.css"></head><body data-page="projects">
<header class="topbar"><div class="topbar-inner"><span class="brand-word">satelle</span>
<span class="meta">push-fed mirror · %d partition(s)</span></div></header>
<div class="wrap"><h1>workspace</h1><p class="meta">Read-only UI fed by CLI push. Reconcile a repo to populate its partition.</p>
<ul>`, len(parts))
	for _, p := range parts {
		slug := p.Slug
		if slug == "" {
			slug = p.RepoKey
		}
		fmt.Fprintf(w, `<li><a href="/r/%s/">%s</a> <code>%s</code> seq=%d</li>`, slug, slug, p.RepoKey, p.Seq)
	}
	fmt.Fprintf(w, `</ul></div>
<footer class="site-footer"><span class="footer-version">satelle %s</span></footer>
<script>
// SSE: any ingest/publish reloads so the push-fed view stays current (sty_dbdadfa0 AC3).
(function(){try{var es=new EventSource("/events");es.addEventListener("trigger",function(){location.reload()});}catch(e){}})();
</script>
</body></html>`, buildinfo.Version)
}

func (s *MirrorServer) projectHome(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	repoKey, err := s.resolveSlug(r.Context(), slug)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	stories, _ := s.Store.ListItems(r.Context(), repoKey, "story")
	tasks, _ := s.Store.ListItems(r.Context(), repoKey, "task")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>satelle · %s</title>
<link rel="stylesheet" href="/static/app.css"></head><body>
<header class="topbar"><div class="topbar-inner"><a href="/">workspace</a> / %s</div></header>
<div class="wrap"><h1>%s</h1>
<p class="meta">repo_key=%s · stories=%d · tasks=%d · push-fed</p>
<h2>Stories</h2><ul>`, slug, slug, slug, repoKey, len(stories), len(tasks))
	for _, it := range stories {
		var m map[string]any
		_ = json.Unmarshal([]byte(it.Payload), &m)
		title, _ := m["title"].(string)
		status, _ := m["status"].(string)
		fmt.Fprintf(w, `<li><a href="/r/%s/story/%s">%s</a> <span class="meta">%s</span></li>`, slug, it.ID, esc(title), esc(status))
	}
	fmt.Fprintf(w, `</ul><h2>Tasks</h2><ul>`)
	for _, it := range tasks {
		var m map[string]any
		_ = json.Unmarshal([]byte(it.Payload), &m)
		title, _ := m["title"].(string)
		fmt.Fprintf(w, `<li><a href="/r/%s/task/%s">%s</a></li>`, slug, it.ID, esc(title))
	}
	fmt.Fprintf(w, `</ul></div>
<footer class="site-footer"><span class="footer-version">satelle %s</span></footer>
<script>
(function(){try{var es=new EventSource("/events");es.addEventListener("trigger",function(){location.reload()});}catch(e){}})();
</script>
</body></html>`, buildinfo.Version)
}

func (s *MirrorServer) itemJSON(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug := r.PathValue("slug")
		id := r.PathValue("id")
		repoKey, err := s.resolveSlug(r.Context(), slug)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		payload, err := s.Store.GetItem(r.Context(), repoKey, kind, id)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		// Prefer HTML detail; Accept application/json returns raw payload.
		if strings.Contains(r.Header.Get("Accept"), "application/json") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(payload))
			return
		}
		var m map[string]any
		_ = json.Unmarshal([]byte(payload), &m)
		title, _ := m["title"].(string)
		body, _ := m["body"].(string)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<!doctype html><html><head><meta charset="utf-8"><title>%s</title>
<link rel="stylesheet" href="/static/app.css"></head><body>
<div class="wrap"><p><a href="/r/%s/">← %s</a></p><h1>%s</h1>
<pre class="meta">%s</pre><div>%s</div></div>
<footer class="site-footer"><span class="footer-version">satelle %s</span></footer>
</body></html>`, esc(title), slug, slug, esc(title), esc(id), esc(body), buildinfo.Version)
	}
}

func (s *MirrorServer) fragmentList(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug := r.PathValue("slug")
		repoKey, err := s.resolveSlug(r.Context(), slug)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		items, err := s.Store.ListItems(r.Context(), repoKey, kind)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(items)
	}
}

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

func esc(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, `"`, "&quot;")
	return s
}

// MirrorServeAddr is a convenience for tests.
func MirrorDefaultPath() string {
	return mirror.DefaultPath(config.GlobalDir())
}
