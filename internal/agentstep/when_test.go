package agentstep

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// A scoped gate carrying `when` is enqueued only when its precondition check
// does not exit 1. Exit 0 runs it, exit 1 is the single skip code, and every
// other outcome runs it — a broken precondition must never cost a gate.

var whenWF = spineWF("", "", `[[gate]]
skill = "rev-when"
agent = "reviewer"
on = ["in_progress"]
when = "trigger"
`, "in_progress|executor", "done")

func whenCheckSkill() docindex.Doc {
	return docindex.Doc{Kind: "skills", Name: "trigger", Body: "---\nname: trigger\ntype: skill\ndescription: d\n---\n\n```check\nrule-in-the-script\n```\n"}
}

// exitErr is a check failure carrying an exit code, the shape exec.ExitError has.
type exitErr int

func (e exitErr) Error() string { return fmt.Sprintf("exit status %d", int(e)) }
func (e exitErr) ExitCode() int { return int(e) }

func runWhenGate(t *testing.T, docs fakeDocs, check func(string) (string, error)) (ran bool, recs []telemetryRec) {
	t.Helper()
	g, r := newEngine(t, `{"decision":"accept"}`, docs)
	got := captureTelemetry(g)
	var payload string
	g.check = func(_ context.Context, _, command, p string) (string, error) {
		if command != "rule-in-the-script" {
			t.Errorf("when ran %q, want the trigger skill's check", command)
		}
		payload = p
		return check(p)
	}
	if _, err := g.Gate(context.Background(), workitem.Item{ID: "sty_w", Status: "backlog"}, "in_progress"); err != nil {
		t.Fatal(err)
	}
	if payload != "" && !strings.Contains(payload, `"sty_w"`) {
		t.Errorf("when payload must carry the story, got %q", payload)
	}
	return r.got.SystemPrompt != "", *got
}

func whenEvents(recs []telemetryRec) []telemetryRec {
	var out []telemetryRec
	for _, r := range recs {
		if r.kind == "scoped-gate-skipped" {
			out = append(out, r)
		}
	}
	return out
}

func TestWhenExit0EnqueuesTheGate(t *testing.T) {
	docs := fakeDocs{workflow: whenWF, skillFound: true, skillBody: "rubric", extraSkills: []docindex.Doc{whenCheckSkill()}}
	ran, recs := runWhenGate(t, docs, func(string) (string, error) { return "changed 40 words", nil })
	if !ran {
		t.Fatal("exit 0 must enqueue the gate")
	}
	if ev := whenEvents(recs); len(ev) != 0 {
		t.Errorf("exit 0 must record no skip, got %#v", ev)
	}
}

func TestWhenExit1SkipsTheGateAndRecordsWhy(t *testing.T) {
	docs := fakeDocs{workflow: whenWF, skillFound: true, skillBody: "rubric", extraSkills: []docindex.Doc{whenCheckSkill()}}
	ran, recs := runWhenGate(t, docs, func(string) (string, error) { return "typo/whitespace-only (1 words)\n", exitErr(1) })
	if ran {
		t.Fatal("exit 1 must skip the gate")
	}
	ev := whenEvents(recs)
	if len(ev) != 1 {
		t.Fatalf("want one skip event, got %#v", ev)
	}
	d := ev[0].data
	if d["reason"] != "when" || d["skill"] != "rev-when" || d["when"] != "trigger" {
		t.Errorf("skip event = %#v", d)
	}
	if !strings.Contains(fmt.Sprint(d["detail"]), "typo/whitespace-only") {
		t.Errorf("skip event must carry the script's stdout, got %v", d["detail"])
	}
}

func TestWhenAnyOtherOutcomeRunsTheGate(t *testing.T) {
	cases := map[string]func(string) (string, error){
		"exit 2":       func(string) (string, error) { return "boom", exitErr(2) },
		"crash -1":     func(string) (string, error) { return "", exitErr(-1) },
		"exec failure": func(string) (string, error) { return "", errFake("fork/exec: no such file") },
	}
	for name, check := range cases {
		t.Run(name, func(t *testing.T) {
			docs := fakeDocs{workflow: whenWF, skillFound: true, skillBody: "rubric", extraSkills: []docindex.Doc{whenCheckSkill()}}
			ran, recs := runWhenGate(t, docs, check)
			if !ran {
				t.Fatal("a failing precondition must still run the gate")
			}
			ev := whenEvents(recs)
			if len(ev) != 1 || ev[0].data["reason"] != "when-error" {
				t.Errorf("want one when-error event, got %#v", ev)
			}
		})
	}
}

