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

// TranslateTools restates a grant in another adapter's names, and never guesses
// a name that has no counterpart (sty_f7f80c48 AC1/AC2).
func TestTranslateTools(t *testing.T) {
	cases := []struct {
		name, from, to, grant string
		want, wantUnmapped    []string
	}{
		{"every pi built-in to claude", HarnessPi, HarnessClaude, "read,bash,edit,write,grep,find,ls",
			[]string{"Read", "Bash", "Edit", "Write", "Grep", "Glob", "LS"}, nil},
		{"claude to pi", HarnessClaude, HarnessPi, "Read,Glob,LS", []string{"read", "find", "ls"}, nil},
		{"wildcard, scoped pattern and own names pass through", HarnessPi, HarnessClaude, "*,Bash(satelle:*),Read,grep",
			[]string{"*", "Bash(satelle:*)", "Read", "Grep"}, nil},
		{"a name in neither vocabulary passes through", HarnessPi, HarnessClaude, "read_file,ls", []string{"read_file", "LS"}, nil},
		{"no counterpart stays and is reported", HarnessClaude, HarnessPi, "Read,WebFetch",
			[]string{"read", "WebFetch"}, []string{"WebFetch"}},
		{"a repeat after translation is kept once", HarnessPi, HarnessClaude, "read,Read", []string{"Read"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, unmapped := TranslateTools(tc.from, tc.to, tc.grant)
			if !slices.Equal(got, tc.want) || !slices.Equal(unmapped, tc.wantUnmapped) {
				t.Errorf("TranslateTools(%q, %q, %q) = %v %v, want %v %v", tc.from, tc.to, tc.grant, got, unmapped, tc.want, tc.wantUnmapped)
			}
		})
	}
}

func TestSameTools(t *testing.T) {
	if !SameTools("Read,Grep, Glob", "Glob Read,Grep,Read") {
		t.Error("order, separators and repeats must not matter")
	}
	if SameTools("Read,Grep", "Read,Grep,Glob") {
		t.Error("a different set must not match")
	}
}
