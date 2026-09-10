// The validate half of sty_8e0b29a0 AC1: a step's `rework = { consult }` is an
// ALLOCATION (so the binding is not orphaned) and it carries the one obligation
// an advisor does not — it must be openable as a LIVE session.
package agentvalidate

import (
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/health"
)

const reworkDone = `["*"]
obligations = ["raised", "coded", "closed"]
`

func reworkStep(consult string) string {
	return `[raised]
status = "backlog"
start = true

[coded]
status = "in_progress"
agent = "coder"
requires = ["raised"]
rework = { consult = "` + consult + `", rounds = 3 }

[closed]
status = "done"
terminal = true
requires = ["coded"]
`
}

func reworkAgents(consultBinding config.AgentBinding) config.AgentsConfig {
	return config.AgentsConfig{
		Executor: config.AgentBinding{Command: "in-loop", Role: config.RoleAgent},
		Reviewer: config.AgentBinding{
			Role: config.RoleReviewer, Command: agentcli.DefaultClaudeCommand,
			Tools: "Read,Grep,Glob", Model: "opus",
		},
		Agents: map[string]config.AgentBinding{
			"coder": {
				Role: config.RoleAgent, Interface: agentcli.InterfaceStream,
				Command: agentcli.DefaultClaudeStreamCommand,
				Tools:   "Read,Grep,Glob,Edit,Write,Bash(satelle:*)", Model: "opus",
			},
			"consultant": consultBinding,
		},
	}
}

func liveConsultant() config.AgentBinding {
	return config.AgentBinding{
		Role: config.RoleReviewer, Interface: agentcli.InterfaceStream,
		Command: agentcli.DefaultClaudeStreamCommand, Tools: "Read,Grep,Glob", Model: "opus",
	}
}

func reworkFindings(t *testing.T, r Report, needle string) []health.Finding {
	t.Helper()
	var out []health.Finding
	for _, f := range r.Findings {
		if strings.Contains(f.Detail, needle) {
			out = append(out, f)
		}
	}
	return out
}

// A live consult binding is sound: allocated, not orphaned, no warning.
func TestValidate_ReworkConsultBindingIsAnAllocation(t *testing.T) {
	r := Validate(reworkAgents(liveConsultant()), nil, routeDocs(reworkDone, reworkStep("consultant")))
	if !r.OK() {
		t.Fatalf("a live rework consult must not produce hard problems: %v", r.Problems)
	}
	for _, w := range r.Warnings {
		if strings.Contains(w, "[consultant] is orphaned") {
			t.Errorf("a rework consult binding is an allocation and must not be reported orphaned: %v", r.Warnings)
		}
		if strings.Contains(w, "rework consult") {
			t.Errorf("a live consult binding must not warn: %q", w)
		}
	}
}

// A consult naming no binding at all WARNS by name — and does not refuse: a
// repo may author the loop before wiring the binding, and the relay is opened
// by hand, so the cost of a broken one is a clear error at `story rework`.
func TestValidate_ReworkConsultMissingBindingWarns(t *testing.T) {
	agents := reworkAgents(liveConsultant())
	delete(agents.Agents, "consultant")
	r := Validate(agents, nil, routeDocs(reworkDone, reworkStep("consultant")))
	if !r.OK() {
		t.Fatalf("a missing rework consult must WARN, never refuse: %v", r.Problems)
	}
	got := reworkFindings(t, r, "rework consult=consultant")
	if len(got) == 0 {
		t.Fatalf("no finding names the missing consult binding: %v", r.Warnings)
	}
	for _, f := range got {
		if f.Severity != health.SeverityWarn {
			t.Errorf("finding severity = %v, want warn: %+v", f.Severity, f)
		}
		if !strings.Contains(f.Detail, "no [consultant] binding") {
			t.Errorf("finding must name the missing section: %q", f.Detail)
		}
		if !strings.Contains(f.Detail, "rounds=3") {
			t.Errorf("finding must report the authored budget: %q", f.Detail)
		}
	}
}

// A consult binding that EXISTS but cannot be opened live warns for that
// reason, which is the obligation an advisor does not carry.
func TestValidate_ReworkConsultNotLiveCapableWarns(t *testing.T) {
	for _, tc := range []struct {
		name    string
		binding config.AgentBinding
		want    string
	}{
		{
			name: "in-loop",
			binding: config.AgentBinding{
				Role: config.RoleReviewer, Command: "in-loop",
			},
			want: "is command=in-loop",
		},
		{
			name: "interface=command",
			binding: config.AgentBinding{
				Role: config.RoleReviewer, Interface: agentcli.InterfaceCommand,
				Command: agentcli.DefaultClaudeCommand, Tools: "Read,Grep,Glob", Model: "opus",
			},
			want: "cannot open a session",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := Validate(reworkAgents(tc.binding), nil, routeDocs(reworkDone, reworkStep("consultant")))
			if !r.OK() {
				t.Fatalf("a non-live consult must WARN, never refuse: %v", r.Problems)
			}
			got := reworkFindings(t, r, "rework consult=consultant")
			if len(got) == 0 {
				t.Fatalf("no finding names the non-live consult: %v", r.Warnings)
			}
			var named bool
			for _, f := range got {
				if f.Severity != health.SeverityWarn {
					t.Errorf("finding severity = %v, want warn: %+v", f.Severity, f)
				}
				if strings.Contains(f.Detail, tc.want) {
					named = true
				}
			}
			if !named {
				t.Errorf("finding must say why it is not live-capable (%q): %+v", tc.want, got)
			}
		})
	}
}

// A route with NO rework key produces no rework finding at all — the AC1 no-op
// guarantee on the validate surface.
func TestValidate_NoReworkKeyNoReworkFinding(t *testing.T) {
	step := `[raised]
status = "backlog"
start = true

[coded]
status = "in_progress"
agent = "coder"
requires = ["raised"]

[closed]
status = "done"
terminal = true
requires = ["coded"]
`
	agents := reworkAgents(liveConsultant())
	delete(agents.Agents, "consultant") // nothing should ask about it
	r := Validate(agents, nil, routeDocs(reworkDone, step))
	if len(reworkFindings(t, r, "rework consult")) != 0 {
		t.Errorf("a route with no rework key must produce no rework finding: %v", r.Warnings)
	}
}
