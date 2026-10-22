package agentstep

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// sty_23ceeca2: every step skill delivered to a performer is recorded by name and
// content hash; one that does not resolve is recorded undelivered; and the
// workflow's skill_evidence key decides whether that refuses or is only recorded.

// shrinkingDocs is fakeDocs with skills that can be taken away after the engine
// is built — the gap the engagement guard does not cover.
type shrinkingDocs struct {
	fakeDocs
	gone map[string]bool
}

func (d shrinkingDocs) Get(ctx context.Context, kind, name string) (docindex.Doc, error) {
	if kind == "skills" && d.gone[name] {
		return docindex.Doc{}, docindex.ErrNotFound
	}
	return d.fakeDocs.Get(ctx, kind, name)
}

// withSkillEvidence sets the route source's skill_evidence key in a packed fixture.
func withSkillEvidence(wf, mode string) string {
	return strings.Replace(wf, "scope = \"system\"\n\n", "scope = \"system\"\nskill_evidence = \""+mode+"\"\n\n", 1)
}

func evSkillDoc(name, hash, extraFM, body string) docindex.Doc {
	return docindex.Doc{Kind: "skills", Name: name, Hash: hash,
		Body: "---\nname: " + name + "\ntype: skill\ndescription: test\n" + extraFM + "---\n\n" + body + "\n"}
}

// eventRunner is a runner that reports tool events through the request's OnEvent,
// as a stream transport does, then prints out.
type eventRunner struct {
	out    string
	events []agentcli.Event
}

func (r *eventRunner) Name() string    { return "event" }
func (r *eventRunner) Command() string { return "event -p {system}" }
func (r *eventRunner) Run(_ context.Context, req agentcli.Request) ([]byte, error) {
	for _, ev := range r.events {
		if req.OnEvent != nil {
			req.OnEvent(ev)
		}
	}
	return []byte(r.out), nil
}

func toolStart(tool, command string) agentcli.Event {
	return agentcli.Event{Kind: agentcli.EventToolStart, Tool: tool, Command: command}
}

var performerBindings = map[string]config.AgentBinding{
	"architect": {Command: "fake -p {system}", Tools: "Read,Bash(satelle:*)"},
	"streamer":  {Command: "fake -p {system}", Interface: "stream", Tools: "Read,Bash(satelle:*)"},
	"operator":  {Command: "in-loop"},
}

func engineFor(t *testing.T, docs fakeDocs, gone map[string]bool, runner agentcli.Runner) *Engine {
	t.Helper()
	g := New(runner, shrinkingDocs{fakeDocs: docs, gone: gone}, "/repo", "")
	g.SetNamedAgents(func(name string) (config.AgentBinding, bool) {
		b, ok := performerBindings[name]
		return b, ok
	})
	g.newRunner = func(string, string) (agentcli.Runner, error) { return runner, nil }
	return g
}

func twoSkills() []docindex.Doc {
	return []docindex.Doc{
		evSkillDoc("skill-a", "hash-of-a", "", "rubric A"),
		evSkillDoc("skill-b", "hash-of-b", "", "rubric B"),
	}
}

// AC1: a dispatched performer's result records each skill delivered, name and the
// index's content hash.
func TestDispatchedStepRecordsEachDeliveredSkill(t *testing.T) {
	wf := spineWF("", "", "", "named|architect|skill-a", "done")
	g := engineFor(t, fakeDocs{workflow: wf, extraSkills: twoSkills()}, nil, &fakeRunner{out: "ok"})
	res, err := g.DispatchExecutor(context.Background(), workitem.Item{ID: "sty_1", Status: "backlog"}, "named")
	if err != nil {
		t.Fatal(err)
	}
	want := verb.SkillDelivery{Name: "skill-a", Hash: "hash-of-a", Delivered: true}
	if len(res.Skills) != 1 || res.Skills[0] != want {
		t.Fatalf("Skills = %+v, want [%+v]", res.Skills, want)
	}
}

