package agentcli

import (
	"os"
	"testing"
)

// TestMain starts the suite from a bare shell: no test here judges itself
// against the session identity of the harness that happens to run it. A test
// that needs a harness passes an explicit environ or t.Setenv.
func TestMain(m *testing.M) {
	ScrubSessionEnv()
	os.Exit(m.Run())
}
