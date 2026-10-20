package agentstep

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// Read-only context sources (sty_12499ecc AC1, AC4). The neutral name "src" and the
// reference ref:jira:ID-12 are test data; the engine knows neither.

// ctxRunner records every request and answers each with a verdict whose
// `consulted` reports one lookup through "src" and one through "nope" — a source
// nobody offered.
type ctxRunner struct {
	mu   sync.Mutex
	reqs []agentcli.Request
	out  string
}

func (r *ctxRunner) Name() string    { return "ctx-fake" }
func (r *ctxRunner) Command() string { return "ctx-fake -p {system}" }
func (r *ctxRunner) Run(_ context.Context, req agentcli.Request) ([]byte, error) {
	r.mu.Lock()
	r.reqs = append(r.reqs, req)
	r.mu.Unlock()
	if r.out != "" {
		return []byte(r.out), nil
	}
	return []byte(`{"decision":"accept","notes":"ok","consulted":[{"source":"src","ref":"jira:ID-12"},{"source":"nope","ref":"x"}]}`), nil
}

// forSkill returns the requests whose prompt carries the skill's rubric heading.
func (r *ctxRunner) forSkill(skill string) []agentcli.Request {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []agentcli.Request
	for _, q := range r.reqs {
		if strings.Contains(q.SystemPrompt, "# "+skill+"\n") {
			out = append(out, q)
		}
	}
	return out
}

func testSrc() agentcli.ContextServer {
	return agentcli.ContextServer{
		Name: "src", Type: agentcli.ContextHTTP, URL: "https://ctx.example.test/mcp",
		Headers: map[string]string{"Authorization": "Bearer tok"},
		Tools:   []string{"get_issue", "search_issues"},
	}
}

// contextRig is an engine over a route whose plan step declares context = ["src"]
// and whose in_progress step declares none: each step has a performer, a route
// reviewer and a gate-table reviewer.
func contextRig(t *testing.T, planExtra string, planReviewers ...string) (*Engine, *ctxRunner) {
	t.Helper()
	if len(planReviewers) == 0 {
		planReviewers = []string{"rev-rp"}
	}
	wf := spineWF("", "",
		"[[gate]]\nskill = \"rev-gp\"\nagent = \"reviewer\"\non = [\"plan\"]\n\n"+
			"[[gate]]\nskill = \"rev-gi\"\nagent = \"reviewer\"\non = [\"in_progress\"]\n",
		"plan|worker|perf-p|"+strings.Join(planReviewers, ",")+"|reviewer||"+planExtra,
		"in_progress|worker|perf-i|rev-ri|reviewer",
		"done")
	docs := fakeDocs{workflow: wf, extraSkills: []docindex.Doc{
		{Kind: "skills", Name: "perf-p", Body: parallelSkill("perf-p")},
		{Kind: "skills", Name: "perf-i", Body: parallelSkill("perf-i")},
		{Kind: "skills", Name: "rev-rp", Body: parallelSkill("rev-rp")},
		{Kind: "skills", Name: "rev-rq", Body: parallelSkill("rev-rq")},
		{Kind: "skills", Name: "rev-ri", Body: parallelSkill("rev-ri")},
		{Kind: "skills", Name: "rev-gp", Body: parallelSkill("rev-gp")},
		{Kind: "skills", Name: "rev-gi", Body: parallelSkill("rev-gi")},
	}}
	r := &ctxRunner{}
	g := New(r, docs, "/repo", "")
	g.backoff = func(int) time.Duration { return 0 }
	g.SetNamedAgents(func(name string) (config.AgentBinding, bool) {
		if name == "worker" {
			return config.AgentBinding{Command: "ctx-fake -p {system}", Tools: "Read,Grep,Glob,Bash(satelle:*)", Role: "agent"}, true
		}
		return config.AgentBinding{}, false
	})
	g.newRunner = func(string, string) (agentcli.Runner, error) { return r, nil }
	cfg := config.Config{Tags: config.TagsConfig{References: []string{"ref"}}}
	g.SetContextSources(func(name string) (agentcli.ContextServer, bool) {
		if name == "src" {
			return testSrc(), true
		}
		return agentcli.ContextServer{}, false
	})
	g.SetReferences(cfg.References)
	return g, r
}

// ctxItem is the story at the status that precedes the edge into `to` (backlog,
// for the plan edge when `to` is omitted).
func ctxItem(to ...string) workitem.Item {
	from := "backlog"
	if len(to) > 0 && to[0] == "in_progress" {
		from = "plan"
	}
	return workitem.Item{ID: "sty_ctx", Status: from, Category: "feature", Tags: []string{"ref:jira:ID-12", "surface:cli"}}
}

