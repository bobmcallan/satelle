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
	"html/template"
	"net/http"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bobmcallan/satelle/internal/app"
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

// serverStart marks when the web service came up; the header shows the elapsed
// uptime. Set once when the server is wired.
var serverStart = time.Now()

// formatUptime renders an elapsed duration as a compact "up Hh Mm" / "up Nm" /
// "up Ns" string for the header.
func formatUptime(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("up %ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("up %dm", int(d.Minutes()))
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	return fmt.Sprintf("up %dh %dm", h, m)
}

// globalTheme returns the operator's explicit light/dark choice from the
// machine-wide config — shared across every repo. An EXPLICIT "light" or "dark"
// is authoritative (the server injects it so it overrides any stale per-browser
// localStorage); an empty value means the choice was never made, so the page
// falls back to localStorage/the light default.
func globalTheme() string {
	gc, err := config.LoadGlobal()
	if err != nil {
		return ""
	}
	if gc.UI.Theme == "dark" || gc.UI.Theme == "light" {
		return gc.UI.Theme
	}
	return ""
}

// getTheme reports the global theme as JSON so a page can reconcile after load.
func getTheme(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	t := globalTheme()
	if t == "" {
		t = "light"
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"theme": t})
}

// setTheme persists the light/dark choice to the machine-wide config, so the
// toggle in one repo's UI follows the operator into every other repo.
func setTheme(w http.ResponseWriter, r *http.Request) {
	theme := strings.TrimSpace(r.FormValue("theme"))
	if theme != "dark" && theme != "light" {
		http.Error(w, "theme must be dark or light", http.StatusBadRequest)
		return
	}
	gc, err := config.LoadGlobal()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	gc.UI.Theme = theme // store the EXPLICIT choice ("light" or "dark") — both authoritative
	if err := config.SaveGlobal(gc); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// New wires the server for the given bootstrap. It registers the verb-change
// notifier so web-initiated mutations ring the doorbell instantly; cross-
// process mutations (CLI edits) are picked up by StartRealtime's poller.
func New(a *app.App) *Server {
	serverStart = time.Now()
	footerIdentity(a.RepoRoot) // resolve the footer email once so every page's shared footer has it
	h := newHub()
	verb.SetChangeNotifier(h.publish)

	mux := http.NewServeMux()
	mux.Handle("GET /static/", http.FileServerFS(staticFS))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprintln(w, "ok")
	})
	mux.HandleFunc("GET /events", h.serveEvents)
	mux.HandleFunc("GET /theme", getTheme)
	mux.HandleFunc("POST /theme", setTheme)

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

	mux.HandleFunc("GET /doc/{kind}/{name}", docPage())
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
		render(w, "workspace", wsPageData{
			Aggregate: workspace.Load(r.Context(), roots),
			TopBar:    topBar{Uptime: formatUptime(time.Since(serverStart))},
		})
	}
}

// wsPageData embeds the workspace aggregate (so .Repos still resolves) and adds
// the shared top bar.
type wsPageData struct {
	workspace.Aggregate
	TopBar topBar
}

// helpTopic is one rendered help guide for the web /help page.
type helpTopic struct {
	Name  string
	Title string
	Body  string
}

// docPageData backs the standalone authored-document viewer: the rendered
// markdown plus the shared chrome.
type docPageData struct {
	TopBar   topBar
	Kind     string
	Name     string
	Headline string
	HTML     template.HTML
}

// docPage renders one authored document with its markdown formatted to HTML
// server-side (renderMarkdown is safe by construction — see markdown.go).
func docPage() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		kind, name := r.PathValue("kind"), r.PathValue("name")
		doc, err := fetchOne[docindex.Doc](r.Context(), "doc-get", map[string]any{"kind": kind, "name": name})
		if err != nil || doc.Name == "" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		render(w, "docPage", docPageData{
			TopBar:   topBar{Uptime: formatUptime(time.Since(serverStart))},
			Kind:     kind,
			Name:     doc.Name,
			Headline: doc.Headline,
			HTML:     renderMarkdown(doc.Body),
		})
	}
}

