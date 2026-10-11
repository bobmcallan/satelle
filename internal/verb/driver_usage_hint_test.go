package verb

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/config"
)

// TestDefaultSnapshotterPassesThePublishedSessionRoot pins the verb seam of AC1: the
// root a session published as its home is handed to the reader as a hint. A transcript
// for the session sits under two trees (so a store-wide search alone is ambiguous); the
// published root picks the session's own, and without one the ambiguity is reported.
func TestDefaultSnapshotterPassesThePublishedSessionRoot(t *testing.T) {
	withWiring(t)
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	t.Setenv("SATELLE_HOME", t.TempDir())
	const sid = "sess-published-root"
	const mainTree, otherTree, worktree = "/home/example/main", "/home/example/other", "/home/example/wt"
	line := `{"type":"assistant","message":{"id":"m1","model":"claude-opus-5","usage":{"input_tokens":3,"output_tokens":4}}}` + "\n"
	for _, tree := range []string{mainTree, otherTree} {
		path := filepath.Join(home, "projects", strings.ReplaceAll(tree, "/", "-"), sid+".jsonl")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if snap := driverSnapshotter(agentcli.HarnessClaude, sid, worktree); snap.Available || !strings.Contains(snap.UnavailableReason, "claude: ambiguous session record") {
		t.Fatalf("no published root: snapshot = %+v, want the ambiguous-record reason", snap)
	}
	config.PublishSessionRoot(sid, mainTree)
	if snap := driverSnapshotter(agentcli.HarnessClaude, sid, worktree); !snap.Available || snap.FreshInputTokens != 3 || snap.OutputTokens != 4 {
		t.Fatalf("published root: snapshot = %+v, want the main tree's transcript", snap)
	}
}
