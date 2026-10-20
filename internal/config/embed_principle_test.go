package config

import (
	"strings"
	"testing"
)

// embeddedPrinciples returns the names of the principles carried in the binary.
func embeddedPrinciples() map[string]string {
	out := map[string]string{}
	for _, d := range EmbeddedDefaults() {
		if d.Kind == "principles" {
			out[d.Name] = d.Body
		}
	}
	return out
}

// TestEmbeddedOperatingPrinciples: curated MANIFEST of which principles MUST
// ship (sty_6830e78e AC6 — a table over what exists cannot assert what SHOULD
// exist). Prose substring pins and byte ceilings retired into Tier 1.
func TestEmbeddedOperatingPrinciples(t *testing.T) {
	embedded := embeddedPrinciples()
	for _, name := range []string{
		"satelle-agent-goals",
		"satelle-agent-model",
		"satelle-edits-require-a-story",
		"satelle-cross-repo-containment",
		"satelle-recognise-blockage",
		"satelle-residency",
		"satelle-done-is-last",
		"satelle-reviewer-self-contained",
		ReviewerVerdictPrinciple,
		"satelle-route-standard",
		"satelle-story-classification",
		// Promoted from repo-local: workflows resolve skills by name, so the
		// naming scheme is product-canon (sty_24365c69).
		"satelle-skill-naming",
		// Promoted from repo-local (sty_fd4c3466): executor owes declared
		// outcome artifacts, never gate-judging criteria.
		"satelle-executor-deliverables",
		// Product default (sty_2d5e583a): configurations are warned, never blocked,
		// and no rule is undocumented.
		"satelle-configure-freely",
		// Product default (sty_9fa1023d): agents do not write documentation that
		// restates code or configuration, or that nothing reads.
		"satelle-no-drifting-docs",
		// Product default (sty_a8a308aa): agents review a workflow's structure,
		// never the practice a developer or team configures.
		"satelle-review-structure-not-process",
	} {
		if body, ok := embedded[name]; !ok {
			t.Errorf("operating principle %q must be embedded, but is missing from EmbeddedDefaults()", name)
		} else if len(body) == 0 {
			t.Errorf("embedded principle %q has empty body", name)
		}
	}
	// Surviving pin: these must be session-resident — residency is a curated
	// product choice per principle, not a general corpus rule.
	for _, name := range []string{"satelle-cross-repo-containment", "satelle-no-drifting-docs", "satelle-review-structure-not-process"} {
		if body := embedded[name]; body != "" {
			tagsLine := ""
			for _, line := range strings.Split(body, "\n") {
				if strings.HasPrefix(strings.TrimSpace(line), "tags:") {
					tagsLine = line
					break
				}
			}
			if !strings.Contains(tagsLine, "principles:session") {
				t.Errorf("%s must carry principles:session", name)
			}
		}
	}
	// Negative guard: the shipped text must name nothing specific to this repo.
	for _, term := range []string{"agent-roster", "agents.toml", "sty_"} {
		if strings.Contains(embedded["satelle-no-drifting-docs"], term) {
			t.Errorf("satelle-no-drifting-docs must not name %q (repo-specific)", term)
		}
	}
	// Surviving pin: residency taxonomy is ondemand (defines the axis, does not
	// itself session-inject). Tables check tags legality, not this role choice.
	body := embedded["satelle-residency"]
	if !strings.Contains(body, "principles:session") {
		t.Error("satelle-residency body must name the principles:session marker")
	}
	tagsLine := ""
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "tags:") {
			tagsLine = line
			break
		}
		if strings.HasPrefix(line, "scope:") {
			t.Error("satelle-residency must not carry scope: (residency is the tag alone)")
		}
	}
	if tagsLine == "" {
		t.Error("satelle-residency missing tags: frontmatter line")
	} else if strings.Contains(tagsLine, "principles:session") {
		t.Error("satelle-residency must be ondemand after context diet (no principles:session in tags:)")
	}
	// Surviving pin: executor-deliverables stays ondemand (reference principle;
	// no residency argument made for session injection at promotion).
	edBody := embedded["satelle-executor-deliverables"]
	edTags := ""
	for _, line := range strings.Split(edBody, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "tags:") {
			edTags = line
			break
		}
	}
	if edTags == "" {
		t.Error("satelle-executor-deliverables missing tags: frontmatter line")
	} else if strings.Contains(edTags, "principles:session") {
		t.Error("satelle-executor-deliverables must stay ondemand (no principles:session in tags:)")
	}
}

