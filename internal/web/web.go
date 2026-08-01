// Package web is satelle's push-fed read-only web UI (mirror server).
// The live verb-dispatch Server (web.New) was removed in sty_80233c10 —
// production uses NewMirror only. Shared view-model types and pure helpers
// stay here with the embedded static assets and templates.
package web

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/wfdot"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// serverStart marks when the web service process came up.
var serverStart = time.Now()

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

func getTheme(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	t := globalTheme()
	if t == "" {
		t = "light"
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"theme": t})
}

type pageData struct {
	RepoRoot     string
	ProjectName  string
	DBPath       string
	Stories      []rowVM
	BacklogCount int
	// EngagementCount is the number of non-stale story seats (typically 0 or 1).
	// Always rendered in project chrome — 0 is visible, not hidden (sty_01ba9482).
	EngagementCount int
	// EngagedStoryIDs lists those seat item ids (sorted); empty when count is 0.
	EngagedStoryIDs []string
	Tasks           []rowVM
	DocKinds        []kindGroup
	DocCount        int
	Workflows       []workflowRowVM
	Uptime          string
	Theme           string
	TopBar          topBar
	Projects        []crumbProject // workspace project switcher for the breadcrumb
	// LastIngest is when this partition's state was last confirmed against the
	// repo, and Stale says that confirmation is older than mirror.StaleAfter —
	// so the page says so rather than presenting an unrepaired frame as current
	// (sty_e6e467fe).
	LastIngest time.Time
	Stale      bool
}

type crumbProject struct {
	Name      string
	Slug      string
	Path      string
	Current   bool
	Ambiguous bool
}

type topBar struct {
	Uptime string
	// User is the signed-in hosted-server identity, or nil when signed out /
	// unconfigured — the account control renders an avatar+menu vs a Sign in link.
	User *topBarUser
	// Active names the current nav item so its link renders accent (DS SiteHeader):
	// "home", "projects", "help", or "" for a page no nav item represents.
	Active string
	// MirrorRO is true on the push-fed serve: no sign-in/settings forms; identity
	// (if any) comes from the pushed meta blob (epic:mirror-ui-parity order:4).
	MirrorRO bool
	// IdentityEmail is the operator email from the pushed identity meta; shown as
	// a static strip when MirrorRO is set (no auth menu).
	IdentityEmail string
}

type rowVM struct {
	workitem.Item
	Lights []reviewLight
}

type reviewLight struct {
	Index int
	State string // pass | fail | fired | current
	Title string // tooltip
}

type lightPayload struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Skill string `json:"skill"`
}

type docRowVM struct {
	Name       string
	Headline   string
	ModTime    time.Time
	Provenance string // default | edited | authored; empty for free-form documents
	Source     string
}

type kindGroup struct {
	Kind string
	Docs []docRowVM
}

type seatRowVM struct {
	ID       string `json:"id"`
	InFlight bool   `json:"in_flight"`
	Stale    bool   `json:"stale"`
}

type detailData struct {
	Item   workitem.Item
	Events []eventVM
	// Route is the story's route DOCUMENT — the plan half plus every resolved
	// step's outcome, exactly as verb.recordRoute wrote it (sty_39e2d9df). Nil
	// when the story has not transitioned yet: the web layer presents this
	// artifact, it never re-derives one (sty_085e1a5a).
	Route      *storyDocVM
	Docs       []storyDocVM
	Executions []executionVM // populated only for a TASK — its runs (sty_30a917f8)
	TopBar     topBar
	Standalone bool
}

type executionVM struct {
	ID        string
	Status    string
	CreatedAt time.Time
	UpdatedAt time.Time
	Output    string // recorded run output (frontmatter stripped); "" when none
}

type storyDocRef struct {
	Name string `json:"name"`
	Type string `json:"type"`
	Body string `json:"body,omitempty"`
}

type storyDocVM struct {
	Name string
	Type string
	HTML template.HTML
}

type chipVM struct {
	Type  string
	Label string
}

type eventVM struct {
	ledger.Entry
	Chips []chipVM
}

type helpTopic struct {
	Name  string
	Title string
	Body  string
}

type docPageData struct {
	TopBar     topBar
	Kind       string
	Name       string
	Headline   string
	HTML       template.HTML
	Provenance string
	Source     string
}

type helpPageData struct {
	Topics []helpTopic
	TopBar topBar
}

type topBarUser struct {
	Name    string
	Email   string
	Initial string
}

type settingsRowVM struct {
	FieldID  string // the config key, shown as the row's monospace id
	Label    string
	Help     string
	Value    string
	SectHead string // non-empty on the first row of a new section group
}