// AC1: an index that carries no hash still yields a sha256 of the body that was
// delivered, so a delivery is always checkable.
func TestDeliveredSkillHashFallsBackToTheBodyDigest(t *testing.T) {
	docs := fakeDocs{workflow: spineWF("", "", "", "named|architect|plain", "done"), skillFound: true, skillBody: "rubric"}
	g := engineFor(t, docs, nil, &fakeRunner{out: "ok"})
	res, err := g.DispatchExecutor(context.Background(), workitem.Item{ID: "sty_1", Status: "backlog"}, "named")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Skills) != 1 || len(res.Skills[0].Hash) != 64 {
		t.Fatalf("Skills = %+v, want one entry with a 64-hex sha256", res.Skills)
	}
}

// AC1: the in-loop executor step and a named in-loop binding each hand back the
// skills they deliver to the driving session — name, hash and the rubric.
func TestInLoopStepsDeliverTheirSkills(t *testing.T) {
	wf := spineWF("", "", "", "exec|executor|skill-a", "inloop|operator|skill-b", "done")
	g := engineFor(t, fakeDocs{workflow: wf, extraSkills: twoSkills()}, nil, &fakeRunner{out: "ok"})
	for _, tc := range []struct {
		to, skill, hash string
		kind            verb.PerformerKind
	}{
		{"exec", "skill-a", "hash-of-a", verb.PerformerExecutor},
		{"inloop", "skill-b", "hash-of-b", verb.PerformerInLoop},
	} {
		res, err := g.DispatchExecutor(context.Background(), workitem.Item{ID: "sty_1", Status: "backlog"}, tc.to)
		if err != nil {
			t.Fatalf("%s: %v", tc.to, err)
		}
		if res.Dispatched || res.Kind != tc.kind {
			t.Errorf("%s: Dispatched=%v Kind=%q, want not dispatched, %q", tc.to, res.Dispatched, res.Kind, tc.kind)
		}
		if len(res.Skills) != 1 || res.Skills[0].Name != tc.skill || res.Skills[0].Hash != tc.hash || !res.Skills[0].Delivered {
			t.Errorf("%s: Skills = %+v, want %s (%s) delivered", tc.to, res.Skills, tc.skill, tc.hash)
		}
		if !strings.Contains(res.InLoopRubric, "rubric "+strings.ToUpper(tc.skill[len(tc.skill)-1:])) {
			t.Errorf("%s: InLoopRubric = %q, want the skill's text", tc.to, res.InLoopRubric)
		}
	}
	// resolvePerformer carries the route on an in-loop answer, so the skills can be named.
	perf, err := g.resolvePerformer(context.Background(), workitem.Item{ID: "sty_1", Status: "backlog"}, "exec", false)
	if err != nil || perf.target == nil || len(perf.spec.States) == 0 {
		t.Fatalf("an executor answer must carry its spec and target: %+v, %v", perf, err)
	}
}

// AC2: a skill removed after engagement is recorded undelivered. By default
// (record) the step still runs and the gap is on the result; under
// skill_evidence = reject a dispatched step is refused and an in-loop delivery is
// marked strict for the verb to refuse. Never silent.
func TestUndeliveredSkillIsRecordedAndRefusedPerConfiguration(t *testing.T) {
	gone := map[string]bool{"skill-a": true}
	steps := []string{"named|architect|skill-a", "exec|executor|skill-a", "done"}
	item := workitem.Item{ID: "sty_1", Status: "backlog"}

	t.Run("dispatched, record", func(t *testing.T) {
		g := engineFor(t, fakeDocs{workflow: spineWF("", "", "", steps...), extraSkills: twoSkills()}, gone, &fakeRunner{out: "ok"})
		res, err := g.DispatchExecutor(context.Background(), item, "named")
		if err != nil {
			t.Fatalf("record mode must run the step: %v", err)
		}
		if len(res.Skills) != 1 || res.Skills[0].Delivered || res.Skills[0].Name != "skill-a" || res.Skills[0].Reason == "" {
			t.Fatalf("Skills = %+v, want skill-a recorded undelivered with a reason", res.Skills)
		}
		if res.Evidence == nil || len(res.Evidence.Undelivered) != 1 || res.Evidence.Undelivered[0] != "skill-a" || res.Evidence.Blocks() {
			t.Errorf("Evidence = %+v, want skill-a undelivered and not blocking", res.Evidence)
		}
	})
	t.Run("dispatched, reject", func(t *testing.T) {
		wf := withSkillEvidence(spineWF("", "", "", steps...), "reject")
		g := engineFor(t, fakeDocs{workflow: wf, extraSkills: twoSkills()}, gone, &fakeRunner{out: "ok"})
		res, err := g.DispatchExecutor(context.Background(), item, "named")
		if err == nil || !strings.Contains(err.Error(), "skill-a") || !strings.Contains(err.Error(), "did not resolve") {
			t.Fatalf("reject mode must refuse, naming the skill: %v", err)
		}
		if len(res.Skills) != 1 || res.Skills[0].Delivered {
			t.Errorf("the refusal must still carry the manifest: %+v", res.Skills)
		}
	})
	t.Run("in-loop, record then reject", func(t *testing.T) {
		g := engineFor(t, fakeDocs{workflow: spineWF("", "", "", steps...), extraSkills: twoSkills()}, gone, &fakeRunner{out: "ok"})
		res, err := g.DispatchExecutor(context.Background(), item, "exec")
		if err != nil || res.StrictSkills || len(verb.UndeliveredSkills(res.Skills)) != 1 {
			t.Fatalf("record: res=%+v err=%v, want skill-a undelivered and not strict", res, err)
		}
		wf := withSkillEvidence(spineWF("", "", "", steps...), "reject")
		g = engineFor(t, fakeDocs{workflow: wf, extraSkills: twoSkills()}, gone, &fakeRunner{out: "ok"})
		res, err = g.DispatchExecutor(context.Background(), item, "exec")
		if err != nil || !res.StrictSkills || len(verb.UndeliveredSkills(res.Skills)) != 1 {
			t.Fatalf("reject: res=%+v err=%v, want skill-a undelivered and strict", res, err)
		}
	})
}

