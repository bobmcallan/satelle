package cli

import (
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/ctxaudit"
	"github.com/bobmcallan/satelle/internal/health"
)

// embeddedBaselineBody is an embedded baseline principle's body, frontmatter
// stripped, so a fixture can restate a passage of the real text.
func embeddedBaselineBody(t *testing.T, name string) string {
	t.Helper()
	for _, d := range config.EmbeddedDefaults() {
		if d.Kind == "principles" && d.Name == name {
			return ctxaudit.StripFrontmatter(d.Body)
		}
	}
	t.Fatalf("baseline principle %s is not embedded", name)
	return ""
}

// TestDoctorStoryShapeOverrides (sty_bcd344fd AC4): over a fixture repo with one
// principle that shadows satelle-story-shape by name, one skill that restates a
// passage of satelle-start-small, and one clean skill that only refers to the
// baselines, doctor reports exactly the shadow and the restatement.
func TestDoctorStoryShapeOverrides(t *testing.T) {
	f := newCtxFixture(t, "")
	f.write("workflows/agents.toml", f.claudeAgents())
	f.principle("satelle-story-shape", "# Story shape\n\nA story is whatever the raiser writes; ACs may carry any evidence.", true)
	start := strings.Join(strings.Fields(embeddedBaselineBody(t, "satelle-start-small")), " ")
	from := strings.Index(start, "Widen scope")
	to := strings.Index(start, "the smaller form held.")
	if from < 0 || to < from {
		t.Fatalf("fixture: satelle-start-small no longer carries the passage it restates:\n%s", start)
	}
	passage := start[from : to+len("the smaller form held.")]
	f.write("skills/fixture-restating.md", "---\nname: fixture-restating\ntype: skill\ndescription: restates a baseline\n---\n\n# Restating\n\n"+passage+"\n")
	f.write("skills/fixture-clean.md", "---\nname: fixture-clean\ntype: skill\ndescription: refers to the baselines\n---\n\n# Clean\n\nHold each story to [[satelle-story-shape]] and [[satelle-start-small]].\n")

	got := byID(checksFor(f), health.IDContextBaselineOverride)
	if len(got) != 2 {
		t.Fatalf("want exactly the shadow and the restatement, got %d: %+v", len(got), got)
	}
	if n := len(mentioning(got, ".satelle/principles/satelle-story-shape.md", "shadows satelle-story-shape")); n != 1 {
		t.Errorf("the shadowing principle must be reported once, got %d in %+v", n, got)
	}
	if n := len(mentioning(got, ".satelle/skills/fixture-restating.md", "restates satelle-start-small", "the ledger shows the smaller form held")); n != 1 {
		t.Errorf("the restating skill must be reported once, quoting the passage, got %d in %+v", n, got)
	}
	if n := len(mentioning(got, "fixture-clean")); n != 0 {
		t.Errorf("a skill that only refers to the baselines must not be reported, got %+v", got)
	}
	for _, fd := range got {
		if fd.Severity != health.SeverityWarn {
			t.Errorf("severity = %s, want warn", fd.Severity)
		}
	}
}

// TestDoctorStoryShapeIdenticalCopyIsNotAShadow: a repo copy whose body equals
// the embedded baseline (an init-stamped copy, frontmatter aside) overrides
// nothing and is not reported.
func TestDoctorStoryShapeIdenticalCopyIsNotAShadow(t *testing.T) {
	f := newCtxFixture(t, "")
	f.write("workflows/agents.toml", f.claudeAgents())
	f.write("principles/satelle-story-shape.md", "---\nname: satelle-story-shape\ntype: principle\ntags: [type:principle, principles:session]\nembedded_sha: abc\n---\n"+embeddedBaselineBody(t, "satelle-story-shape"))
	if got := byID(checksFor(f), health.IDContextBaselineOverride); len(got) != 0 {
		t.Fatalf("an identical copy is not an override, got %+v", got)
	}
}
