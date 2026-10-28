package cli

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/agentcli"
)

// TestSuiteIgnoresAmbientHarnessSession (sty_a37fa9fe): a harness-sensitive test
// gives the same answer whichever agent session runs the suite. The child is
// this test binary run with EVERY key agentcli names as session evidence set —
// markers, prefix variants, session ids, and the anchor pins pointing at a
// foreign directory. The child's TestMain must scrub them, so the tests that
// would otherwise judge themselves against that identity pass. The child env is
// built from agentcli's own list: a key added there is set here too.
func TestSuiteIgnoresAmbientHarnessSession(t *testing.T) {
	if os.Getenv("SATELLE_CLI_REEXEC") != "" {
		t.Skip("running as the CLI re-exec")
	}
	foreign := t.TempDir()
	anchors := map[string]bool{}
	for _, k := range agentcli.SessionAnchorEnvKeys() {
		anchors[k] = true
	}
	var env []string
	for _, e := range os.Environ() {
		if k, _, _ := strings.Cut(e, "="); k != "SATELLE_CLI_REEXEC" {
			env = append(env, e)
		}
	}
	for _, k := range agentcli.SessionMarkerEnvNames() {
		switch {
		case anchors[k]:
			env = append(env, k+"="+foreign)
		case strings.HasSuffix(k, "_"): // a prefix marker: set a key that carries it
			env = append(env, k+"PROBE=1")
		default:
			env = append(env, k+"=1")
		}
	}
	env = append(env, "CURSOR_CONVERSATION_ID=probe-123", "CLAUDE_CODE_SESSION_ID=probe-claude")

	tests := []string{"TestSessionNameStorySegment", "TestStatusLineRenderIsReadOnly", "TestHookHandlersHeartbeatLiveSeat"}
	cmd := exec.Command(os.Args[0], "-test.run", "^("+strings.Join(tests, "|")+")$", "-test.count=1", "-test.v")
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("child suite failed under an ambient harness session: %v\n%s", err, out)
	}
	for _, name := range tests {
		if !strings.Contains(string(out), "--- PASS: "+name) {
			t.Errorf("child did not run %s to a pass:\n%s", name, out)
		}
	}
}
