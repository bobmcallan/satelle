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

// TestValidate_ReworkUnsetInterfaceLiveNoWarn (AC6, epic:model-selection child
// 2): a relay whose coder seat and consult binding both OMIT interface= is
// live-capable by resolution — no not-live-capable WARN — because validate
// judges the consult binding through LiveBinding (EffectiveBinding(UseLive)),
// the same seam OpenSessionAs opens it through.
func TestValidate_ReworkUnsetInterfaceLiveNoWarn(t *testing.T) {
	agents := reworkAgents(config.AgentBinding{
		Role: config.RoleReviewer, Command: agentcli.DefaultClaudeStreamCommand, Tools: "Read,Grep,Glob", Model: "opus",
	})
	agents.Agents["coder"] = config.AgentBinding{
		Role: config.RoleAgent, Command: agentcli.DefaultClaudeStreamCommand,
		Tools: "Read,Grep,Glob,Edit,Write,Bash(satelle:*)", Model: "opus",
	}
	r := Validate(agents, nil, routeDocs(reworkDone, reworkStep("consultant")))
	if !r.OK() {
		t.Fatalf("an unset-interface relay must not produce hard problems: %v", r.Problems)
	}
	for _, w := range r.Warnings {
		if strings.Contains(w, "not live-capable") {
			t.Errorf("an unset interface= must resolve live, not WARN: %q", w)
		}
	}
}

// TestValidate_GrantsReportInterfaceReason (AC5): each grant's Interface and
// InterfaceReason reflect explicit / one-shot / live-use resolution — a
// one-shot [reviewer] stays command, the live coder/consultant seats resolve
// to stream with reason "live use".
func TestValidate_GrantsReportInterfaceReason(t *testing.T) {
	agents := reworkAgents(config.AgentBinding{
		Role: config.RoleReviewer, Command: agentcli.DefaultClaudeStreamCommand, Tools: "Read,Grep,Glob", Model: "opus",
	})
	agents.Agents["coder"] = config.AgentBinding{
		Role: config.RoleAgent, Command: agentcli.DefaultClaudeStreamCommand,
		Tools: "Read,Grep,Glob,Edit,Write,Bash(satelle:*)", Model: "opus",
	}
	r := Validate(agents, nil, routeDocs(reworkDone, reworkStep("consultant")))
	byName := map[string]Grant{}
	for _, g := range r.Grants {
		byName[g.Name] = g
	}
	if g := byName["reviewer"]; g.Interface != config.InterfaceCommand || g.InterfaceReason != "one-shot default" {
		t.Errorf("reviewer grant = interface=%q reason=%q, want command/one-shot default", g.Interface, g.InterfaceReason)
	}
	if g := byName["coder"]; g.Interface != config.InterfaceStream || g.InterfaceReason != "live use" {
		t.Errorf("coder grant = interface=%q reason=%q, want stream/live use", g.Interface, g.InterfaceReason)
	}
	if g := byName["consultant"]; g.Interface != config.InterfaceStream || g.InterfaceReason != "live use" {
		t.Errorf("consultant grant = interface=%q reason=%q, want stream/live use", g.Interface, g.InterfaceReason)
	}
}

// TestValidate_GrantsAgreeWithRuntimeForLiveExecutor (sty_119f6fda): a
// rework.consult=executor with an unset [executor] command must report the
// SAME thing OpenSessionAs actually does — refused as in-loop — not "stream
// (live use)". The grant loop used to compute this from the bare RawBinding,
// which skips ExecutorBinding's in-loop default and disagreed with
// LiveBinding/the runtime.
func TestValidate_GrantsAgreeWithRuntimeForLiveExecutor(t *testing.T) {
	agents := reworkAgents(liveConsultant())
	agents.Executor = config.AgentBinding{} // no command authored — in-loop by default
	r := Validate(agents, nil, routeDocs(reworkDone, reworkStep("executor")))
	var found bool
	for _, g := range r.Grants {
		if g.Name != "executor" {
			continue
		}
		found = true
		if g.Interface != config.InterfaceCommand || g.InterfaceReason != "live use: in-loop" {
			t.Errorf("executor grant = interface=%q reason=%q, want command/live use: in-loop (matching the runtime refusal)", g.Interface, g.InterfaceReason)
		}
	}
	if !found {
		t.Fatal("no grant reported for executor")
	}
	got := reworkFindings(t, r, "rework consult=executor")
	if len(got) == 0 {
		t.Fatalf("no finding names the not-live-capable executor consult: %v", r.Warnings)
	}
}

// TestValidate_GrantsAgreeWithRuntimeForLiveReviewer (sty_119f6fda): a
// rework.consult=reviewer with no authored tools must report the SAME
// read-only ceiling ReviewerBinding()/LiveBinding give the reviewer
// elsewhere — not an empty grant computed from the bare RawBinding.
func TestValidate_GrantsAgreeWithRuntimeForLiveReviewer(t *testing.T) {
	agents := reworkAgents(liveConsultant())
	agents.Reviewer = config.AgentBinding{Role: config.RoleReviewer, Command: agentcli.DefaultClaudeStreamCommand} // no tools authored
	r := Validate(agents, nil, routeDocs(reworkDone, reworkStep("reviewer")))
	var found bool
	for _, g := range r.Grants {
		if g.Name != "reviewer" {
			continue
		}
		found = true
		if g.Tools != config.DefaultReviewerTools {
			t.Errorf("reviewer grant tools = %q, want %q (LiveBinding's default ceiling)", g.Tools, config.DefaultReviewerTools)
		}
		if g.Interface != config.InterfaceStream || g.InterfaceReason != "live use" {
			t.Errorf("reviewer grant = interface=%q reason=%q, want stream/live use", g.Interface, g.InterfaceReason)
		}
	}
	if !found {
		t.Fatal("no grant reported for reviewer")
	}
}

// TestValidate_LiveSeatWithNoCommandWarnsNamingTransports (sty_a762f3bd): a
// live-used seat that authors no command warns naming both live transports;
// the same seat with a command is clean.
func TestValidate_LiveSeatWithNoCommandWarnsNamingTransports(t *testing.T) {
	agents := reworkAgents(liveConsultant())
	agents.Agents["coder"] = config.AgentBinding{Role: config.RoleAgent, Tools: "Read,Grep,Glob,Edit,Write,Bash(satelle:*)"}
	r := Validate(agents, nil, routeDocs(reworkDone, reworkStep("consultant")))
	var named bool
	for _, w := range r.Warnings {
		if strings.Contains(w, "no command and no profile") && strings.Contains(w, "stream") && strings.Contains(w, "acp") {
			named = true
		}
	}
	if !named {
		t.Fatalf("no warning names the missing live command and both transports: %v", r.Warnings)
	}
	agents.Agents["coder"] = config.AgentBinding{Role: config.RoleAgent, Command: agentcli.DefaultClaudeStreamCommand, Tools: "Read,Grep,Glob,Edit,Write,Bash(satelle:*)"}
	r = Validate(agents, nil, routeDocs(reworkDone, reworkStep("consultant")))
	for _, w := range r.Warnings {
		if strings.Contains(w, "no command and no profile") {
			t.Errorf("a seat with a command must not warn: %q", w)
		}
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