const evidenceFM = "evidence_sections: Premise check, Existing owner\nevidence_commands: go test\n"

// AC4 (dispatched): a met declaration, an unmet one naming each missing item, and
// an unverifiable one naming the adapter.
func TestDispatchedStepEvidenceCheck(t *testing.T) {
	skills := []docindex.Doc{evSkillDoc("ev", "h", evidenceFM, "rubric")}
	wf := spineWF("", "", "", "streamed|streamer|ev", "plain|architect|ev", "done")
	item := workitem.Item{ID: "sty_1", Status: "backlog"}
	statusOf := func(r *verb.EvidenceReport, kind, name string) string {
		for _, i := range r.Items {
			if i.Kind == kind && i.Name == name {
				return i.Status + "|" + i.Detail
			}
		}
		return "absent"
	}

	t.Run("met", func(t *testing.T) {
		runner := &eventRunner{out: "## Premise check\nx\n## Existing owner\ny", events: []agentcli.Event{toolStart("Bash", "cd /r && go test ./...")}}
		g := engineFor(t, fakeDocs{workflow: wf, extraSkills: skills}, nil, runner)
		res, err := g.DispatchExecutor(context.Background(), item, "streamed")
		if err != nil {
			t.Fatal(err)
		}
		if res.Evidence == nil || len(res.Evidence.Gaps()) != 0 || len(res.Evidence.Items) != 3 {
			t.Fatalf("Evidence = %+v, want 3 items, no gaps", res.Evidence)
		}
		if v := res.Evidence.Verdict(0); !v.Accept || !v.System || v.Skill != verb.SkillEvidenceCheck {
			t.Errorf("verdict = %+v, want an accepting system verdict named skill-evidence", v)
		}
	})
	t.Run("unmet names each missing item", func(t *testing.T) {
		runner := &eventRunner{out: "## Premise check\nx", events: []agentcli.Event{toolStart("Bash", "ls")}}
		g := engineFor(t, fakeDocs{workflow: wf, extraSkills: skills}, nil, runner)
		res, err := g.DispatchExecutor(context.Background(), item, "streamed")
		if err != nil {
			t.Fatal(err)
		}
		gaps := strings.Join(res.Evidence.Gaps(), "\n")
		for _, want := range []string{`ev: missing section "Existing owner"`, `ev: missing command "go test"`} {
			if !strings.Contains(gaps, want) {
				t.Errorf("gaps %q lack %q", gaps, want)
			}
		}
		if strings.Contains(gaps, "Premise check") {
			t.Errorf("a met section was reported: %q", gaps)
		}
	})
	t.Run("unverifiable names the adapter", func(t *testing.T) {
		g := engineFor(t, fakeDocs{workflow: wf, extraSkills: skills}, nil, &fakeRunner{out: "## Premise check\n## Existing owner"})
		res, err := g.DispatchExecutor(context.Background(), item, "plain")
		if err != nil {
			t.Fatal(err)
		}
		got := statusOf(res.Evidence, verb.EvidenceCommand, "go test")
		if !strings.HasPrefix(got, verb.EvidenceUnverifiable+"|adapter ") || !strings.Contains(got, "reports no tool events") {
			t.Errorf("command item = %q, want unverifiable naming the adapter", got)
		}
		if len(res.Evidence.Gaps()) != 0 {
			t.Errorf("an unverifiable item must not be a gap: %v", res.Evidence.Gaps())
		}
	})
}

