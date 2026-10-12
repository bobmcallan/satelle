// context_harness.go — which harnesses this repo is bound to, and what each
// delivers inline.
//
// The SessionStart context is rendered under the limit of the harness that
// receives it, so a check that judges it against one number (the roomiest
// harness) passes a set that the harness actually in use degrades to read
// pointers. `satelle doctor`, `satelle init` and `satelle principle validate`
// all ask THIS file which harnesses to judge, so they cannot disagree.
//
// Neutral by construction (satelle-agent-agnostic): a harness name comes only
// from agentcli's detection of a seat command or from a [harness.<name>] table,
// and anything unrecognised is an "unavailable" entry — never assumed to be any
// provider.
package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/docindex"
)

// harnessBinding is one in-loop harness the repo is bound to and the budget its
// session context is delivered under — the same event, unit and limit
// `satelle hook context --harness <name>` applies.
type harnessBinding struct {
	Name  string
	Event string // hook event delivering the session set; "" means SessionStart
	Limit int
	Unit  string   // bytes | characters
	Seats []string // what bound this harness
}

// harnessGap is a harness or seat whose context cannot be judged, worded after
// the lookup that failed.
type harnessGap struct {
	Subject string
	Reason  string
}

// harnessBudget resolves one harness's delivery budget from configuration, or
// the gap that stops it.
func harnessBudget(cfg config.Config, name string) (harnessBinding, *harnessGap) {
	if !hasHarnessTable(cfg, name) {
		return harnessBinding{}, &harnessGap{Subject: name, Reason: fmt.Sprintf(
			"no [harness.%s] table in satelle.toml or the embedded defaults, so its SessionStart limit is unknown", name)}
	}
	h := harnessBinding{Name: name, Event: cfg.SessionContextEvent(name), Unit: "bytes"}
	if h.Event == "" || strings.EqualFold(h.Event, "SessionStart") {
		h.Limit = cfg.ContextLimit(name)
		return h, nil
	}
	h.Unit = "characters"
	h.Limit = cfg.ToolContextLimitChars(name)
	if h.Limit <= 0 {
		return harnessBinding{}, &harnessGap{Subject: name, Reason: fmt.Sprintf(
			"[harness.%s] delivers session context on %s but declares no tool_context_limit_chars", name, h.Event)}
	}
	return h, nil
}

func hasHarnessTable(cfg config.Config, name string) bool {
	if _, ok := cfg.Harness[name]; ok {
		return true
	}
	_, ok := config.EmbeddedHarness()[name]
	return ok
}