// AC1: the sources a step declares reach its performer, its route reviewer and its
// gate-table reviewer, and a sibling step's dispatches carry none.
func TestContext_StepSourcesReachEveryDispatchOfTheDeclaringStepOnly(t *testing.T) {
	g, r := contextRig(t, `context = ["src"]`)
	ctx := context.Background()
	for _, to := range []string{"plan", "in_progress"} {
		dec, err := g.Gate(ctx, ctxItem(to), to)
		if err != nil || !dec.Accept {
			t.Fatalf("Gate(%s) = %+v, %v", to, dec, err)
		}
		if _, err := g.DispatchExecutor(ctx, ctxItem(to), to); err != nil {
			t.Fatalf("DispatchExecutor(%s): %v", to, err)
		}
	}
	for _, tc := range []struct {
		role, skill string
		want        bool
	}{
		{"plan performer", "perf-p", true},
		{"plan route reviewer", "rev-rp", true},
		{"plan gate-table reviewer", "rev-gp", true},
		{"in_progress performer", "perf-i", false},
		{"in_progress route reviewer", "rev-ri", false},
		{"in_progress gate-table reviewer", "rev-gi", false},
	} {
		reqs := r.forSkill(tc.skill)
		if len(reqs) == 0 {
			t.Fatalf("%s: no dispatch recorded for %s", tc.role, tc.skill)
		}
		for _, q := range reqs {
			got := len(q.ContextServers) == 1 && q.ContextServers[0].Name == "src" && len(q.ContextServers[0].Tools) == 2
			if got != tc.want {
				t.Errorf("%s: context servers = %+v, want carried=%v", tc.role, q.ContextServers, tc.want)
			}
			if has := strings.Contains(q.SystemPrompt, "## Context sources"); has != tc.want {
				t.Errorf("%s: briefing present = %v, want %v", tc.role, has, tc.want)
			}
			if tc.want {
				for _, s := range []string{"`src`", "`get_issue`", "`search_issues`", "ref:jira:ID-12"} {
					if !strings.Contains(q.SystemPrompt, s) {
						t.Errorf("%s: briefing missing %s", tc.role, s)
					}
				}
			}
		}
	}
}

// The briefing carries mechanism only: it lists what is offered and gives no
// instruction. The rule for using it is the satelle-reviewer-verdict principle's.
func TestContext_BriefingListsOfferedAndGivesNoInstruction(t *testing.T) {
	if got := contextBriefing(nil); got != "" {
		t.Errorf("no offer must be no block, got %q", got)
	}
	if got := contextBriefing(&ContextOffer{}); got != "" {
		t.Errorf("an offer with no source must be no block, got %q", got)
	}
	cfg := config.Config{Tags: config.TagsConfig{References: []string{"ref"}}}
	b := contextBriefing(&ContextOffer{
		Servers:    []agentcli.ContextServer{testSrc()},
		References: cfg.References([]string{"ref:jira:ID-12"}),
	})
	for _, want := range []string{"## Context sources", "`src`", "`get_issue`", "`search_issues`", "`ref:jira:ID-12`", "jira", "ID-12"} {
		if !strings.Contains(b, want) {
			t.Errorf("block missing %q:\n%s", want, b)
		}
	}
	for _, instruction := range []string{"consulted", "look up", "lookup", "must", "before judging"} {
		if strings.Contains(strings.ToLower(b), instruction) {
			t.Errorf("block carries instruction text %q:\n%s", instruction, b)
		}
	}
	if secret := "tok"; strings.Contains(b, secret) {
		t.Errorf("block leaks a header value:\n%s", b)
	}
}

// AC4: a verdict records the sources offered to its reviewer and the lookups it
// reports, kept only from offered sources, the rest counted as dropped.
func TestContext_VerdictRecordsOfferedAndConsultedFromOfferedOnly(t *testing.T) {
	g, _ := contextRig(t, `context = ["src"]`)
	ctx := context.Background()

	dec, err := g.Gate(ctx, ctxItem(), "plan")
	if err != nil || !dec.Accept {
		t.Fatalf("Gate(plan) = %+v, %v", dec, err)
	}
	if len(dec.Reviewers) != 2 {
		t.Fatalf("reviewers = %+v", dec.Reviewers)
	}
	for _, rv := range dec.Reviewers {
		if len(rv.ContextSources) != 1 || rv.ContextSources[0] != "src" {
			t.Errorf("%s: context_sources = %v, want [src]", rv.Skill, rv.ContextSources)
		}
		if len(rv.Consulted) != 1 || rv.Consulted[0] != (verb.ConsultedRef{Source: "src", Ref: "jira:ID-12"}) {
			t.Errorf("%s: consulted = %+v, want the one offered lookup", rv.Skill, rv.Consulted)
		}
		if rv.ConsultedDropped != 1 {
			t.Errorf("%s: consulted_dropped = %d, want 1 (the unoffered source)", rv.Skill, rv.ConsultedDropped)
		}
	}

	// The sibling step offers nothing, so nothing can have been consulted.
	dec, err = g.Gate(ctx, ctxItem("in_progress"), "in_progress")
	if err != nil {
		t.Fatal(err)
	}
	for _, rv := range dec.Reviewers {
		if len(rv.ContextSources) != 0 || len(rv.Consulted) != 0 || rv.ConsultedDropped != 2 {
			t.Errorf("%s: sources %v consulted %+v dropped %d, want none kept and both dropped",
				rv.Skill, rv.ContextSources, rv.Consulted, rv.ConsultedDropped)
		}
	}
}

