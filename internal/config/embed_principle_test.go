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
// execution model (actor-model). Everything else is authoring/development
// substrate that lives in a repo's .satelle/principles (sty_807ae744).
func TestEmbeddedOperatingPrinciples(t *testing.T) {
	embedded := embeddedPrinciples()
	for _, name := range []string{"satelle-agent-goals", "satelle-actor-model"} {
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

func TestEmbeddedStructureReviewers(t *testing.T) {
	want := map[string]bool{
		"satelle-skill-review":     false,
		"satelle-workflow-review":  false,
		"satelle-principle-review": false,
		"satelle-story-review":     false,
	}
	for _, d := range EmbeddedDefaults() {
		if d.Kind == "skills" {
			if _, ok := want[d.Name]; ok {
				want[d.Name] = true
			}
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("embedded structure reviewer %q missing from EmbeddedDefaults()", name)
		}
	}
}