// helpPage renders the embedded help topics (the same internal/help source the
// CLI `satelle help` reads) as a read-only guide page.
func helpPage() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		topics := make([]helpTopic, 0)
		for _, t := range help.List() {
			topics = append(topics, helpTopic{Name: t.Name, Title: t.Title, Body: t.Body})
		}
		render(w, "help", helpPageData{
			Topics: topics,
			TopBar: topBar{Uptime: formatUptime(time.Since(serverStart))},
		})
	}
}

// helpPageData carries the help topics plus the shared top bar.
type helpPageData struct {
	Topics []helpTopic
	TopBar topBar
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
	RepoRoot     string
	DBPath       string
	Stories      []rowVM
	BacklogCount int
	Tasks        []rowVM
	DocKinds     []kindGroup
	DocCount     int
	Workflows    []workflowRowVM
	Uptime       string
	Theme        string
	TopBar       topBar
}

// topBar is the data the shared "topbar" template needs — the page-chrome
// utility cluster (uptime indicator + theme toggle + live dot) rendered
// identically on every page so the nav is one component, not a per-page copy.
type topBar struct {
	Uptime string
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

// buildLights folds a story's ledger rows into the progress strip. A light's
// NUMBER is the workflow STEP it represents — stepOf maps a transition's target
// state to its 1-based position on the workflow's forward spine (every step, not
// only the gated ones) — so a clean run reads (1) → (2) → (3) → (4) sequentially,
// and a step attempted more than once (a reject then a later accept of the same
// edge, or a recovery loop) renders lights that SHARE the step number (e.g. 1 red
// then 1 green) rather than incrementing. A gated transition is a pass (green), an
// ungated one a fired checkpoint (slate), a review_reject a fail (red). A
// non-terminal story trails a pulsing current light at the next step. Off-spine
// targets (e.g. blocked) fall back to ledger-appearance order.
func buildLights(entries []ledger.Entry, status string, stepOf func(state string) int) []reviewLight {
	// ledger-list yields entries oldest-first (the store orders created_at ASC),
	// which is the order the lights render left-to-right — consume it as-is so
	// the steps read 1 → N rather than reversed.
	es := entries
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
	// Off-spine fallback: an edge whose target has no gated step still gets a
	// stable number by order of first appearance, after the highest real step.
	idx := map[string]int{}
	extra := 0
	stepFor := func(to, edge string) int {
		if s := stepOf(to); s > 0 {
			return s
		}
		if _, ok := idx[edge]; !ok {
			extra++
			idx[edge] = extra
		}
		return idx[edge]
	}
	var lights []reviewLight
	maxStep := 0
	for _, e := range es {
		lp := parse(e.Payload)
		edge := lp.From + " → " + lp.To
		switch e.Kind {
		case ledger.KindReviewReject:
			i := stepFor(lp.To, edge)
			lights = append(lights, reviewLight{i, "fail", fmt.Sprintf("%d. %s — rejected", i, edge)})
			if i > maxStep {
				maxStep = i
			}
		case ledger.KindStatusTransition:
			i := stepFor(lp.To, edge)
			state := "fired"
			if accepted[lp.From+"→"+lp.To] {
				state = "pass"
			}
			lights = append(lights, reviewLight{i, state, fmt.Sprintf("%d. %s — %s", i, edge, state)})
			if i > maxStep {
				maxStep = i
			}
		}
	}
	// Trail a pulsing "current" light at the NEXT step only once the item has
	// actually entered the workflow (≥1 recorded transition). A freshly-created
	// item still at its initial state has started no step — no phantom current ①.
	if len(lights) > 0 && status != "done" && status != "cancelled" {
		cur := maxStep + 1
		if s := stepOf(status); s > 0 {
			cur = s + 1
		}
		lights = append(lights, reviewLight{cur, "current", "current stage"})
	}
	return lights
}

// spineDepths maps each state on the workflow's forward SUCCESS spine to its
// 1-based step number — the BFS distance from the start state, restricted to
// states that can reach the success terminal ("done"). EVERY step on that path is
// numbered (executor and reviewer alike), so a clean run reads 1→2→3→…→N rather
// than restarting at 1 for ungated executor steps. Off-spine states (cancelled or
// blocked detours that cannot reach done) are absent — step 0 — and a back edge
// (the committed→in_progress recovery) never inflates a number, because BFS keeps
// the shortest distance. The start state itself is depth 0 and omitted.
func spineDepths(spec wfSpec) map[string]int {
	adj := map[string][]string{}
	radj := map[string][]string{}
	indeg := map[string]int{}
	for _, s := range spec.States {
		if _, ok := indeg[s.Name]; !ok {
			indeg[s.Name] = 0
		}
	}
	for _, t := range spec.Transitions {
		adj[t.From] = append(adj[t.From], t.To)
		radj[t.To] = append(radj[t.To], t.From)
		indeg[t.To]++
	}
	// Success terminal: prefer a state literally named "done"; else the first
	// terminal (no outgoing edges).
	done := ""
	for _, s := range spec.States {
		if s.Name == "done" {
			done = s.Name
			break
		}
	}
	if done == "" {
		for _, s := range spec.States {
			if len(adj[s.Name]) == 0 {
				done = s.Name
				break
			}
		}
	}
	if done == "" {
		return map[string]int{}
	}
	// Start states = no incoming edges (deterministic order).
	var starts []string
	for name, d := range indeg {
		if d == 0 {
			starts = append(starts, name)
		}
	}
	sort.Strings(starts)
	dStart := bfsDist(adj, starts)         // forward distance from the start(s)
	dDone := bfsDist(radj, []string{done}) // distance to `done` (reverse BFS)
	total, ok := dStart[done]
	if !ok {
		return map[string]int{} // start cannot reach done
	}
	// A state is a spine STEP when it lies on a shortest start→done path:
	// dStart + dDone == total. This admits the forward chain (in_progress,
	// commit_push, committed, done) and excludes both unreachable terminals
	// (cancelled) and rejoining detours (blocked) — and a back edge never lowers
	// any dStart, so the recovery loop leaves the numbering intact. The start(s)
	// (depth 0) are omitted.
	out := map[string]int{}
	for name, ds := range dStart {
		if ds < 1 {
			continue
		}
		if dd, ok := dDone[name]; ok && ds+dd == total {
			out[name] = ds
		}
	}
	return out
}

// bfsDist returns the shortest-edge distance from any of starts to every
// reachable node over the given adjacency.
func bfsDist(adj map[string][]string, starts []string) map[string]int {
	dist := map[string]int{}
	var q []string
	for _, s := range starts {
		if _, seen := dist[s]; !seen {
			dist[s] = 0
			q = append(q, s)
		}
	}
	for len(q) > 0 {
		n := q[0]
		q = q[1:]
		for _, m := range adj[n] {
			if _, seen := dist[m]; !seen {
				dist[m] = dist[n] + 1
				q = append(q, m)
			}
		}
	}
	return dist
}

// categoryStepOf builds a per-CATEGORY step resolver: each item is numbered
// against the workflow ACTIVE for its category, never a single hardcoded one. The
// selection mirrors the reviewer's precedence — a workflow whose applies_to lists
// the category wins; a wildcard ("*") is next; the longest spine is the final
// fallback — so e.g. an epic-parent (parent workflow, backlog→done) numbers
// `done` as step 1 while a feature (wildcard project workflow) numbers it 5.
func categoryStepOf(docs []docindex.Doc) func(category, state string) int {
	type wf struct {
		applies []string
		depths  map[string]int
	}
	var wfs []wf
	var longest map[string]int
	for _, d := range docs {
		depths := spineDepths(parseWorkflow(d.Body))
		wfs = append(wfs, wf{applies: frontmatterList(d.Body, "applies_to"), depths: depths})
		if len(depths) > len(longest) {
			longest = depths
		}
	}
	pick := func(category string) map[string]int {
		if category != "" {
			for _, w := range wfs {
				if sliceHas(w.applies, category) {
					return w.depths
				}
			}
		}
		for _, w := range wfs {
			if sliceHas(w.applies, "*") {
				return w.depths
			}
		}
		return longest
	}
	return func(category, state string) int { return pick(category)[state] }
}

// sliceHas reports whether ss contains want.
func sliceHas(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

// attachLights wraps items with their progress lights, numbering each item
// against the workflow active for ITS category (catStepOf).
func attachLights(ctx context.Context, items []workitem.Item, catStepOf func(category, state string) int) []rowVM {
	out := make([]rowVM, len(items))
	for i, it := range items {
		entries, _ := fetchList[ledger.Entry](ctx, "ledger-list", map[string]any{"story_id": it.ID, "limit": 500})
		stepOf := func(s string) int { return catStepOf(it.Category, s) }
		out[i] = rowVM{Item: it, Lights: buildLights(entries, it.Status, stepOf)}
	}
	return out
}

// footerIdentity resolves the operator's email for the footer from the repo's git
// config (their identity, not baked into the binary). Best-effort and resolved
// once; empty when git or the key is unavailable.
var (
	footerOnce  sync.Once
	footerEmail string
)

func footerIdentity(repoRoot string) string {
	footerOnce.Do(func() {
		footerEmail = gitConfig(repoRoot, "user.email")
	})
	return footerEmail
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
	backlog := 0
	for _, s := range stories {
		if s.Status == workitem.StatusBacklog {
			backlog++
		}
	}
	catStepOf := categoryStepOf(byKind["workflows"])
	return pageData{
		RepoRoot: a.RepoRoot, DBPath: a.DBPath,
		Stories: attachLights(ctx, stories, catStepOf), BacklogCount: backlog,
		Tasks:    attachLights(ctx, tasks, catStepOf),
		DocKinds: kinds, DocCount: len(allDocs),
		Workflows: workflowRows(byKind["workflows"]),
		Uptime:    formatUptime(time.Since(serverStart)),
		Theme:     globalTheme(),
		TopBar:    topBar{Uptime: formatUptime(time.Since(serverStart))},
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
// Standalone is true only on the standalone /story/<id> page, where the
// "Open story →" self-link is redundant and hidden; the expanded project-page
// card (the fragment) keeps it.
type detailData struct {
	Item       workitem.Item
	Events     []ledger.Entry
	Docs       []storyDocVM
	TopBar     topBar
	Standalone bool
}

// storyDocRef is one of a story's attached documents (from story-doc-list /
// story-doc-get).
type storyDocRef struct {
	Name string `json:"name"`
	Type string `json:"type"`
	Body string `json:"body,omitempty"`
}

// storyDocVM is an attached document prepared for the detail-page tab strip: its
// markdown rendered to safe HTML.
type storyDocVM struct {
	Name string
	Type string
	HTML template.HTML
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
	// A story's attached documents become tabs on the detail page. Best-effort:
	// the timeline and detail still render if a doc fails to load. Tasks have none,
	// so the list comes back empty and no tab strip shows.
	var docs []storyDocVM
	if refs, derr := fetchList[storyDocRef](ctx, "story-doc-list", map[string]any{"story_id": id}); derr == nil {
		for _, ref := range refs {
			full, gerr := fetchOne[storyDocRef](ctx, "story-doc-get", map[string]any{"story_id": id, "name": ref.Name})
			if gerr != nil {
				continue
			}
			docs = append(docs, storyDocVM{Name: ref.Name, Type: ref.Type, HTML: renderMarkdown(full.Body)})
		}
	}
	return detailData{Item: item, Events: events, Docs: docs, TopBar: topBar{Uptime: formatUptime(time.Since(serverStart))}}, nil
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
		d.Standalone = true // the standalone page hides its own "Open story →" self-link
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