// configuredHarnesses is every harness with a [harness.<name>] table, sorted,
// without the neutral unknown fallback (which is a budget, not a harness).
func configuredHarnesses(cfg config.Config) []string {
	set := map[string]bool{}
	for k := range config.EmbeddedHarness() {
		set[k] = true
	}
	for k := range cfg.Harness {
		set[k] = true
	}
	delete(set, agentcli.HarnessUnknown)
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// seatBinding resolves one agents.toml seat the way dispatch does.
func seatBinding(a config.AgentsConfig, seat string) (config.AgentBinding, bool) {
	switch seat {
	case "executor":
		return a.ExecutorBinding(), true
	case config.RoleReviewer:
		return a.ReviewerBinding(), true
	}
	return a.NamedBinding(seat)
}

// boundHarnesses names the in-loop harnesses this repo's session context must
// fit, each with its delivery budget.
//
//   - A seat dispatched through a recognised harness binds that harness.
//   - An in-loop seat is the driving session; the harnesses it can run under are
//     the ones whose gate wiring is installed in the repo.
//   - A seat whose command no adapter recognises, and a harness with no budget,
//     are returned as gaps instead of being assumed to be something.
//   - Before any seat binds a harness (no agents config yet) every configured
//     harness is judged, so a fresh repo is held to the stricter limits too.
//
// dataDir is the authored plane; repoRoot is where harness wiring is installed.
func boundHarnesses(dataDir, repoRoot string) ([]harnessBinding, []harnessGap) {
	cfg, _, err := config.Load(filepath.Join(dataDir, config.ConfigName))
	if err != nil {
		cfg = config.Config{}
	}
	bound := map[string]*harnessBinding{}
	var gaps []harnessGap
	bind := func(name, seat string) {
		if b, ok := bound[name]; ok {
			if b != nil { // a nil entry is a harness already reported as a gap
				b.Seats = append(b.Seats, seat)
			}
			return
		}
		b, gap := harnessBudget(cfg, name)
		if gap != nil {
			gaps = append(gaps, *gap)
			bound[name] = nil
			return
		}
		b.Seats = []string{seat}
		bound[name] = &b
	}

	eff, aerr := config.LoadEffectiveAgents(dataDir, cfg.Vars)
	if aerr == nil {
		seats := []string{"executor", config.RoleReviewer}
		var named []string
		for name := range eff.Agents.Agents {
			named = append(named, name)
		}
		sort.Strings(named)
		inLoop := ""
		for _, seat := range append(seats, named...) {
			b, ok := seatBinding(eff.Agents, seat)
			if !ok {
				continue
			}
			cmd := b.CommandTemplate()
			switch {
			case strings.TrimSpace(cmd) == "":
				continue
			case config.IsInLoopCommand(cmd):
				if inLoop == "" {
					inLoop = seat
				}
			default:
				if h := agentcli.HarnessOf(cmd); h == agentcli.HarnessUnknown {
					gaps = append(gaps, harnessGap{Subject: "seat [" + seat + "]", Reason: fmt.Sprintf(
						"command %q is not detected by any adapter, so its harness limit is unknown", config.ExecutableToken(cmd))})
				} else {
					bind(h, "["+seat+"]")
				}
			}
		}
		if inLoop != "" {
			for _, h := range configuredHarnesses(cfg) {
				if harnessInstalled(cfg, h, repoRoot) {
					bind(h, "in-loop ["+inLoop+"]")
				}
			}
		}
	}

	if len(bound) == 0 && len(gaps) == 0 {
		for _, h := range configuredHarnesses(cfg) {
			bind(h, "every configured harness")
		}
	}

	names := make([]string, 0, len(bound))
	for n, b := range bound {
		if b != nil {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	out := make([]harnessBinding, 0, len(names))
	for _, n := range names {
		out = append(out, *bound[n])
	}
	return out, gaps
}

// harnessInstalled reports whether a harness's gate wiring is present in the
// repo — the evidence that a driving session of that harness runs here.
func harnessInstalled(cfg config.Config, harness, repoRoot string) bool {
	for _, p := range cfg.GateWiring(harness) {
		if _, err := os.Stat(filepath.Join(repoRoot, filepath.FromSlash(p))); err == nil {
			return true
		}
	}
	return false
}

// fsDocs reads principles and skills from the authored plane, falling back to
// the embedded defaults a repo has not overridden (the sparse virtual defaults
// rule) — without a store, so doctor can judge a repo that was never indexed.
// It satisfies agentstep.DocGetter, which is what lets the same seat-size and
// resident-set code run on it.
type fsDocs struct{ dataDir string }

func (f fsDocs) List(_ context.Context, kind string) ([]docindex.Doc, error) {
	seen := map[string]bool{}
	var out []docindex.Doc
	if entries, err := os.ReadDir(filepath.Join(f.dataDir, kind)); err == nil {
		for _, e := range entries {
			fn := e.Name()
			if e.IsDir() || !strings.HasSuffix(fn, ".md") || reservedKeepFile(fn) {
				continue
			}
			path := filepath.Join(f.dataDir, kind, fn)
			body, rerr := os.ReadFile(path)
			if rerr != nil {
				continue
			}
			name := strings.TrimSuffix(fn, ".md")
			seen[name] = true
			out = append(out, docindex.Doc{Kind: kind, Name: name, Path: path, Ext: ".md", Body: string(body)})
		}
	}
	for _, d := range config.EmbeddedDefaults() {
		if d.Kind != kind || seen[d.Name] || (d.Ext != "" && d.Ext != ".md") {
			continue
		}
		out = append(out, docindex.Doc{Kind: kind, Name: d.Name, Path: "embedded:" + d.RelPath(), Ext: ".md", Body: d.Body, Embedded: true})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (f fsDocs) Get(ctx context.Context, kind, name string) (docindex.Doc, error) {
	docs, _ := f.List(ctx, kind)
	for _, d := range docs {
		if d.Name == name {
			return d, nil
		}
	}
	return docindex.Doc{}, docindex.ErrNotFound
}

// renderSessionContext renders the stable body of the SessionStart context for
// one harness — constitution, then the session-resident principles, degraded to
// read pointers past the harness limit — through the same sessionAssemblyUnit
// `satelle hook context` calls. It returns the content and the names omitted to
// read pointers (nil when everything fits).
func renderSessionContext(dataDir string, h harnessBinding) (string, []string) {
	docs, _ := fsDocs{dataDir}.List(context.Background(), "principles")
	constPath := filepath.Join(dataDir, config.DefaultConstitutionName)
	rules, _ := condenseRules(dataDir)
	return sessionAssemblyUnit(readConstitution(constPath), selectAlwaysDocs(docs), constPath, h.Name, h.Limit, 0, h.Unit, rules, nil)
}

// measureContext is the size of content in the unit its harness counts in.
func measureContext(content string, h harnessBinding) int {
	if h.Unit == "characters" {
		return utf8.RuneCountInString(content)
	}
	return len(content)
}
