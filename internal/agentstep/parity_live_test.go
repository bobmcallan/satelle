package agentstep

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/reviewscore"
	"github.com/bobmcallan/satelle/internal/structure"
	"github.com/bobmcallan/satelle/internal/wfdot"
	"github.com/bobmcallan/satelle/tests/reviewcorpus"
)

// TestParityLive is the bundled-vs-separate comparison of sty_23e10d92 AC7/AC8
// over the epic's frozen corpus (tests/reviewcorpus) — nothing else. It SPENDS
// REAL MONEY (a high-effort reviewer session per judgement) and needs the
// binding's agent CLI, so it only runs when SATELLE_PARITY=1:
//
//	SATELLE_PARITY=1 go test ./internal/agentstep -run TestParityLive -timeout 3h -v
//
// Every case is judged reviewcorpus.ParityRuns times as a separate session (its
// own rubric alone) and reviewcorpus.ParityRuns times as ONE bundled session
// carrying every LLM rubric its edge would bundle, through the real Gate path in
// both modes. It writes the per-rubric, per-run report and the per-edge outcome
// to $SATELLE_PARITY_OUT (default: the test's temp dir) and logs both.
//
//	SATELLE_PARITY_BINDING  the agents.toml section that judges (default reviewer);
//	                        run once per harness — claude command, grok command.
//	SATELLE_PARITY_WORKERS  concurrent sessions (default 4).
//
// The outcome (reviewcorpus.ParityVerdict) is advice for the author of a
// workflow's `bundle` key; nothing here writes one.
func TestParityLive(t *testing.T) {
	if os.Getenv("SATELLE_PARITY") != "1" {
		t.Skip("SATELLE_PARITY=1 not set — the live parity run spends real money")
	}
	root := parityRepoRoot(t)
	binding := os.Getenv("SATELLE_PARITY_BINDING")
	if binding == "" {
		binding = "reviewer"
	}
	workers := 4
	if v, err := strconv.Atoi(os.Getenv("SATELLE_PARITY_WORKERS")); err == nil && v > 0 {
		workers = v
	}

	corpus, err := reviewcorpus.Load(reviewcorpus.ParityCorpusRoot())
	if err != nil {
		t.Fatal(err)
	}
	docs := newLiveDocs(t, root)
	edges := parityEdges(t, root, docs)
	if len(edges) == 0 {
		t.Fatal("the workflow has no edge with two or more LLM gates — nothing to bundle")
	}

	var results []reviewcorpus.Result
	var mu sync.Mutex
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	edgeCases := map[string][]reviewcorpus.Case{}
	for _, e := range edges {
		cases := reviewcorpus.CasesForSkills(corpus, e.Skills)
		edgeCases[e.From+"→"+e.To] = cases
		for _, c := range cases {
			for run := 1; run <= reviewcorpus.ParityRuns; run++ {
				for _, mode := range []reviewcorpus.Mode{reviewcorpus.ModeSeparate, reviewcorpus.ModeBundled} {
					wg.Add(1)
					go func(e reviewcorpus.Edge, c reviewcorpus.Case, run int, mode reviewcorpus.Mode) {
						defer wg.Done()
						sem <- struct{}{}
						defer func() { <-sem }()
						v := liveJudge(t, root, docs, binding, e, c, mode)
						mu.Lock()
						results = append(results, reviewcorpus.Result{
							CaseID: c.ID, Rubric: c.Rubric, Skill: c.Skill, Label: c.Label, Expected: c.ExpectedVerdict,
							Mode: mode, Run: run, Verdict: v,
						})
						mu.Unlock()
						t.Logf("%s %s run %d %s → %q (expected %s)", c.ID, c.Skill, run, mode, v, c.ExpectedVerdict)
					}(e, c, run, mode)
				}
			}
		}
	}
	wg.Wait()

	rows := reviewcorpus.Pair(results)
	var report strings.Builder
	fmt.Fprintf(&report, "Binding: %s · runs per case per mode: %d\n\n", binding, reviewcorpus.ParityRuns)
	report.WriteString(reviewcorpus.Markdown(rows))
	report.WriteString("\n## Outcome per edge\n\n")
	for _, e := range edges {
		var edgeRows []reviewcorpus.Row
		for _, r := range rows {
			for _, c := range edgeCases[e.From+"→"+e.To] {
				if r.CaseID == c.ID {
					edgeRows = append(edgeRows, r)
					break
				}
			}
		}
		enable, reasons := reviewcorpus.ParityVerdict(edgeRows, e)
		fmt.Fprintf(&report, "### %s → %s (%s)\n\n", e.From, e.To, strings.Join(e.Skills, ", "))
		if enable {
			report.WriteString("ENABLE bundling by default: the bundle missed no known defect the separate sessions caught and added no false rejection.\n\n")
			continue
		}
		report.WriteString("STAYS OPT-IN:\n\n")
		for _, r := range reasons {
			report.WriteString("- " + r + "\n")
		}
		report.WriteString("\n")
	}
	out := os.Getenv("SATELLE_PARITY_OUT")
	if out == "" {
		out = t.TempDir()
	}
	path := filepath.Join(out, "parity-"+binding+".md")
	if err := os.WriteFile(path, []byte(report.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("report written to %s\n%s", path, report.String())
}

func parityRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, config.DefaultDataDir)); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no .satelle directory above the test's working directory")
		}
		dir = parent
	}
}

