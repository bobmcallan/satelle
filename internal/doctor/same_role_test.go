package doctor

import (
	"testing"

	"github.com/bobmcallan/satelle/internal/health"
)

const sameRoleAgentsTOML = healthyAgentsTOML + `
[orchestrator]
role    = "agent"
command = "in-loop"
`

// sty_29ce243b: both seats in-loop is a Warn that names both seats; doctor stays
// healthy. The healthy baseline (orchestrator seat shipped with no command)
// does not warn.
func TestDoctorSameRoleWarning(t *testing.T) {
	r := check(t, newFixtureRepo(t, fixtureOpts{agents: sameRoleAgentsTOML}))
	if !ids(r)[health.IDAgentsSameRole] {
		t.Fatalf("missing %s: %v", health.IDAgentsSameRole, r.Findings)
	}
	if !r.OK {
		t.Fatalf("an advisory must not fail doctor: %v", r.Findings.Details(health.SeverityError))
	}
	for _, f := range r.Findings {
		if f.ID == health.IDAgentsSameRole && f.Severity != health.SeverityWarn {
			t.Errorf("severity = %v, want warn", f.Severity)
		}
	}
}

func TestDoctorNoSameRoleWarningOnBaseline(t *testing.T) {
	r := check(t, newFixtureRepo(t, fixtureOpts{}))
	if ids(r)[health.IDAgentsSameRole] {
		t.Fatalf("baseline must not warn: %v", r.Findings)
	}
}
