// Two allocation checks that used to report a live configuration as broken
// (sty_338a53f8): an agent bound only on an EDGE/advisor was called orphaned,
// and a reviewer's shell grant was called unused while a reviewer skill shelled
// `satelle` with it.
package agentvalidate

import (
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/health"
)

// advisorAgents is a route whose ONLY reference to two bindings is an advisor —
// done.toml's park `advisor` and a step's `advise`. Neither is a node agent=, so
// the node-only allocation walk saw both as unreferenced.
func advisorAgents() config.AgentsConfig {
	return config.AgentsConfig{
		Executor: config.AgentBinding{Command: "in-loop", Role: config.RoleAgent},
		Reviewer: config.AgentBinding{Command: agentcli.DefaultGrokCommand, Tools: "read_file,grep,list_dir", Model: "grok-4.5"},
		Agents: map[string]config.AgentBinding{
			"park-triage":   {Command: agentcli.DefaultGrokCommand, Tools: "read_file,grep,list_dir", Model: "grok-4.5", Role: config.RoleAgent},
			"lessons":       {Command: agentcli.DefaultGrokCommand, Tools: "read_file,grep,list_dir", Model: "grok-4.5", Role: config.RoleAgent},
			"never-alloced": {Command: agentcli.DefaultGrokCommand, Tools: "read_file,grep,list_dir", Model: "grok-4.5", Role: config.RoleAgent},
		},
	}
}

const advisorDone = `["*"]
obligations = ["raised", "closed"]
park = { state = "blocked", gate = "park-review", advisor = "park-triage", advisor_skill = "triage-rubric" }
`

const advisorStep = `[raised]
status = "backlog"
start = true

[closed]
status = "done"
terminal = true
requires = ["raised"]
advise = { agent = "lessons", skill = "lessons-rubric" }
`

// TestValidate_AdvisorBindingIsNotOrphaned is AC1: an advisor binding is an
// ALLOCATION. Both forms — the park advisor off done.toml and a step's advise —
// must count, while a binding nothing references at all still warns.
func TestValidate_AdvisorBindingIsNotOrphaned(t *testing.T) {
	r := Validate(advisorAgents(), nil, routeDocs(advisorDone, advisorStep))
	if !r.OK() {
		t.Fatalf("advisor fixture must not produce hard problems: %v", r.Problems)
	}

	orphaned := map[string]bool{}
	for _, f := range r.Findings {
		if f.ID != health.IDNodeAlloc {
			continue
		}
		for _, name := range []string{"park-triage", "lessons", "never-alloced"} {
			if strings.Contains(f.Detail, "["+name+"]") {
				orphaned[name] = true
			}
		}
	}
	for _, name := range []string{"park-triage", "lessons"} {
		if orphaned[name] {
			t.Errorf("[%s] is bound as an advisor and must not be reported orphaned: %v", name, r.Warnings)
		}
	}
	if !orphaned["never-alloced"] {
		t.Errorf("a binding nothing references must still warn: %v", r.Warnings)
	}
}

// TestValidate_AdvisorCarriesNoDispatchObligation pins the rule an implementer
// might "complete" by reflex: an advisor is consulted by the orchestrator and
// never dispatched, so a role=agent advisor with no context channel is sound.
// Adding a performer-style check here would break every advisor in the field.
func TestValidate_AdvisorCarriesNoDispatchObligation(t *testing.T) {
	agents := advisorAgents()
	b := agents.Agents["park-triage"]
	b.Tools = "read_file" // no satelle CLI, no context channel
	agents.Agents["park-triage"] = b

	r := Validate(agents, nil, routeDocs(advisorDone, advisorStep))
	if !r.OK() {
		t.Fatalf("an advisor is never dispatched and carries no channel requirement: %v", r.Problems)
	}
}

// reviewerShellAgents grants [reviewer] a satelle-only shell.
func reviewerShellAgents() config.AgentsConfig {
	return config.AgentsConfig{
		Executor: config.AgentBinding{Command: "in-loop", Role: config.RoleAgent},
		Reviewer: config.AgentBinding{
			Role:    config.RoleReviewer,
			Command: agentcli.DefaultClaudeCommand,
			Tools:   "Read,Grep,Glob,Bash(satelle:*)",
			Model:   "opus",
		},
	}
}

const shellGrantDone = `["*"]
obligations = ["raised", "closed"]
`

const shellGrantStep = `[raised]
status = "backlog"
start = true

[closed]
status = "done"
reviewers = ["evidence-check"]
reviewer_agent = "reviewer"
terminal = true
requires = ["raised"]
`

// TestValidate_ReviewerShellGrantUsedBySkill is AC2. The advisory claims the
// grant "is never used"; that is only true when nothing the workflow reaches
// exercises it. A gate skill whose body shells `satelle` exercises it directly,
// and the checker must see the skill BODY to know.
func TestValidate_ReviewerShellGrantUsedBySkill(t *testing.T) {
	shellingSkill := "# evidence check\n\nPull the closing evidence:\n\n```bash\nsatelle story docs <id>\n```\n"
	quietSkill := "# evidence check\n\nRead the diff and judge it. No tooling.\n"

	unsafe := func(r Report) bool {
		for _, f := range r.Findings {
			if f.ID == health.IDReviewerUnsafe && strings.Contains(f.Detail, "grants shell") {
				return true
			}
		}
		return false
	}
	body := func(s string) SkillBody {
		return func(name string) (string, bool) {
			if name == "evidence-check" {
				return s, true
			}
			return "", false
		}
	}

	wfs := routeDocs(shellGrantDone, shellGrantStep)
	global := config.GlobalAgentsConfig{}

	// The skill shells satelle — the grant is live, so no advisory.
	if r := ValidateEffectiveWithSkills(reviewerShellAgents(), global, nil, wfs, body(shellingSkill)); unsafe(r) {
		t.Errorf("a reviewer skill that shells satelle EXERCISES the grant: %v", r.Warnings)
	}
	// Same fixture, a rubric that never shells — the advisory stands.
	if r := ValidateEffectiveWithSkills(reviewerShellAgents(), global, nil, wfs, body(quietSkill)); !unsafe(r) {
		t.Errorf("no reachable skill shells satelle — the advisory must still fire: %v", r.Warnings)
	}
	// No resolver: no evidence either way, so behaviour is unchanged (back-compat).
	if r := Validate(reviewerShellAgents(), nil, wfs); !unsafe(r) {
		t.Errorf("without a skill resolver the advisory must behave as before: %v", r.Warnings)
	}
}

// TestSkillShellsSatelle pins the heuristic's two ends: a shell position counts,
// prose does not. It only suppresses an advisory, so it errs toward matching.
func TestSkillShellsSatelle(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want bool
	}{
		{"fenced command", "```bash\nsatelle story docs <id>\n```", true},
		{"indented in a fence", "```\n  satelle ledger list --story x\n```", true},
		{"code span mid-sentence", "Pull it with `satelle story doc <id> ac-evidence` first.", true},
		{"bullet with a code span", "- `satelle reindex` before judging", true},
		{"prose only", "satelle runs this repo's workflow and this gate judges it.", false},
		{"names the tool, never shells it", "The satelle ledger records every verdict.", false},
		{"empty", "", false},
	} {
		if got := skillShellsSatelle(tc.body); got != tc.want {
			t.Errorf("%s: skillShellsSatelle = %v, want %v", tc.name, got, tc.want)
		}
	}
}
