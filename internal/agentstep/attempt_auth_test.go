package agentstep

import (
	"context"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/workitem"
)

func attachAnything(g *Engine) {
	g.SetArtifactAttacher(func(_ context.Context, _ workitem.Item, name, typ, _ string) (string, string, error) {
		return name, typ, nil
	})
}

// Each performer agent-attempt row records the auth mode of the binding that ran
// the attempt — the escalated attempt its own binding's, not the primary's — and a
// binding that declares none records unrecorded.
func TestAttemptRowsRecordTheirBindingsAuth(t *testing.T) {
	run := func(t *testing.T, primaryAuth, strongAuth string) []attemptEvent {
		t.Helper()
		primary := &attemptRunner{runs: []attemptRun{{out: validAttempt("initial invalid")}, {out: validAttempt("repair invalid")}}}
		stronger := &attemptRunner{runs: []attemptRun{{out: validAttempt("## AC1\nstrong\n## AC2\nstrong")}}}
		g, events := attemptedEngine(t, attemptedDispatchSkill, primary, stronger)
		g.SetNamedAgents(func(name string) (config.AgentBinding, bool) {
			switch name {
			case "architect":
				return config.AgentBinding{Command: "primary", Tools: "read_file", Model: "economy", Auth: primaryAuth}, true
			case "stronger":
				return config.AgentBinding{Command: "stronger", Tools: "read_file", Model: "strong", Auth: strongAuth}, true
			}
			return config.AgentBinding{}, false
		})
		attachAnything(g)
		if _, err := g.DispatchExecutor(context.Background(), attemptedItem(), "plan"); err != nil {
			t.Fatal(err)
		}
		return *events
	}
	for name, tc := range map[string]struct {
		primary, strong string
		want            []string
	}{
		"declared":   {config.AuthAPIKey, config.AuthOrg, []string{"api_key", "api_key", "org"}},
		"undeclared": {"", "", []string{"unrecorded", "unrecorded", "unrecorded"}},
	} {
		t.Run(name, func(t *testing.T) {
			events := run(t, tc.primary, tc.strong)
			if len(events) != len(tc.want) {
				t.Fatalf("events = %d, want %d: %#v", len(events), len(tc.want), events)
			}
			for i, want := range tc.want {
				if got := events[i].data["auth_mode"]; got != want {
					t.Errorf("attempt row %d (%v) auth_mode = %v, want %q", i, events[i].data["phase"], got, want)
				}
			}
		})
	}
}

// A dispatch result and every gate verdict a seat's binding produced carry the
// binding's auth, a bundled one on each of its verdicts (the usage rides the first
// alone). A binding that declares none is unrecorded.
func TestGateVerdictsRecordTheirSeatsAuth(t *testing.T) {
	for name, auth := range map[string]struct{ declared, want string }{
		"declared":   {config.AuthOrg, "org"},
		"undeclared": {"", "unrecorded"},
	} {
		t.Run(name, func(t *testing.T) {
			docs := fakeDocs{workflow: bundleEdgeWF("rev-a,rev-b,rev-c", "bundle = true"), extraSkills: bundleSkillDocs("rev-a", "rev-b", "rev-c")}
			g := New(&bundleRunner{}, docs, "/repo", "")
			g.reviewerBinding.Auth = auth.declared
			dec, err := bundleGate(t, g)
			if err != nil || len(dec.Reviewers) != 3 {
				t.Fatalf("dec=%+v err=%v", dec, err)
			}
			for _, rv := range dec.Reviewers {
				if rv.AuthMode != auth.want {
					t.Errorf("verdict %s auth_mode = %q, want %q", rv.Skill, rv.AuthMode, auth.want)
				}
			}
		})
	}
}

// A pi performer whose run left no session record records an adapter-named
// usage-unavailable reason beside its model on every attempt row, and an
// available usage records the figures — never a silent zero.
func TestPiAttemptRowsRecordModelAndUsageOrNamedReason(t *testing.T) {
	const reason = "pi adapter: no session record written in the run window"
	pi := adapterUsageRunner{
		attemptRunner: &attemptRunner{runs: []attemptRun{{out: validAttempt("draft")}, {out: validAttempt("## AC1\nfixed\n## AC2\nfixed")}}},
		usage:         agentcli.UsageResult{Adapter: agentcli.HarnessPi, UnavailableReason: reason},
	}
	g, events := attemptedEngine(t, attemptedDispatchSkill, nil, nil)
	g.newRunner = func(string, string) (agentcli.Runner, error) { return pi, nil }
	attachAnything(g)
	if _, err := g.DispatchExecutor(context.Background(), attemptedItem(), "plan"); err != nil {
		t.Fatal(err)
	}
	if len(*events) != 2 {
		t.Fatalf("events = %#v", *events)
	}
	for i, ev := range *events {
		d := ev.data
		if d["adapter"] != agentcli.HarnessPi || d["usage_available"] != false || d["usage_unavailable_reason"] != reason {
			t.Errorf("attempt row %d = %#v, want an unavailable usage naming the pi adapter", i, d)
		}
		if d["model"] != "economy" || d["model_resolved"] == "" || d["model_resolved"] == nil {
			t.Errorf("attempt row %d model=%v model_resolved=%v, want both recorded", i, d["model"], d["model_resolved"])
		}
	}
}

// A criterion wrapped over two lines fails the artifact contract, and the
// agent-attempt row of that failed attempt records the model that produced it and
// the validator's findings — the evidence a per-model contract failure needs.
func TestFailedContractAttemptRowRecordsModelAndFindings(t *testing.T) {
	skill := strings.Replace(attemptedDispatchSkill, "output_ac_coverage: true", "output_criteria_section: Acceptance criteria", 1)
	primary := &attemptRunner{runs: []attemptRun{
		{out: validAttempt("## Acceptance criteria\n1. first criterion that wraps\n   onto a second line\n2. second")},
		{out: validAttempt("## Acceptance criteria\n1. first criterion\n2. second")},
	}}
	g, events := attemptedEngine(t, skill, primary, &attemptRunner{})
	attachAnything(g)
	if _, err := g.DispatchExecutor(context.Background(), attemptedItem(), "plan"); err != nil {
		t.Fatal(err)
	}
	if len(*events) != 2 {
		t.Fatalf("events = %#v", *events)
	}
	failed, repaired := (*events)[0].data, (*events)[1].data
	if failed["validator_ok"] != false || failed["model"] != "economy" || failed["model_resolved"] == nil || failed["model_resolved"] == "" {
		t.Fatalf("failed attempt row = %#v, want validator_ok=false with the model recorded", failed)
	}
	findings, _ := failed["validator_findings"].(string)
	if !strings.Contains(findings, `"## Acceptance criteria" line 2 is not a numbered criterion`) || !strings.Contains(findings, "onto a second line") {
		t.Errorf("validator_findings = %q, want the wrapped line named", findings)
	}
	if repaired["validator_ok"] != true {
		t.Errorf("repair row = %#v, want validator_ok=true", repaired)
	}
	if _, set := repaired["validator_findings"]; set {
		t.Errorf("a passing attempt records no findings: %#v", repaired)
	}
}