type settingsData struct {
	Rows     []settingsRowVM
	TopBar   topBar
	RepoRoot string
	// MirrorRO hides the global-settings link (no write surface on push-fed serve).
	MirrorRO bool
}

func settingsRows(cfg config.Config) []settingsRowVM {
	var rows []settingsRowVM
	lastSect := "\x00"
	for _, s := range config.Settings {
		vm := settingsRowVM{FieldID: s.FieldID(), Label: s.Label, Help: s.Help, Value: config.SettingDisplay(cfg, s)}
		if s.Section != lastSect {
			vm.SectHead = sectionLabel(s.Section)
			lastSect = s.Section
		}
		rows = append(rows, vm)
	}
	return rows
}

func sectionLabel(s string) string {
	if s == "" {
		return "General"
	}
	return strings.ToUpper(s[:1]) + s[1:] // "hosted" → "Hosted", "gate" → "Gate"
}

func buildLights(entries []ledger.Entry, status string, seatHeld bool, stepOf func(state string) int) []reviewLight {
	// Derivation order (sty_c5065d05): status → current-stage light; ledger →
	// history enrichment; seat → decoration only (never the sole light for an
	// on-spine performing status). Entries may arrive newest- or oldest-first;
	// callers re-sort per story when needed.
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
	// The story is actively IN its current state, so the entry transition into that
	// state is rendered as the pulsing current light, in place at its starting
	// edge (not a completed step, and not appended at the tail — see below), so a
	// later higher-numbered reject of a rejected outgoing edge still trails it and
	// the strip reads in step order. Suppress that one transition — the LAST one
	// landing in the current state (an earlier visit in a recovery loop stays a
	// completed prior step) — but only for a non-terminal story sitting on the
	// spine (curStep > 0). Terminal stories render every transition (the entry
	// into done IS the final completed light), and an off-spine current state
	// keeps today's maxStep+1 fallback.
	curStep := stepOf(status)
	terminal := status == "done" || status == "cancelled"
	suppress := -1
	if !terminal && curStep > 0 {
		for pos, e := range es {
			if e.Kind == ledger.KindStatusTransition && parse(e.Payload).To == status {
				suppress = pos
			}
		}
	}
	var lights []reviewLight
	entered := false
	currentEmitted := false
	maxStep := 0
	minStep := 0
	note := func(i int) {
		if i > maxStep {
			maxStep = i
		}
		if minStep == 0 || i < minStep {
			minStep = i
		}
	}
	for pos, e := range es {
		lp := parse(e.Payload)
		edge := lp.From + " → " + lp.To
		switch e.Kind {
		case ledger.KindReviewReject:
			entered = true
			i := stepFor(lp.To, edge)
			lights = append(lights, reviewLight{i, "fail", fmt.Sprintf("%d. %s — rejected", i, edge)})
			note(i)
		case ledger.KindStatusTransition:
			entered = true
			i := stepFor(lp.To, edge)
			// note() before the suppress skip so the suppressed step still feeds
			// minStep/maxStep — the leading-gap fillers depend on it.
			note(i)
			if pos == suppress {
				// The entry transition INTO the current state is the step's STARTING
				// edge — starting a step closes the prior one — so render the pulsing
				// current light HERE, in ledger position, not appended at the tail.
				// Prior steps close to its left; a higher-numbered reject of a rejected
				// OUTGOING edge (release→done at step 5 while sitting at release, step
				// 4) then trails it, so the strip reads in step order (fixes 1 2 3 5 4).
				lights = append(lights, reviewLight{i, "current", "current stage"})
				currentEmitted = true
				continue
			}
			state := "fired"
			if accepted[lp.From+"→"+lp.To] {
				state = "pass"
			}
			lights = append(lights, reviewLight{i, state, fmt.Sprintf("%d. %s — %s", i, edge, state)})
		}
	}
	// If the earliest recorded step is beyond step 1 — e.g. an item engaged before
	// the workflow gained an earlier step, so its first transition lands mid-spine
	// (sty_d9a0b573) — prepend muted placeholders so the strip ALWAYS reads in
	// order from 1 rather than starting at a gap. A clean run (first step == 1)
	// prepends nothing.
	if minStep > 1 {
		fillers := make([]reviewLight, 0, minStep-1)
		for i := 1; i < minStep; i++ {
			fillers = append(fillers, reviewLight{i, "pending", fmt.Sprintf("%d. not run", i)})
		}
		lights = append(fillers, lights...)
	}
	// Current-stage light from STATUS when on-spine (sty_c5065d05 AC4): an
	// in_progress (etc.) story shows PROGRESS even with ZERO mirrored ledger
	// rows. Ledger suppress path above usually already emitted current in place;
	// this arm covers empty-ledger and off-spine entered fallbacks.
	if !terminal && !currentEmitted {
		if curStep > 0 {
			lights = append(lights, reviewLight{curStep, "current", "current stage"})
			currentEmitted = true
		} else if entered {
			// Off-spine with ledger history: keep maxStep+1 tail fallback.
			lights = append(lights, reviewLight{maxStep + 1, "current", "current stage"})
			currentEmitted = true
		}
	}
	// Pre-transition seat (sty_e1314fe3): live lease at the START state only
	// (curStep==0). On-spine performing statuses already have a status-derived
	// current light — seat must not add a second light or flicker with lease churn.
	if seatHeld && !entered && curStep == 0 {
		lights = append(lights, reviewLight{stepOf(status), "current", "starting"})
	}
	return lights
}

