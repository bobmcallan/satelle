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
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/buildinfo"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/help"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
	"github.com/bobmcallan/satelle/internal/workspace"
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
	mux.HandleFunc("GET /fragment/workflow/{name}", workflowFragment())
	mux.HandleFunc("GET /story/{id}", itemDetailPage("story"))
	mux.HandleFunc("GET /task/{id}", itemDetailPage("task"))

	mux.HandleFunc("GET /workspace", workspacePage(a))
	mux.HandleFunc("GET /help", helpPage())
	mux.HandleFunc("GET /{$}", projectPage(a))
	return &Server{Handler: mux, a: a, hub: h}
}

// workspacePage renders the multi-repo aggregate: the current repo plus every
// repo registered in the global config, each read from its own database. The
// single-repo project page (/) is untouched.
func workspacePage(a *app.App) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		gc, _ := config.LoadGlobal()
		roots := []string{a.RepoRoot}
		for _, rp := range gc.Workspace.Repos {
			if rp != a.RepoRoot {
				roots = append(roots, rp)
			}
		}
		render(w, "workspace", workspace.Load(r.Context(), roots))
	}
}

// helpTopic is one rendered help guide for the web /help page.
type helpTopic struct {
	Name  string
	Title string
	Body  string
}

// helpPage renders the embedded help topics (the same internal/help source the
// CLI `satelle help` reads) as a read-only guide page.
func helpPage() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		topics := make([]helpTopic, 0)
		for _, t := range help.List() {
			topics = append(topics, helpTopic{Name: t.Name, Title: t.Title, Body: t.Body})
		}
		render(w, "help", topics)
	}
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
	RepoRoot    string
	DBPath      string
	Stories     []rowVM
	Tasks       []rowVM
	DocKinds    []kindGroup
	DocCount    int
	Workflows   []workflowRowVM
	Version     string
	FooterName  string
	FooterEmail string
}

// rowVM is a work item plus its progress lights for the table row. Embedding the
// item promotes its fields, so the row template reaches .Status/.Tags/etc.
type rowVM struct {
	workitem.Item
	Lights []reviewLight
}

// reviewLight is one numbered stage circle in the progress column.
type reviewLight struct {
	Index int
	State string // pass | fail | fired | current
	Title string // tooltip
}

// lightPayload is the {from,to,skill} stamped on review/transition ledger rows.
type lightPayload struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Skill string `json:"skill"`
}

// buildLights folds a story's ledger rows into the progress strip: each distinct
// workflow edge gets a stable 1-based index; a gated transition is a pass
// (green), an ungated one a fired checkpoint (slate), a review_reject a fail
// (red). A non-terminal story trails a pulsing current light. Mirrors the
// satellites review-lights algorithm over satelle's ledger kinds.
func buildLights(entries []ledger.Entry, status string) []reviewLight {
	es := make([]ledger.Entry, len(entries)) // oldest-first
	copy(es, entries)
	for i, j := 0, len(es)-1; i < j; i, j = i+1, j-1 {
		es[i], es[j] = es[j], es[i]
	}
	parse := func(p json.RawMessage) lightPayload {
		var lp lightPayload
		_ = json.Unmarshal(p, &lp)
		return lp
	}
	accepted := map[string]bool{}
	for _, e := range es {
		if e.Kind == ledger.KindReviewAccept {
			lp := parse(e.Payload)
			accepted[lp.From+"→"+lp.To] = true
		}
	}
	idx := map[string]int{}
	next := 0
	idxFor := func(edge string) int {
		if _, ok := idx[edge]; !ok {
			next++
			idx[edge] = next
		}
		return idx[edge]
	}
	var lights []reviewLight
	for _, e := range es {
		lp := parse(e.Payload)
		edge := lp.From + " → " + lp.To
		switch e.Kind {
		case ledger.KindReviewReject:
			i := idxFor(edge)
			lights = append(lights, reviewLight{i, "fail", fmt.Sprintf("%d. %s — rejected", i, edge)})
		case ledger.KindStatusTransition:
			i := idxFor(edge)
			state := "fired"
			if accepted[lp.From+"→"+lp.To] {
				state = "pass"
			}
			lights = append(lights, reviewLight{i, state, fmt.Sprintf("%d. %s — %s", i, edge, state)})
		}
	}
	if status != "done" && status != "cancelled" {
		lights = append(lights, reviewLight{next + 1, "current", "current stage"})
	}
	return lights
}

// attachLights wraps items with their progress lights, reading each item's
// ledger via the same verb the detail view uses.
func attachLights(ctx context.Context, items []workitem.Item) []rowVM {
	out := make([]rowVM, len(items))
	for i, it := range items {
		entries, _ := fetchList[ledger.Entry](ctx, "ledger-list", map[string]any{"story_id": it.ID, "limit": 500})
		out[i] = rowVM{Item: it, Lights: buildLights(entries, it.Status)}
	}
	return out
}

// footerIdentity resolves the operator's name/email for the footer from the
// repo's git config (their identity, not baked into the binary). Best-effort and
// resolved once; empty when git or the keys are unavailable.
var (
	footerOnce  sync.Once
	footerName  string
	footerEmail string
)

func footerIdentity(repoRoot string) (string, string) {
	footerOnce.Do(func() {
		footerName = gitConfig(repoRoot, "user.name")
		footerEmail = gitConfig(repoRoot, "user.email")
	})
	return footerName, footerEmail
}

func gitConfig(dir, key string) string {
	cmd := exec.Command("git", "config", "--get", key)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
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
	name, email := footerIdentity(a.RepoRoot)
	return pageData{
		RepoRoot: a.RepoRoot, DBPath: a.DBPath,
		Stories: attachLights(ctx, stories), Tasks: attachLights(ctx, tasks),
		DocKinds: kinds, DocCount: len(allDocs),
		Workflows: workflowRows(byKind["workflows"]),
		Version:   buildinfo.Resolve().Version, FooterName: name, FooterEmail: email,
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