// AC4 / AC5 (in-loop): the departure from a step the driving session performed is
// checked against the documents attached and the hook-recorded trace; a gap is
// recorded by default and refuses under reject; an item the harness's hooks
// cannot see is unverifiable and never refuses.
func TestInLoopStepEvidenceCheck(t *testing.T) {
	skills := []docindex.Doc{evSkillDoc("ev", "h", evidenceFM+"evidence_tools: Read\n", "rubric")}
	steps := []string{"exec|executor|ev", "next||", "done"}
	item := workitem.Item{ID: "sty_1", Status: "exec"}
	build := func(mode string, doc string, tr ToolTrace) *Engine {
		wf := spineWF("", "", "", steps...)
		if mode != "" {
			wf = withSkillEvidence(wf, mode)
		}
		g := engineFor(t, fakeDocs{workflow: wf, extraSkills: skills}, nil, &fakeRunner{out: "ok"})
		g.SetDocsResolver(func(context.Context, string) []DocState { return []DocState{{Name: "notes", Body: doc}} })
		g.SetToolTraceResolver(func(context.Context, string, string) ToolTrace { return tr })
		return g
	}
	both := "## Premise check\n## Existing owner\n"
	goTest := ToolTrace{Harness: agentcli.HarnessClaude, Calls: []ToolCall{{Tool: "Bash", Command: "go test ./..."}}}
	verdictOf := func(t *testing.T, dec verb.GateDecision) verb.ReviewerVerdict {
		t.Helper()
		for _, v := range dec.Reviewers {
			if v.Skill == verb.SkillEvidenceCheck {
				return v
			}
		}
		t.Fatalf("no skill-evidence verdict in %+v", dec.Reviewers)
		return verb.ReviewerVerdict{}
	}

	t.Run("met", func(t *testing.T) {
		dec, err := build("reject", both, goTest).Gate(context.Background(), item, "next")
		if err != nil || !dec.Accept {
			t.Fatalf("dec=%+v err=%v, want an accepted edge", dec, err)
		}
		v := verdictOf(t, dec)
		// evidence_tools: Read is not seen by claude's hooks, so it is unverifiable, not a gap.
		if !v.Accept || !v.System || !strings.Contains(v.Notes, "unverifiable") {
			t.Errorf("verdict = %+v, want accept, with Read reported unverifiable", v)
		}
	})
	t.Run("unmet is recorded by default", func(t *testing.T) {
		dec, err := build("", "## Premise check\n", ToolTrace{Harness: agentcli.HarnessClaude}).Gate(context.Background(), item, "next")
		if err != nil || !dec.Accept {
			t.Fatalf("record mode must not refuse: dec=%+v err=%v", dec, err)
		}
		v := verdictOf(t, dec)
		if !v.Accept || !strings.Contains(v.Notes, `missing section "Existing owner"`) || !strings.Contains(v.Notes, `missing command "go test"`) {
			t.Errorf("notes = %q, want both gaps named", v.Notes)
		}
	})
	t.Run("unmet refuses under reject", func(t *testing.T) {
		dec, err := build("reject", "## Premise check\n", ToolTrace{Harness: agentcli.HarnessClaude}).Gate(context.Background(), item, "next")
		if err != nil || dec.Accept {
			t.Fatalf("reject mode must refuse: dec=%+v err=%v", dec, err)
		}
		if v := verdictOf(t, dec); v.Accept || !strings.Contains(dec.Notes, `missing command "go test"`) {
			t.Errorf("verdict=%+v notes=%q", v, dec.Notes)
		}
	})
	t.Run("unverifiable never refuses", func(t *testing.T) {
		// pi's hooks see edit|write and bash: Read is outside them. An unknown
		// harness has no trace channel at all.
		for _, tr := range []ToolTrace{{Harness: "pi", Calls: []ToolCall{{Tool: "bash", Command: "go test ./..."}}}, {}} {
			dec, err := build("reject", both, tr).Gate(context.Background(), item, "next")
			if err != nil || !dec.Accept {
				t.Fatalf("trace %+v: dec=%+v err=%v, want accepted", tr, dec, err)
			}
			notes := verdictOf(t, dec).Notes
			want := "adapter " + tr.Harness
			if tr.Harness == "" {
				want = "no trace channel"
			}
			if !strings.Contains(notes, "unverifiable") || !strings.Contains(notes, want) {
				t.Errorf("trace %+v: notes %q should say unverifiable and %q", tr, notes, want)
			}
		}
	})
	t.Run("a back-edge is never refused for evidence, the forward exit is", func(t *testing.T) {
		wf := withSkillEvidence(spineWF("", "", "", "first||", "exec|executor|ev", "next||", "done"), "reject")
		wf = regexp.MustCompile(`(obligations = \[[^\n]*\]\n)`).ReplaceAllString(wf, "${1}recover = { step = \"first\", from = [\"exec\"] }\n")
		g := engineFor(t, fakeDocs{workflow: wf, extraSkills: skills}, nil, &fakeRunner{out: "ok"})
		g.SetDocsResolver(func(context.Context, string) []DocState { return nil })
		g.SetToolTraceResolver(func(context.Context, string, string) ToolTrace { return ToolTrace{Harness: agentcli.HarnessClaude} })
		back, err := g.Gate(context.Background(), item, "first")
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range back.Reviewers {
			if v.Skill == verb.SkillEvidenceCheck {
				t.Errorf("rework to an earlier step must not be judged for evidence: %+v", v)
			}
		}
		if !back.Accept && back.Gated && back.Skill == verb.SkillEvidenceCheck {
			t.Errorf("the back-edge was refused by the evidence check: %+v", back)
		}
		fwd, err := g.Gate(context.Background(), item, "next")
		if err != nil || fwd.Accept {
			t.Fatalf("the forward exit with nothing produced must still be refused: dec=%+v err=%v", fwd, err)
		}
	})
	t.Run("a document from an earlier step does not satisfy the section", func(t *testing.T) {
		wf := spineWF("", "", "", steps...)
		g := engineFor(t, fakeDocs{workflow: wf, extraSkills: skills}, nil, &fakeRunner{out: "ok"})
		g.SetDocsResolver(func(context.Context, string) []DocState {
			return []DocState{{Name: "plan", Body: both}, {Name: "step-summary", Body: "## Premise check\n"}}
		})
		scoped := func(docs ...string) { // the documents attached since the step was entered
			g.SetToolTraceResolver(func(context.Context, string, string) ToolTrace {
				return ToolTrace{Harness: agentcli.HarnessClaude, Docs: docs, DocsScoped: true}
			})
		}
		scoped("step-summary") // the old plan, which has both sections, predates the step
		dec, err := g.Gate(context.Background(), item, "next")
		if err != nil {
			t.Fatal(err)
		}
		if notes := verdictOf(t, dec).Notes; !strings.Contains(notes, `missing section "Existing owner"`) {
			t.Errorf("notes = %q, want the section the old plan carries reported missing", notes)
		}
		scoped("plan", "step-summary")
		dec, err = g.Gate(context.Background(), item, "next")
		if err != nil {
			t.Fatal(err)
		}
		if notes := verdictOf(t, dec).Notes; strings.Contains(notes, `missing section`) {
			t.Errorf("notes = %q, want the section met once the document was attached in the step", notes)
		}
	})
	t.Run("a headless run's recorded stream supplies the commands", func(t *testing.T) {
		g := build("reject", both, ToolTrace{
			Harness: "claude", Stream: true, StreamCommands: true, StreamTools: true,
			Calls: []ToolCall{{Tool: "Bash", Command: "go test ./..."}, {Tool: "Read"}},
		})
		dec, err := g.Gate(context.Background(), item, "next")
		if err != nil || !dec.Accept {
			t.Fatalf("dec=%+v err=%v, want accepted: the stream shows go test and Read", dec, err)
		}
		if notes := verdictOf(t, dec).Notes; strings.Contains(notes, "unverifiable") {
			t.Errorf("notes = %q, want nothing unverifiable when the stream covered commands and tools", notes)
		}
	})
	t.Run("a dispatched step is not re-checked on departure", func(t *testing.T) {
		wf := spineWF("", "", "", "exec|architect|ev", "next||", "done")
		g := engineFor(t, fakeDocs{workflow: wf, extraSkills: skills}, nil, &fakeRunner{out: "ok"})
		dec, err := g.Gate(context.Background(), item, "next")
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range dec.Reviewers {
			if v.Skill == verb.SkillEvidenceCheck {
				t.Errorf("a dispatched step's check ran at dispatch, not departure: %+v", v)
			}
		}
	})
}