// spineDepths numbers the states on a shortest start→done path, which is what
// the status lights count. It takes a wfdot.Spec — the one lifecycle type the
// web layer knows — so no second parse or second shape exists here (sty_085e1a5a).
func spineDepths(spec wfdot.Spec) map[string]int {
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

func categoryStepOf(docs []docindex.Doc) func(category, state string) int {
	// Spine depths per category from workflow applies_to frontmatter. Prefer a
	// category-specific workflow, then wildcard (*), then the longest spine.
	// Mirrors agentstep precedence without importing agentstep (serve-binary
	// link isolation, sty_80233c10).
	var longest map[string]int
	byCat := map[string]map[string]int{}
	var wild map[string]int
	rs := routeSourceOf(docs)
	for _, d := range docs {
		if isRouteSourceDoc(d.Name) {
			continue // half of a derived route, not a lifecycle of its own
		}
		applies := frontmatterList(d.Body, "applies_to")
		spec, _, ok := workflowSpec(d.Body, rs, panelCategory(applies), nil)
		if !ok {
			continue
		}
		depths := spineDepths(spec)
		if len(depths) > len(longest) {
			longest = depths
		}
		isWild := len(applies) == 0
		for _, a := range applies {
			if a == "*" {
				isWild = true
				continue
			}
			if _, ok := byCat[a]; !ok {
				byCat[a] = depths
			}
		}
		if isWild && len(depths) > len(wild) {
			wild = depths
		}
	}
	return func(category, state string) int {
		if d, ok := byCat[category]; ok {
			return d[state]
		}
		if len(wild) > 0 {
			return wild[state]
		}
		return longest[state]
	}
}

func eventChips(e ledger.Entry) []chipVM {
	agent, model, outcome, tokens, durMs := ledger.EventTelemetry(e)
	var chips []chipVM
	if outcome != "" {
		chips = append(chips, chipVM{Type: "outcome", Label: outcome})
	}
	if durMs > 0 {
		chips = append(chips, chipVM{Type: "walltime", Label: humanMs(durMs)})
	}
	if tokens > 0 {
		chips = append(chips, chipVM{Type: "tokens", Label: humanTokens(tokens) + " tok"})
	}
	if model != "" {
		chips = append(chips, chipVM{Type: "model", Label: model})
	} else if agent != "" && agent != "reviewer" && agent != "executor" {
		chips = append(chips, chipVM{Type: "model", Label: agent})
	}
	return chips
}

func humanMs(ms int64) string {
	if ms <= 0 {
		return ""
	}
	if ms < 60000 {
		return strconv.FormatFloat(float64(ms)/1000, 'f', 1, 64) + "s"
	}
	return strconv.FormatInt(ms/60000, 10) + "m"
}

func humanTokens(n int) string {
	s := strconv.Itoa(n)
	if n < 1000 {
		return s
	}
	var out []byte
	for i, c := range []byte(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, c)
	}
	return string(out)
}

func stripFrontmatter(s string) string {
	if !strings.HasPrefix(s, "---\n") {
		return strings.TrimSpace(s)
	}
	if end := strings.Index(s[4:], "\n---"); end >= 0 {
		rest := s[4+end+len("\n---"):]
		return strings.TrimSpace(strings.TrimPrefix(rest, "\n"))
	}
	return strings.TrimSpace(s)
}

// newTopBar builds RO topbar chrome (no hosted auth on the push-fed serve).
func newTopBar(active string) topBar {
	return topBar{Uptime: formatUptime(time.Since(serverStart)), Active: active}
}

func httpError(w http.ResponseWriter, err error) {
	http.Error(w, err.Error(), http.StatusInternalServerError)
}

// footerEmail backs the shared footer template (mirror prefers identity meta).
var footerEmail string
