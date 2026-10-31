package agentcli

import (
	"slices"
	"testing"
)

func TestForeignToolNames(t *testing.T) {
	cases := []struct {
		name, adapter, grant string
		wantOwner            string
		wantNames            []string
	}{
		{"pi names under claude", HarnessClaude, "read,grep,find,ls", HarnessPi, []string{"read", "grep", "find", "ls"}},
		{"claude names under pi", HarnessPi, "Read,Bash,Edit", HarnessClaude, []string{"Read", "Bash", "Edit"}},
		{"own names are clean", HarnessClaude, "Read,Grep,Glob", "", nil},
		{"mixed grant names only the foreign ones", HarnessClaude, "Read,grep,Glob,ls", HarnessPi, []string{"grep", "ls"}},
		{"wildcard and scoped patterns are not judged", HarnessClaude, "*,Bash(satelle:*),Read", "", nil},
		{"a name in no vocabulary is not judged", HarnessClaude, "read_file,list_dir", "", nil},
		{"unknown adapter judges nothing", HarnessUnknown, "read,Read", "", nil},
		{"grok has no vocabulary here", HarnessGrok, "read,grep", "", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			owner, names := ForeignToolNames(tc.adapter, tc.grant)
			if owner != tc.wantOwner || !slices.Equal(names, tc.wantNames) {
				t.Errorf("ForeignToolNames(%q, %q) = %q %v, want %q %v", tc.adapter, tc.grant, owner, names, tc.wantOwner, tc.wantNames)
			}
		})
	}
}
