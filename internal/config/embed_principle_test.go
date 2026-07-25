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
		"satelle-dot-standard",
		"satelle-story-classification",
	} {
		if body, ok := embedded[name]; !ok {
			t.Errorf("operating principle %q must be embedded, but is missing from EmbeddedDefaults()", name)
		} else if len(body) == 0 {
			t.Errorf("embedded principle %q has empty body", name)
		}
	}
	// Surviving pin: cross-repo-containment must be session-resident — residency
	// is a curated product choice for this principle, not a general corpus rule.
	if body := embedded["satelle-cross-repo-containment"]; body != "" {
		tagsLine := ""
		for _, line := range strings.Split(body, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "tags:") {
				tagsLine = line
				break
			}
		}
		if !strings.Contains(tagsLine, "principles:session") {
			t.Error("satelle-cross-repo-containment must carry principles:session")
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
