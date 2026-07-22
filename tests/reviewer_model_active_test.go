package tests

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
)

// TestRepoReviewerModelIsActive pins this repo's dogfood substrate when present:
// the reviewer binding's model knob stays ACTIVE (a non-empty value), so the
// reviewer runs on a pinned model rather than silently falling back to the CLI
// default. It guards against the activation regressing — the knob being
// re-commented or cleared. The SPECIFIC model is an operator choice that changes
// (this repo has run sonnet and, under the model-mixing work, GLM), so the test
// asserts the knob is set, not a particular value. The wiring from binding →
// reviewer subprocess is covered by internal/agentstep.TestReviewerModelReachesRunner.
//
// After sty_91a390a0, .satelle/ is gitignored (operator-owned). CI clones have no
// agents.toml; LoadAgents then returns zero config and Model is empty. Skip when
// the file is absent so the pin remains a local dogfood check, not a false CI red.
func TestRepoReviewerModelIsActive(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	dataDir := filepath.Join(filepath.Dir(filepath.Dir(file)), ".satelle")
	if _, err := os.Stat(filepath.Join(dataDir, "agents.toml")); os.IsNotExist(err) {
		t.Skip(".satelle/agents.toml not present (gitignored operator substrate); dogfood pin is local-only")
	}
	ac, err := config.LoadAgents(dataDir)
	if err != nil {
		t.Fatalf("load %s/agents.toml: %v", dataDir, err)
	}
	if got := ac.ReviewerBinding().Model; got == "" {
		t.Errorf("reviewer model is empty — .satelle/agents.toml must keep the reviewer-model knob active (a pinned model)")
	}
}
