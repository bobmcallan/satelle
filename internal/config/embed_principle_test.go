package config

import "testing"

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

// TestEmbeddedOperatingPrinciples: the binary embeds exactly the principles an
// agent needs to OPERATE satelle — the operating discipline (agent-goals) and the
// execution model (agent-model). Everything else is authoring/development
// substrate that lives in a repo's .satelle/principles (sty_807ae744).
func TestEmbeddedOperatingPrinciples(t *testing.T) {
	embedded := embeddedPrinciples()
	for _, name := range []string{"satelle-agent-goals", "satelle-agent-model"} {
		if body, ok := embedded[name]; !ok {
			t.Errorf("operating principle %q must be embedded, but is missing from EmbeddedDefaults()", name)
		} else if len(body) == 0 {
			t.Errorf("embedded principle %q has empty body", name)
		}
	}
}

// TestDevelopmentPrinciplesNotEmbedded: principles that are about AUTHORING
// substrate or DEVELOPING satelle (not required to operate) are NOT embedded —
// they were relocated to .satelle/principles as project substrate (sty_807ae744).
func TestDevelopmentPrinciplesNotEmbedded(t *testing.T) {
	embedded := embeddedPrinciples()
	for _, name := range []string{
		"satelle-done-is-last",
		"satelle-configuration-over-code",
		"satelle-dot-standard",
		"satelle-reviewer-self-contained",
	} {
		if _, ok := embedded[name]; ok {
			t.Errorf("principle %q should NOT be embedded — it belongs in .satelle/principles (project)", name)
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
