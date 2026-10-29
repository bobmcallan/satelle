package doctor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/agentvalidate"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/health"
)

const piExt = ".pi/extensions/satelle.ts"

// piCoderAgents binds a coder whose command loads the repo's pi extension, the
// shape of the machine-wide pi-coder profile.
const piCoderAgents = healthyAgentsTOML + `
[coder]
role    = "agent"
command = "pi -p --no-session --no-extensions -e .pi/extensions/satelle.ts --append-system-prompt {system} --tools {tools} --model {model} {payload}"
tools   = "read,bash,edit,write"
`

func wiringFindings(r Report) []health.Finding {
	var out []health.Finding
	for _, f := range r.Findings {
		if f.ID == health.IDAgentsWiringAbsent {
			out = append(out, f)
		}
	}
	return out
}

// sty_d15f6713 AC2: a pi binding loading an absent .pi/extensions/satelle.ts is
// an error naming the binding, the path and the fix, and doctor is not OK.
// AC3: once the extension is present the finding is gone.
func TestDoctorBindingWiringAbsent(t *testing.T) {
	root := newFixtureRepo(t, fixtureOpts{agents: piCoderAgents})
	r := check(t, root)
	got := wiringFindings(r)
	if len(got) != 1 {
		t.Fatalf("want one %s finding, got %v", health.IDAgentsWiringAbsent, r.Findings)
	}
	f := got[0]
	if f.Severity != health.SeverityError {
		t.Errorf("severity = %v, want error", f.Severity)
	}
	if !strings.Contains(f.Remediation, "satelle agents install pi") {
		t.Errorf("remediation %q must name `satelle agents install pi`", f.Remediation)
	}
	for _, want := range []string{"[coder]", piExt} {
		if !strings.Contains(f.Detail, want) {
			t.Errorf("detail %q must name %q", f.Detail, want)
		}
	}
	if r.OK {
		t.Error("doctor must not be OK while a binding's wiring is absent")
	}

	p := filepath.Join(root, filepath.FromSlash(piExt))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("export default function () {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := wiringFindings(check(t, root)); len(got) != 0 {
		t.Errorf("present wiring must not be reported: %v", got)
	}
}

// sty_d15f6713 AC3: only a binding whose own command names an absent declared
// wiring path is reported.
func TestBindingWiringScope(t *testing.T) {
	repo := t.TempDir()
	absExt := filepath.Join(t.TempDir(), "pi-cursor")
	if err := os.WriteFile(absExt, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name  string
		grant agentvalidate.Grant
		want  int
	}{
		{"pi reviewer, absolute extension only", agentvalidate.Grant{Name: "reviewer", Backend: "isolated:pi",
			Command: "pi -p --no-session --no-extensions -e " + absExt + " --system-prompt {system}"}, 0},
		{"pi planner, absolute extension only", agentvalidate.Grant{Name: "planner", Backend: "isolated:pi",
			Command: "pi -p --no-session --no-extensions -e " + absExt + " --append-system-prompt {system} {payload}"}, 0},
		{"in-loop", agentvalidate.Grant{Name: "executor", Backend: "in-loop", Command: "in-loop " + piExt}, 0},
		{"invalid", agentvalidate.Grant{Name: "broken", Backend: "invalid", Command: "pi -e " + piExt}, 0},
		{"unknown harness, no gate wiring", agentvalidate.Grant{Name: "other", Backend: "isolated:sh",
			Command: "sh -c " + piExt}, 0},
		{"claude naming none of its wiring", agentvalidate.Grant{Name: "judge", Backend: "isolated:claude",
			Command: "claude -p --disallowedTools Write,Edit {system}"}, 0},
		{"pi coder naming the scaffold", agentvalidate.Grant{Name: "coder", Backend: "isolated:pi",
			Command: "pi -p -e ./" + piExt + " {payload}"}, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := BindingWiring(repo, config.Config{}, []agentvalidate.Grant{c.grant})
			if len(got) != c.want {
				t.Fatalf("findings = %v, want %d", got, c.want)
			}
		})
	}
}

// Two bindings sharing one gap are one finding naming both.
func TestBindingWiringDedupes(t *testing.T) {
	cmd := "pi -p -e " + piExt + " {payload}"
	got := BindingWiring(t.TempDir(), config.Config{}, []agentvalidate.Grant{
		{Name: "coder", Backend: "isolated:pi", Command: cmd},
		{Name: "coder-2", Backend: "isolated:pi", Command: cmd},
	})
	if len(got) != 1 || !strings.Contains(got[0].Detail, "[coder], [coder-2]") {
		t.Fatalf("want one finding naming both bindings, got %v", got)
	}
}