// liveDocs serves skills and principles from the repo's substrate, over the
// embedded defaults, so the live run judges with the rubrics a real gate would.
type liveDocs struct {
	skills     map[string]string
	principles map[string]string
}

func newLiveDocs(t *testing.T, root string) *liveDocs {
	t.Helper()
	d := &liveDocs{skills: map[string]string{}, principles: map[string]string{}}
	for _, e := range config.EmbeddedDefaults() {
		switch e.Kind {
		case "skills":
			d.skills[e.Name] = e.Body
		case "principles":
			d.principles[e.Name] = e.Body
		}
	}
	for kind, into := range map[string]map[string]string{"skills": d.skills, "principles": d.principles} {
		entries, err := os.ReadDir(filepath.Join(root, config.DefaultDataDir, kind))
		if err != nil {
			continue
		}
		for _, en := range entries {
			if en.IsDir() || !strings.HasSuffix(en.Name(), ".md") {
				continue
			}
			b, err := os.ReadFile(filepath.Join(root, config.DefaultDataDir, kind, en.Name()))
			if err != nil {
				t.Fatal(err)
			}
			into[strings.TrimSuffix(en.Name(), ".md")] = string(b)
		}
	}
	return d
}

func (d *liveDocs) Get(_ context.Context, kind, name string) (docindex.Doc, error) {
	m := d.skills
	if kind == "principles" {
		m = d.principles
	} else if kind != "skills" {
		return docindex.Doc{}, docindex.ErrNotFound
	}
	if b, ok := m[name]; ok {
		return docindex.Doc{Kind: kind, Name: name, Body: b}, nil
	}
	return docindex.Doc{}, docindex.ErrNotFound
}

func (d *liveDocs) List(_ context.Context, kind string) ([]docindex.Doc, error) {
	m := d.skills
	if kind == "principles" {
		m = d.principles
	} else if kind != "skills" {
		return nil, nil
	}
	var out []docindex.Doc
	for n, b := range m {
		out = append(out, docindex.Doc{Kind: kind, Name: n, Body: b})
	}
	return out, nil
}

// parityEdges lists the workflow's edges that carry two or more LLM gates — the
// ones bundling could change. A rubric that carries a check, or marks itself
// independent, never enters a bundle and is not listed.
func parityEdges(t *testing.T, root string, docs *liveDocs) []reviewcorpus.Edge {
	t.Helper()
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join(root, config.DefaultDataDir, "workflows", name))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	spec, err := wfdot.ParseRoute(read("done.toml"), read("step.toml"), "*", nil)
	if err != nil {
		t.Fatalf("derive the repo's route: %v", err)
	}
	var edges []reviewcorpus.Edge
	seen := map[string]bool{}
	for _, tr := range spec.Transitions {
		var llm []string
		for _, s := range tr.Skills {
			body, err := docs.Get(context.Background(), "skills", s)
			if err != nil || structure.CheckCommand(body.Body) != "" || structure.Independent(body.Body) {
				continue
			}
			llm = append(llm, s)
		}
		key := tr.From + "→" + tr.To
		if len(llm) < 2 || seen[key] {
			continue
		}
		seen[key] = true
		edges = append(edges, reviewcorpus.Edge{From: tr.From, To: tr.To, Skills: llm})
	}
	return edges
}

// liveJudge judges one case in one mode through the real Gate and returns the
// verdict for the case's OWN rubric ("" when the run produced none).
func liveJudge(t *testing.T, root string, docs *liveDocs, binding string, e reviewcorpus.Edge, c reviewcorpus.Case, mode reviewcorpus.Mode) reviewcorpus.Verdict {
	t.Helper()
	j, err := NewCaseJudge(CaseJudgeOptions{Root: root, Binding: binding, Docs: docs})
	if err != nil {
		t.Errorf("%v", err)
		return ""
	}
	var res reviewscore.JudgeResult
	if mode == reviewcorpus.ModeBundled {
		res, err = j.JudgeBundled(context.Background(), c, e.Skills)
	} else {
		res, err = j.Judge(context.Background(), c)
	}
	if err != nil {
		t.Logf("%s %s %s: %v", c.ID, c.Skill, mode, err)
		return ""
	}
	return res.Verdict
}