// A bundled session's per-rubric `consulted` is filtered the same way.
func TestContext_BundledVerdictsFilterConsultedToo(t *testing.T) {
	g, r := contextRig(t, "context = [\"src\"]\nbundle = true", "rev-rp", "rev-rq")
	r.out = `{"verdicts":[` +
		`{"skill":"rev-rp","decision":"accept","notes":"a","consulted":[{"source":"src","ref":"jira:ID-12"},{"source":"nope","ref":"x"}]},` +
		`{"skill":"rev-rq","decision":"accept","notes":"b"},` +
		`{"skill":"rev-gp","decision":"accept","notes":"c"}]}`
	dec, err := g.Gate(context.Background(), ctxItem(), "plan")
	if err != nil || !dec.Accept {
		t.Fatalf("Gate = %+v, %v", dec, err)
	}
	by := map[string]verb.ReviewerVerdict{}
	for _, rv := range dec.Reviewers {
		by[rv.Skill] = rv
	}
	a, b := by["rev-rp"], by["rev-rq"]
	if a.BundleID == "" {
		t.Fatalf("the route reviewers did not bundle: %+v", a)
	}
	if len(a.Consulted) != 1 || a.Consulted[0].Source != "src" || a.ConsultedDropped != 1 || len(a.ContextSources) != 1 {
		t.Errorf("rev-rp = %+v", a)
	}
	if len(b.Consulted) != 0 || b.ConsultedDropped != 0 || len(b.ContextSources) != 1 {
		t.Errorf("rev-rq = %+v, want offered but nothing consulted", b)
	}
}

// A verdict from before this existed parses unchanged.
func TestContext_OldShapeVerdictParsesUnchanged(t *testing.T) {
	d, err := parseDecision([]byte(`{"decision":"accept","notes":"n","reasoning":"r"}`))
	if err != nil || !d.Accept || d.Notes != "n" || d.Reasoning != "r" || len(d.Consulted) != 0 || d.ConsultedDropped != 0 {
		t.Fatalf("d = %+v, err = %v", d, err)
	}
}

func TestFilterConsulted(t *testing.T) {
	kept, dropped := filterConsulted([]verb.ConsultedRef{
		{Source: "src", Ref: "jira:1"}, {Source: " src ", Ref: "jira:1"}, {Source: "other", Ref: "x"}, {Source: "", Ref: "y"},
	}, []string{"src"})
	if len(kept) != 1 || kept[0] != (verb.ConsultedRef{Source: "src", Ref: "jira:1"}) || dropped != 2 {
		t.Errorf("kept %+v dropped %d", kept, dropped)
	}
}

// A step that names a source agents.toml does not declare cannot dispatch.
func TestContext_UndeclaredSourceNameRefusesTheDispatch(t *testing.T) {
	g, r := contextRig(t, `context = ["missing"]`)
	_, err := g.Gate(context.Background(), ctxItem(), "plan")
	if err == nil || !strings.Contains(err.Error(), `declares context source "missing"`) || !strings.Contains(err.Error(), "[context.missing]") {
		t.Fatalf("err = %v, want the undeclared source named", err)
	}
	if len(r.reqs) != 0 {
		t.Errorf("a dispatch ran despite the undeclared source: %d", len(r.reqs))
	}
}

// A cloud-bound performer cannot be handed a local server: a named error, and
// no header value anywhere in it.
func TestContext_CloudPerformerIsANamedError(t *testing.T) {
	g, _ := contextRig(t, `context = ["src"]`)
	g.SetNamedAgents(func(name string) (config.AgentBinding, bool) {
		if name == "worker" {
			return cloudBinding("claude -p {system}", cloudDocName, cloudTestLongLimit), true
		}
		return config.AgentBinding{}, false
	})
	_, err := g.DispatchExecutor(context.Background(), ctxItem(), "plan")
	if err == nil || !strings.Contains(err.Error(), "context sources not supported by claude/cloud") {
		t.Fatalf("err = %v, want a claude/cloud named error", err)
	}
	if strings.Contains(err.Error(), "Bearer") || strings.Contains(err.Error(), "tok") {
		t.Errorf("error leaks a header value: %v", err)
	}
	// The sibling step declares none, so a cloud performer there is not refused for it.
	if _, err := g.DispatchExecutor(context.Background(), ctxItem("in_progress"), "in_progress"); err != nil && strings.Contains(err.Error(), "context sources") {
		t.Errorf("sibling step refused for context: %v", err)
	}
}

// Unwired (no repo declares a source), the engine dispatches exactly as before.
func TestContext_UnwiredEngineOffersNothing(t *testing.T) {
	g, r := contextRig(t, `context = ["src"]`)
	g.SetContextSources(nil)
	if _, err := g.Gate(context.Background(), ctxItem(), "plan"); err != nil {
		t.Fatal(err)
	}
	for _, q := range r.reqs {
		if len(q.ContextServers) != 0 || strings.Contains(q.SystemPrompt, "## Context sources") {
			t.Errorf("an unwired engine offered context: %+v", q.ContextServers)
		}
	}
}
