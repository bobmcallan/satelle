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

// TestEmbeddedOperatingPrinciples: the binary embeds the principles an agent
// needs to OPERATE satelle — operating discipline (agent-goals), execution model
// (agent-model), edit-gate rule (edits-require-a-story), blockage recognition
// (recognise-blockage; sty_0334d12b), and the residency taxonomy DEFINITION
// (residency; sty_1278fdd9, demoted to ondemand by the context diet sty_cd5e341c).
// Everything else is authoring/development substrate that lives in a repo's
// .satelle/principles (sty_807ae744, sty_949e8739).
func TestEmbeddedOperatingPrinciples(t *testing.T) {
	embedded := embeddedPrinciples()
	for _, name := range []string{
		"satelle-agent-goals",
		"satelle-agent-model",
		"satelle-edits-require-a-story",
		"satelle-recognise-blockage",
		"satelle-residency",
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
