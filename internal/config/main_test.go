package config

import (
	"os"
	"testing"

	"github.com/bobmcallan/satelle/internal/agentcli"
)

// TestMain starts the suite from a bare shell: ResolveSession and the anchor
// read the session identity of whichever harness runs the suite, which no test
// here expects to be ambient. A test that needs one sets it with t.Setenv.
func TestMain(m *testing.M) {
	agentcli.ScrubSessionEnv()
	_ = os.Unsetenv(SessionEnv)
	os.Exit(m.Run())
}