// A headless executor step is judged like a dispatched one, against its own run
// stream and the documents it attached since the step was entered, and hands the
// stream back to be recorded for the step's exit gate. A step that declares no
// evidence hands back no trace.
func TestPerformExecutorStepChecksEvidenceAndCarriesItsStream(t *testing.T) {
	skills := []docindex.Doc{evSkillDoc("ev", "h", evidenceFM, "rubric"), evSkillDoc("plain", "p", "", "rubric")}
	build := func(skill string) *Engine {
		wf := spineWF("", "", "", "exec|executor|"+skill, "done")
		runner := &eventRunner{out: "## Premise check\n", events: []agentcli.Event{toolStart("Bash", "go test ./...")}}
		g := engineFor(t, fakeDocs{workflow: wf, extraSkills: skills}, nil, runner)
		g.SetOneShotAgents(func(string) (config.AgentBinding, bool) {
			return config.AgentBinding{Command: "fake -p {system}", Interface: "stream", Tools: "Read,Bash(satelle:*)"}, true
		})
		g.SetDocsResolver(func(context.Context, string) []DocState {
			return []DocState{{Name: "plan", Body: "## Existing owner\n"}, {Name: "old", Body: "## Existing owner\n## Premise check\n"}}
		})
		g.SetToolTraceResolver(func(context.Context, string, string) ToolTrace {
			return ToolTrace{Docs: []string{"plan"}, DocsScoped: true}
		})
		return g
	}
	res, err := build("ev").PerformExecutorStep(context.Background(), workitem.Item{ID: "sty_1", Status: "exec"}, "exec", "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Evidence == nil || len(res.Evidence.Gaps()) != 0 {
		t.Fatalf("evidence = %+v, want every item met: the section is in the attached document, the command on the stream", res.Evidence)
	}
	if res.Trace == nil || !res.Trace.CommandsCovered || len(res.Trace.Calls) != 1 || res.Trace.Calls[0].Command != "go test ./..." {
		t.Errorf("trace = %+v, want the run's go test with its coverage", res.Trace)
	}
	none, err := build("plain").PerformExecutorStep(context.Background(), workitem.Item{ID: "sty_1", Status: "exec"}, "exec", "")
	if err != nil {
		t.Fatal(err)
	}
	if none.Trace != nil || none.Evidence != nil {
		t.Errorf("a step declaring no evidence must hand back no trace or report, got %+v / %+v", none.Trace, none.Evidence)
	}
}

func TestCommandSegmentsAndMatching(t *testing.T) {
	calls := []ToolCall{{Tool: "Bash", Command: "FOO=1 cd /r && (go test ./... | tail -3); echo done"}}
	for prefix, want := range map[string]bool{"go test": true, "go": true, "cd": true, "echo done": true, "go vet": false, "got": false} {
		if got := commandRan(calls, prefix); got != want {
			t.Errorf("commandRan(%q) = %v, want %v", prefix, got, want)
		}
	}
	if !hasSection("# Plan\n### Existing Owner\n", "existing owner") || hasSection("text Existing owner", "Existing owner") {
		t.Error("hasSection must match a heading, case-insensitively, and only a heading")
	}
	keys := topLevelKeys(`prose {not json} then {"decision":"accept","nested":{"x":1}} tail`)
	if !keys["decision"] || !keys["nested"] || keys["x"] {
		t.Errorf("topLevelKeys = %v, want the final object's top-level keys", keys)
	}
}
