// Package web is satelle's local web server — a project page for one repo,
// rendered through verb.Dispatch (the same seam the CLI uses), no auth. It is
// the satellites portal style brought to the local tier: tabbed panels, an SSE
// realtime doorbell, inline expand/collapse, and filter chips — but stripped of
// auth/OAuth/sessions. Static assets and templates are embedded so the binary
// stays self-contained.
package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// Server is the local web server: an http.Handler plus the realtime hub.
type Server struct {
	Handler http.Handler
	a       *app.App
	hub     *hub
}

// New wires the server for the given bootstrap. It registers the verb-change
// notifier so web-initiated mutations ring the doorbell instantly; cross-
// process mutations (CLI edits) are picked up by StartRealtime's poller.
func New(a *app.App) *Server {
	h := newHub()
	verb.SetChangeNotifier(h.publish)

	mux := http.NewServeMux()
	mux.Handle("GET /static/", http.FileServerFS(staticFS))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprintln(w, "ok")
	})
	mux.HandleFunc("GET /events", h.serveEvents)

	// Realtime panel fragments (rows only) — what the SSE refetch swaps in.
	mux.HandleFunc("GET /fragment/stories", fragmentRows(a, "workitemRows", verb.TopicStories))
	mux.HandleFunc("GET /fragment/tasks", fragmentRows(a, "workitemRows", verb.TopicTasks))
	mux.HandleFunc("GET /fragment/docs", fragmentRows(a, "docsRows", verb.TopicDocs))

	// Inline expand fragments + standalone detail pages (shared template).
	mux.HandleFunc("GET /fragment/story/{id}", itemFragment("story"))
	mux.HandleFunc("GET /fragment/task/{id}", itemFragment("task"))
	mux.HandleFunc("GET /story/{id}", itemDetailPage("story"))
	mux.HandleFunc("GET /task/{id}", itemDetailPage("task"))

	mux.HandleFunc("GET /{$}", projectPage(a))
	return &Server{Handler: mux, a: a, hub: h}
}

// Build is the thin handler-only constructor used by tests (no poller).
func Build(a *app.App) http.Handler { return New(a).Handler }

// StartRealtime runs the cross-process DB-change poller until ctx is cancelled.
// The CLI and the server are separate processes sharing one sqlite file, so the
// in-process notifier alone can't see CLI edits; the poller fingerprints each
// panel and rings the doorbell when one changes. interval<=0 uses 1.5s.
func (s *Server) StartRealtime(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 1500 * time.Millisecond
	}
	go s.pollDB(ctx, interval)
}

// pollDB publishes a topic whenever its store fingerprint changes.
func (s *Server) pollDB(ctx context.Context, interval time.Duration) {
	prev := map[string]string{}
	check := func(topic string, fp func(context.Context) (string, error)) {
		cur, err := fp(ctx)
		if err != nil {
			return
		}
		if old, seen := prev[topic]; seen && old != cur {
			s.hub.publish(topic)
		}
		prev[topic] = cur
	}
	tick := func() {
		check(verb.TopicStories, func(c context.Context) (string, error) {
			return s.a.Store.Stories.Fingerprint(c, workitem.KindStory)
		})
		check(verb.TopicTasks, func(c context.Context) (string, error) {
			return s.a.Store.Stories.Fingerprint(c, workitem.KindTask)
		})
		check(verb.TopicDocs, func(c context.Context) (string, error) {
			return s.a.Store.DocIndex.Fingerprint(c)
		})
	}
	tick() // seed fingerprints without firing
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			tick()
		}
	}
}

type pageData struct {
	RepoRoot string
	DBPath   string
	Stories  []workitem.Item
	Tasks    []workitem.Item
	DocKinds []kindGroup
	DocCount int
}

type kindGroup struct {
	Kind string
	Docs []docindex.Doc
}

