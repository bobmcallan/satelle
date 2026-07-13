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

// TestEmbeddedOperatingPrinciples: the binary embeds product-canon principles
// an agent needs to OPERATE satelle or AUTHOR within the harness (channel-
// alignment sty_ceb1a3ef). Operating triad + residency taxonomy + authoring
// canon (done-is-last, reviewer-self-contained, dot-standard, story-
// classification). Repo-local dogfood discipline stays unstamped under
// .satelle/principles only.
func TestEmbeddedOperatingPrinciples(t *testing.T) {
	embedded := embeddedPrinciples()
	for _, name := range []string{
		"satelle-agent-goals",
		"satelle-agent-model",
		"satelle-edits-require-a-story",
		"satelle-recognise-blockage",
		"satelle-residency",
		"satelle-done-is-last",
		"satelle-reviewer-self-contained",
		"satelle-dot-standard",
		"satelle-story-classification",
	} {
		if body, ok := embedded[name]; !ok {
			t.Errorf("operating principle %q must be embedded, but is missing from EmbeddedDefaults()", name)
		} else if len(body) == 0 {
			t.Errorf("embedded principle %q has empty body", name)
		}
	}
	// Taxonomy principle is embedded + ondemand (sty_cd5e341c): it DEFINES the
	// system|ondemand axis and names principles:session as the marker, but does
	// not itself carry the session tag. Must not reintroduce inert scope.
	body := embedded["satelle-residency"]
	if !strings.Contains(body, "principles:session") {
		t.Error("satelle-residency body must name the principles:session marker")
	}
	// tags: line must NOT include the session marker (ondemand after diet).
	// description may still mention the marker by name — only the tag list counts.
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
}

// TestAgentModelMatchesDispatch pins the sty_704bfb8b rewrite: short, accurate
// against the real dispatch model (in-loop executor; isolated reviewer/named
// agents; accept-only advance; no stale commit-agent claim).
func TestAgentModelMatchesDispatch(t *testing.T) {
	body, ok := embeddedPrinciples()["satelle-agent-model"]
	if !ok || body == "" {
		t.Fatal("satelle-agent-model must be embedded")
	}
	lines := strings.Count(body, "\n") + 1
	if lines >= 100 {
		t.Errorf("satelle-agent-model must be materially shorter than ~139 lines; got %d", lines)
	}
	for _, want := range []string{
		"in-loop",
		"agents.toml",
		"accept",
		"agent=executor",
		"agent=reviewer",
		"isolated",
	} {
		if !strings.Contains(strings.ToLower(body), strings.ToLower(want)) {
			t.Errorf("satelle-agent-model missing dispatch-model marker %q", want)
		}
	}
	// Stale / inaccurate claims the rewrite must not reintroduce.
	for _, ban := range []string{"commit-agent", "sty_"} {
		if strings.Contains(body, ban) {
			t.Errorf("satelle-agent-model must not contain %q (stale this-repo or wrong binding example)", ban)
		}
	}
	// Named agent is a performer, not a general "worker" substitute for planner.
	if !strings.Contains(body, "planner") && !strings.Contains(body, "named agent") {
		t.Error("satelle-agent-model must mention named agents (e.g. planner) as isolated performers")
	}
}

// TestDevelopmentPrinciplesNotEmbedded: dogfood-only / dead principles must
// NOT ship embedded. configuration-over-code was deleted (merged into the
// constitution). Remaining names are repo-local discipline, not product-canon.
func TestDevelopmentPrinciplesNotEmbedded(t *testing.T) {
	embedded := embeddedPrinciples()
	for _, name := range []string{
		"satelle-configuration-over-code", // deleted; residue lives in constitution
		"satelle-repo-agnostic",           // repo-local identity (resident, unstamped)
		"satelle-agile-increments",
		"satelle-broken-windows",
		"satelle-yagni",
		"satelle-skill-naming",
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
