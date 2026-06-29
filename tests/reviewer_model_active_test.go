package tests

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
)

// TestRepoReviewerModelIsSonnet pins this repo's dogfood substrate: the activated
// .satelle/actors.toml resolves the reviewer binding's model to "sonnet"
// (sty_5073df2f turns on sty_dad271fd's configurable knob). It guards against the
// activation silently regressing — being re-commented or cleared — so the reviewer
// keeps reviewing on sonnet here. The wiring from binding → reviewer subprocess is
// covered by internal/reviewer.TestReviewerModelReachesRunner; this asserts the
// repo's own config feeds that wiring "sonnet".
func TestRepoReviewerModelIsSonnet(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	dataDir := filepath.Join(filepath.Dir(filepath.Dir(file)), ".satelle")
	ac, err := config.LoadActors(dataDir)
	if err != nil {
		t.Fatalf("load %s/actors.toml: %v", dataDir, err)
	}
	if got := ac.ReviewerBinding().Model; got != "sonnet" {
		t.Errorf("reviewer model = %q, want sonnet (.satelle/actors.toml must keep the reviewer-model knob active)", got)
	}
}
