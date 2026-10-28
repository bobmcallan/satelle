package drive_test

import (
	"os"
	"testing"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/config"
)

// TestMain starts the suite from a bare shell: the drive tests resolve the
// session through config.ResolveSession, so an ambient harness session would
// answer for the test's own. A test that needs one sets it with t.Setenv.
func TestMain(m *testing.M) {
	agentcli.ScrubSessionEnv()
	_ = os.Unsetenv(config.SessionEnv)
	os.Exit(m.Run())
}