func TestWhenMissingOrCheckLessSkillRunsTheGate(t *testing.T) {
	t.Run("missing skill", func(t *testing.T) {
		rev := docindex.Doc{Kind: "skills", Name: "rev-when", Body: conformantSkill("rev-when", "rubric")}
		g, r := newEngine(t, `{"decision":"accept"}`, fakeDocs{workflow: whenWF, extraSkills: []docindex.Doc{rev}})
		recs := captureTelemetry(g)
		if _, err := g.Gate(context.Background(), workitem.Item{ID: "sty_w", Status: "backlog"}, "in_progress"); err != nil {
			t.Fatal(err)
		}
		if r.got.SystemPrompt == "" {
			t.Error("a missing when skill must run the gate")
		}
		if ev := whenEvents(*recs); len(ev) != 1 || ev[0].data["reason"] != "when-error" {
			t.Errorf("want one when-error event, got %#v", ev)
		}
	})
	t.Run("skill without a check", func(t *testing.T) {
		// skillFound resolves "trigger" to a plain rubric with no ```check block.
		g, r := newEngine(t, `{"decision":"accept"}`, fakeDocs{workflow: whenWF, skillFound: true, skillBody: "rubric"})
		recs := captureTelemetry(g)
		if _, err := g.Gate(context.Background(), workitem.Item{ID: "sty_w", Status: "backlog"}, "in_progress"); err != nil {
			t.Fatal(err)
		}
		if r.got.SystemPrompt == "" {
			t.Error("a when skill with no check must run the gate")
		}
		if ev := whenEvents(*recs); len(ev) != 1 || ev[0].data["reason"] != "when-error" {
			t.Errorf("want one when-error event, got %#v", ev)
		}
	})
}

// A `when` skill that does not resolve is reported with the route's other skill
// references, so the misconfiguration shows at validate time and not only as a
// gate that quietly runs every time.
func TestWhenSkillMustResolveInTheSkillAudit(t *testing.T) {
	_, step, ok := strings.Cut(whenWF, routeHalfSplit)
	if !ok {
		t.Fatal("fixture is not a packed route source")
	}
	doc := docindex.Doc{Kind: "workflows", Name: "step", Body: step}
	problems := WorkflowSkillProblems(doc, func(s string) bool { return s != "trigger" })
	if len(problems) != 1 || !strings.Contains(problems[0], `"trigger"`) {
		t.Fatalf("want one problem naming the when skill, got %v", problems)
	}
	if got := WorkflowSkillProblems(doc, func(string) bool { return true }); len(got) != 0 {
		t.Errorf("a resolving when skill must raise nothing, got %v", got)
	}
}

// The contract is about real process exit codes, so pin it against the shipped
// runner rather than only the fake: bash exit 1 skips, exit 2 does not.
func TestWhenContractAgainstRealExecCheck(t *testing.T) {
	for script, wantSkip := range map[string]bool{"exit 0": false, "exit 1": true, "exit 2": false} {
		t.Run(script, func(t *testing.T) {
			skill := docindex.Doc{Kind: "skills", Name: "trigger", Body: "---\nname: trigger\ntype: skill\ndescription: d\n---\n\n```check\n" + script + "\n```\n"}
			g, r := newEngine(t, `{"decision":"accept"}`, fakeDocs{workflow: whenWF, skillFound: true, skillBody: "rubric", extraSkills: []docindex.Doc{skill}})
			g.check = execCheck
			g.repoRoot = t.TempDir()
			if _, err := g.Gate(context.Background(), workitem.Item{ID: "sty_w", Status: "backlog"}, "in_progress"); err != nil {
				t.Fatal(err)
			}
			if ran := r.got.SystemPrompt != ""; ran == wantSkip {
				t.Errorf("%s: gate ran = %v, want %v", script, ran, !wantSkip)
			}
		})
	}
}
