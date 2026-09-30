package doctor

import (
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/health"
)

// sty_992cffc6 AC6 — `satelle doctor` reports a repo that opted out of the
// substrate lock, and ONLY that repo. Absent and present-but-empty are tested
// separately because the decoded config cannot tell them apart.
func TestDoctorReportsSubstrateLockOptOutOnly(t *testing.T) {
	cases := []struct {
		name string
		toml string
		want bool
	}{
		{"key absent (default lock)", "", false},
		{"gate section without the key", "[gate]\nedit_exempt_paths = [\".satelle/\"]\n", false},
		{"key present with the default entry", "[gate]\nlock_substrate_paths = [\".satelle/\"]\n", false},
		{"key present with another prefix", "[gate]\nlock_substrate_paths = [\"policy/\"]\n", false},
		{"key present and empty (opt-out)", "[gate]\nlock_substrate_paths = []\n", true},
		{"key present, all blank (opt-out)", "[gate]\nlock_substrate_paths = [\"\"]\n", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := newFixtureRepo(t, fixtureOpts{extraFiles: map[string]string{"satelle.toml": tc.toml}})
			rep := check(t, root)
			got := ids(rep)[health.IDSubstrateUnlocked]
			if got != tc.want {
				t.Fatalf("finding %s present = %v, want %v (findings: %+v)", health.IDSubstrateUnlocked, got, tc.want, rep.Findings)
			}
			if !tc.want {
				return
			}
			for _, f := range rep.Findings {
				if f.ID != health.IDSubstrateUnlocked {
					continue
				}
				if f.Severity != health.SeverityWarn {
					t.Errorf("severity = %v, want warn", f.Severity)
				}
				if !strings.Contains(f.Detail, "lock_substrate_paths = []") || f.Remediation == "" {
					t.Errorf("finding must name the key and carry a remediation: %+v", f)
				}
			}
		})
	}
}
