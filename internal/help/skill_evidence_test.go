package help

import (
	"strings"
	"testing"
)

// sty_23ceeca2 AC5 (satelle-configure-freely: no undocumented rule): the
// skill_evidence key, its two values and its default, the delivery manifest, the
// four evidence_* keys and the unverifiable outcome are documented in
// `satelle help agent-dispatch`.
func TestSkillEvidenceIsDocumented(t *testing.T) {
	dispatch, ok := Get("agent-dispatch")
	if !ok {
		t.Fatal("agent-dispatch help topic missing")
	}
	for _, want := range []string{
		"`skill_evidence`", "`record` or `reject`", "the default is `record`",
		"evidence_sections", "evidence_keys", "evidence_commands", "evidence_tools",
		"`skills`", "sha256", "`delivered: false`", "unverifiable", "tool_trace",
	} {
		if !strings.Contains(dispatch.Body, want) {
			t.Errorf("help agent-dispatch does not document %q", want)
		}
	}
}
