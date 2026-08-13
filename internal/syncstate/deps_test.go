package syncstate

import (
	"os/exec"
	"strings"
	"testing"
)

func TestSyncstateIsALeaf(t *testing.T) {
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
		"github.com/bobmcallan/satelle/internal/serve",
		"github.com/bobmcallan/satelle/internal/web",
		"github.com/bobmcallan/satelle/internal/doctor",
	} {
		if strings.Contains(deps, f) {
			t.Errorf("internal/syncstate must not depend on %s", f)
		}
	}
}
