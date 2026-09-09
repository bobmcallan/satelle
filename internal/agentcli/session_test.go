package agentcli

import (
	"testing"
)

var (
	_ Session       = (*acpSession)(nil)
	_ Session       = (*streamSession)(nil)
	_ SessionOpener = acpRunner{}.Open
	_ SessionOpener = streamRunner{}.Open
)

func TestDefaultPermissionPolicyShared(t *testing.T) {
	// Same policy both ACP handlePermission and stream handleControl inject.
	cases := []struct {
		name      string
		tool      string
		kind      string
		allowMut  bool
		wantAllow bool
	}{
		{"read-only edit denied", "Edit", "edit", false, false},
		{"read-only write denied", "Write", "", false, false},
		{"read-only bash denied", "Bash", "", false, false},
		{"read-only read allowed", "Read", "read", false, true},
		{"read-only grep allowed", "Grep", "search", false, true},
		{"mutator grant edit allowed", "Edit", "edit", true, true},
		{"mutator grant bash allowed", "Bash", "", true, true},
		{"mutator grant read still allowed", "Read", "read", true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pol := defaultPermissionPolicy(tc.allowMut)
			got := pol(PermissionRequest{ToolName: tc.tool, Kind: tc.kind})
			if got.Allow != tc.wantAllow {
				t.Fatalf("allow=%v, want %v (tool=%s kind=%s mut=%v)", got.Allow, tc.wantAllow, tc.tool, tc.kind, tc.allowMut)
			}
		})
	}
}

func TestToolNameKindMapsMutators(t *testing.T) {
	if toolNameKind("Edit") != "edit" || toolNameKind("Write") != "edit" || toolNameKind("NotebookEdit") != "edit" {
		t.Fatalf("edit-family mapping: Edit=%q Write=%q NotebookEdit=%q", toolNameKind("Edit"), toolNameKind("Write"), toolNameKind("NotebookEdit"))
	}
	if toolNameKind("Bash") != "execute" {
		t.Fatalf("Bash -> %q, want execute", toolNameKind("Bash"))
	}
	if toolNameKind("Read") != "Read" {
		t.Fatalf("Read -> %q, want passthrough", toolNameKind("Read"))
	}
}
