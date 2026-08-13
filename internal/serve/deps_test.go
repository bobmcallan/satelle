package serve

import (
	"os/exec"
	"strings"
	"testing"
)

// TestReconcilerKeepsServeOffTheRepoDB proves AC5 of sty_e6e467fe and AC7 of
// sty_c526753a: the serving tier re-requests state and forwards workstate by
// exec'ing the CLI, so it must still link none of the repo-writing stack
// (including internal/hosted). The per-repo database keeps its single reader
// and the mirror stays a read-only view.
func TestReconcilerKeepsServeOffTheRepoDB(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", ".").CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps: %v\n%s", err, out)
	}
	deps := string(out)
	for _, f := range []string{
		"github.com/bobmcallan/satelle/internal/app",
		"github.com/bobmcallan/satelle/internal/store",
		"github.com/bobmcallan/satelle/internal/cli",
		"github.com/bobmcallan/satelle/internal/verb",
		"github.com/bobmcallan/satelle/internal/hosted",
	} {
		if strings.Contains(deps, f) {
			t.Errorf("internal/serve must not depend on %s", f)
		}
	}
}