// loadPanels fetches the three panels' data through the verbs.
func loadPanels(ctx context.Context, a *app.App) (pageData, error) {
	stories, err := fetchList[workitem.Item](ctx, "story-list", nil)
	if err != nil {
		return pageData{}, err
	}
	tasks, err := fetchList[workitem.Item](ctx, "task-list", nil)
	if err != nil {
		return pageData{}, err
	}
	allDocs, err := fetchList[docindex.Doc](ctx, "doc-list", nil)
	if err != nil {
		return pageData{}, err
	}
	byKind := map[string][]docindex.Doc{}
	for _, d := range allDocs {
		byKind[d.Kind] = append(byKind[d.Kind], d)
	}
	kinds := make([]kindGroup, 0, len(config.AuthoredKinds))
	for _, k := range config.AuthoredKinds {
		kinds = append(kinds, kindGroup{Kind: k, Docs: byKind[k]})
	}
	return pageData{
		RepoRoot: a.RepoRoot, DBPath: a.DBPath,
		Stories: stories, Tasks: tasks, DocKinds: kinds, DocCount: len(allDocs),
	}, nil
}

func projectPage(a *app.App) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data, err := loadPanels(r.Context(), a)
		if err != nil {
			httpError(w, err)
			return
		}
		render(w, "page", data)
	}
}

// fragmentRows renders just one panel's rows — the realtime refetch target.
func fragmentRows(a *app.App, tmplName, topic string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data, err := loadPanels(r.Context(), a)
		if err != nil {
			httpError(w, err)
			return
		}
		switch topic {
		case verb.TopicStories:
			render(w, tmplName, data.Stories)
		case verb.TopicTasks:
			render(w, tmplName, data.Tasks)
		case verb.TopicDocs:
			render(w, tmplName, data.DocKinds)
		}
	}
}

// detailData backs the inline expand fragment and the standalone detail page.
type detailData struct {
	Item   workitem.Item
	Events []ledger.Entry
}

// loadDetail fetches one item + its (newest-first) ledger timeline via verbs.
func loadDetail(ctx context.Context, group, id string) (detailData, error) {
	item, err := fetchOne[workitem.Item](ctx, group+"-get", map[string]any{"id": id})
	if err != nil {
		return detailData{}, err
	}
	events, err := fetchList[ledger.Entry](ctx, "ledger-list", map[string]any{"story_id": id, "limit": 500})
	if err != nil {
		return detailData{}, err
	}
	for i, j := 0, len(events)-1; i < j; i, j = i+1, j-1 {
		events[i], events[j] = events[j], events[i]
	}
	return detailData{Item: item, Events: events}, nil
}

func itemFragment(group string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		d, err := loadDetail(r.Context(), group, r.PathValue("id"))
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		render(w, "itemDetail", d)
	}
}

func itemDetailPage(group string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		d, err := loadDetail(r.Context(), group, r.PathValue("id"))
		if err != nil {
			http.Error(w, "not found: "+r.PathValue("id"), http.StatusNotFound)
			return
		}
		render(w, "detailPage", d)
	}
}

// render executes a named template to a buffer first so a template error
// surfaces as a 500 instead of a half-written response.
func render(w http.ResponseWriter, name string, data any) {
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, name, data); err != nil {
		httpError(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = buf.WriteTo(w)
}

// fetchList dispatches a list verb and unmarshals the JSON array into []T.
func fetchList[T any](ctx context.Context, name string, req any) ([]T, error) {
	body, err := marshalReq(req)
	if err != nil {
		return nil, err
	}
	resp, err := verb.Dispatch(ctx, name, body)
	if err != nil {
		return nil, err
	}
	var out []T
	if err := json.Unmarshal(resp, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// fetchOne dispatches a get verb and unmarshals the JSON object into T.
func fetchOne[T any](ctx context.Context, name string, req any) (T, error) {
	var out T
	body, err := marshalReq(req)
	if err != nil {
		return out, err
	}
	resp, err := verb.Dispatch(ctx, name, body)
	if err != nil {
		return out, err
	}
	err = json.Unmarshal(resp, &out)
	return out, err
}

func marshalReq(req any) (json.RawMessage, error) {
	if req == nil {
		return nil, nil
	}
	return json.Marshal(req)
}

func httpError(w http.ResponseWriter, err error) {
	http.Error(w, err.Error(), http.StatusInternalServerError)
}
