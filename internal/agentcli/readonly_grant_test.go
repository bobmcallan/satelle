package agentcli

import (
	"strings"
	"testing"
)

// ReadOnlyGrantProblems names one problem per cause, by class for a grant and by
// adapter for a harness that cannot hold the grant.
func TestReadOnlyGrantProblems(t *testing.T) {
	const pi = "pi -p --provider openrouter --tools {tools} --model {model} {payload}"
	cases := []struct {
		name, command, iface, grant string
		performer                   bool
		classes                     []ToolClass // the classes named, in order; an adapter cause is ""
	}{
		{"pi read-only", pi, "command", "read,grep,find,ls", true, nil},
		{"pi with shell and write", pi, "", "read,bash,write", true, []ToolClass{ClassShell, ClassWrite}},
		{"scoped shell counts", DefaultClaudeCommand, "command", "Bash(satelle:*)", false, []ToolClass{ClassShell}},
		{"wildcard is unknown", DefaultClaudeCommand, "command", "*", false, []ToolClass{ClassUnknown}},
		{"pi unrestricted", "pi -p {payload}", "command", "read", true, []ToolClass{""}},
		{"unknown adapter", "myagent -p", "command", "Read", false, []ToolClass{""}},
		{"in-loop", "in-loop", "", "", true, []ToolClass{""}},
		{"empty command", "", "", "Read", false, []ToolClass{""}},
		{"cursor reviewer is forced to ask", "cursor-agent -p --output-format json", "command", "", false, nil},
		{"cursor reviewer asking to write", "cursor-agent -p --mode agent", "command", "", false, []ToolClass{""}},
		{"cursor performer asking for ask", "cursor-agent -p --mode ask", "command", "Write", true, nil},
		{"cursor performer not forced", "cursor-agent -p", "command", "", true, []ToolClass{""}},
		{"cursor performer on acp", "cursor-agent acp", "acp", "", true, []ToolClass{""}},
		{"cursor reviewer on acp", "cursor-agent acp", "acp", "", false, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ReadOnlyGrantProblems(tc.command, tc.iface, tc.grant, tc.performer)
			if len(got) != len(tc.classes) {
				t.Fatalf("want %d problem(s), got %+v", len(tc.classes), got)
			}
			for i, want := range tc.classes {
				if got[i].Class != want || strings.TrimSpace(got[i].Cause) == "" {
					t.Errorf("problem %d = %+v, want class %q with a cause", i, got[i], want)
				}
			}
		})
	}
}