// TestAgentModelMatchesDispatch: surviving structural pins only — prose marker
// list and line-count ceiling retired (sty_6830e78e AC6). Manifest: must embed.
func TestAgentModelMatchesDispatch(t *testing.T) {
	body, ok := embeddedPrinciples()["satelle-agent-model"]
	if !ok || body == "" {
		t.Fatal("satelle-agent-model must be embedded")
	}
	// Surviving pin: must not reintroduce the retired commit-agent binding name —
	// a negative claim about a specific obsolete term the table cannot express.
	if strings.Contains(body, "commit-agent") {
		t.Error("satelle-agent-model must not contain \"commit-agent\" (stale binding)")
	}
}

// TestDevelopmentPrinciplesNotEmbedded: dogfood-only / dead principles must
// NOT ship embedded. Curated negative manifest (sty_6830e78e AC6).
func TestDevelopmentPrinciplesNotEmbedded(t *testing.T) {
	embedded := embeddedPrinciples()
	for _, name := range []string{
		"satelle-configuration-over-code", // deleted; residue lives in constitution
		"satelle-repo-agnostic",           // repo-local identity (resident, unstamped)
		"satelle-agile-increments",
		"satelle-broken-windows",
		"satelle-yagni",
		// satelle-skill-naming promoted to embedded (sty_24365c69) — not listed here
		"satelle-enable-then-operate",
		"satelle-agent-telemetry",
		"satelle-generated-readonly",
	} {
		if _, ok := embedded[name]; ok {
			t.Errorf("principle %q should NOT be embedded — repo-local or dead (sty_ceb1a3ef)", name)
		}
	}
}

// TestStructureReviewersNotEmbedded: the LLM structure reviewers were RETIRED
// (sty_a90d5c49) — structural conformance is now deterministic code
// (internal/structure), so these rubrics must NOT ship embedded.
func TestStructureReviewersNotEmbedded(t *testing.T) {
	retired := map[string]bool{
		"satelle-skill-review":     true,
		"satelle-workflow-review":  true,
		"satelle-principle-review": true,
		"satelle-story-review":     true,
	}
	for _, d := range EmbeddedDefaults() {
		if d.Kind == "skills" && retired[d.Name] {
			t.Errorf("structure reviewer %q is still embedded — it should be retired (deterministic check in internal/structure)", d.Name)
		}
	}
}

// TestReviewStructureNotProcessScope (sty_a8a308aa AC2): the principle binds
// every agent seat and holds a reviewer to its ACs and its gate.
func TestReviewStructureNotProcessScope(t *testing.T) {
	body, ok := embeddedPrinciples()["satelle-review-structure-not-process"]
	if !ok {
		t.Fatal("satelle-review-structure-not-process must be embedded")
	}
	flat := strings.Join(strings.Fields(body), " ")
	for _, want := range []string{
		"never its process",
		"well formed, resolves and can run",
		"missing skill", "unresolvable binding", "cycle", "state with no exit", "unreachable gate",
		"poor, unusual or contrary to your training",
		"never stop, ask, warn or object",
		"orchestrator", "planners", "coders", "executors", "reviewer gates",
		"against its ACs and its gate, never the configured practice",
		"[[satelle-configure-freely]]", "[[satelle-agent-goals]]",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("principle missing %q", want)
		}
	}
}

// TestAgentGoalsStopRuleIsStructural (sty_a8a308aa AC3): the surface-and-stop
// rule names structural gaps only and points at the structure-not-process rule.
func TestAgentGoalsStopRuleIsStructural(t *testing.T) {
	body, ok := embeddedPrinciples()["satelle-agent-goals"]
	if !ok {
		t.Fatal("satelle-agent-goals must be embedded")
	}
	flat := strings.Join(strings.Fields(body), " ")
	if !strings.Contains(flat, "[[satelle-review-structure-not-process]]") {
		t.Error("satelle-agent-goals must link [[satelle-review-structure-not-process]]")
	}
	if !strings.Contains(flat, "When a **structural** gap blocks the loop") {
		t.Error("satelle-agent-goals stop rule must be limited to a structural gap")
	}
	if !strings.Contains(flat, "surface a structural gap and stop") {
		t.Error("satelle-agent-goals description must say 'surface a structural gap and stop'")
	}
	if strings.Contains(flat, "human-only decision") {
		t.Error("satelle-agent-goals must not name a practice choice a human-only decision")
	}
}
