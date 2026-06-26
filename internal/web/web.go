// Package web is satelle's local web server — a basic project page for one
// repo, rendered entirely through verb.Dispatch. It is the satellites portal
// stripped to the bone: a plain http.ServeMux, no auth/OAuth/SSE, reaching
// data the SAME way the CLI does (CLI command / web handler → verb.Dispatch →
// store). When multiple repos are connected later, the aggregate becomes the
// workspace; the MVP ships single-repo first.
package web

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// Build returns the root handler for the given bootstrap. The handler renders
// the project page at "/" and answers GET /healthz; data flows through the
// verb registry, which the bootstrap already wired.
func Build(a *app.App) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprintln(w, "ok")
	})
	// Per-item detail pages — the URL you share to track one item's progress.
	mux.HandleFunc("GET /story/{id}", itemDetail(a, "story"))
	mux.HandleFunc("GET /task/{id}", itemDetail(a, "task"))
	mux.HandleFunc("/", projectPage(a))
	return mux
}

// itemDetail renders one story/task with its full fields and ledger timeline —
// the trackable per-item URL. group is "story" or "task" (selects the get verb).
func itemDetail(a *app.App, group string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		id := r.PathValue("id")

		item, err := fetchOne[workitem.Item](ctx, group+"-get", map[string]any{"id": id})
		if err != nil {
			// An unknown id is a 404, not a 500.
			http.Error(w, "not found: "+id, http.StatusNotFound)
			return
		}
		// Ledger timeline for this item (newest first for display).
		events, err := fetchList[ledger.Entry](ctx, "ledger-list", map[string]any{"story_id": id, "limit": 500})
		if err != nil {
			httpError(w, err)
			return
		}
		for i, j := 0, len(events)-1; i < j; i, j = i+1, j-1 {
			events[i], events[j] = events[j], events[i]
		}

		data := detailData{Group: group, RepoRoot: a.RepoRoot, Item: item, Events: events}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := detailTmpl.Execute(w, data); err != nil {
			httpError(w, err)
		}
	}
}

type detailData struct {
	Group    string
	RepoRoot string
	Item     workitem.Item
	Events   []ledger.Entry
}

// projectPage renders the single repo-overview page. It fetches stories,
// tasks, and indexed docs via verbs, so the page can never diverge from what
// the CLI reports.
func projectPage(a *app.App) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		ctx := r.Context()

		stories, err := fetchList[workitem.Item](ctx, "story-list", nil)
		if err != nil {
			httpError(w, err)
			return
		}
		tasks, err := fetchList[workitem.Item](ctx, "task-list", nil)
		if err != nil {
			httpError(w, err)
			return
		}
		allDocs, err := fetchList[docindex.Doc](ctx, "doc-list", nil)
		if err != nil {
			httpError(w, err)
			return
		}

		// Group docs by kind, preserving the canonical kind order.
		byKind := map[string][]docindex.Doc{}
		for _, d := range allDocs {
			byKind[d.Kind] = append(byKind[d.Kind], d)
		}
		kinds := make([]kindGroup, 0, len(config.AuthoredKinds))
		for _, k := range config.AuthoredKinds {
			kinds = append(kinds, kindGroup{Kind: k, Docs: byKind[k]})
		}

		data := pageData{
			RepoRoot: a.RepoRoot,
			DBPath:   a.DBPath,
			Stories:  stories,
			Tasks:    tasks,
			DocKinds: kinds,
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := pageTmpl.Execute(w, data); err != nil {
			httpError(w, err)
		}
	}
}

type pageData struct {
	RepoRoot string
	DBPath   string
	Stories  []workitem.Item
	Tasks    []workitem.Item
	DocKinds []kindGroup
}

type kindGroup struct {
	Kind string
	Docs []docindex.Doc
}

// fetchList dispatches a list verb and unmarshals the JSON array into []T.
func fetchList[T any](ctx context.Context, name string, req any) ([]T, error) {
	var body json.RawMessage
	if req != nil {
		b, err := json.Marshal(req)
		if err != nil {
			return nil, err
		}
		body = b
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
	var zero T
	body, err := json.Marshal(req)
	if err != nil {
		return zero, err
	}
	resp, err := verb.Dispatch(ctx, name, body)
	if err != nil {
		return zero, err
	}
	var out T
	if err := json.Unmarshal(resp, &out); err != nil {
		return zero, err
	}
	return out, nil
}

func httpError(w http.ResponseWriter, err error) {
	http.Error(w, err.Error(), http.StatusInternalServerError)
}

// pageTmpl / detailTmpl are the self-contained pages (inline CSS, no static
// assets) so the binary stays dependency-light and the pages travel with it.
var (
	pageTmpl   = template.Must(template.New("page").Parse(pageHTML))
	detailTmpl = template.Must(template.New("detail").Funcs(detailFuncs).Parse(detailHTML))
)
