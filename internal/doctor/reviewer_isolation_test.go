package doctor

import (
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/health"
)

// isolationFindingsFor returns the reviewer.isolation findings a fixture repo's
// report carries, keyed by the binding they name.
func isolationFindingsFor(t *testing.T, agentsAdd string) map[string]health.Finding {
	t.Helper()
	r := check(t, newFixtureRepo(t, fixtureOpts{agents: healthyAgentsTOML + agentsAdd}))
	out := map[string]health.Finding{}
	for _, f := range r.Findings {
		if f.ID != health.IDReviewerIsolation {
			continue
		}
		for _, name := range []string{"reviewer", "judge", "reviewer-summary", "reviewer-consult"} {
			if strings.Contains(f.Detail, `binding "`+name+`"`) {
				out[name] = f
			}
		}
	}
	return out
}

// sty_ef3efb51 AC8: doctor warns — naming the binding — when a reviewer binding
// skips permission requests without a tool allow-list equal to its grant. The
// stock grok command preset and the claude default do not warn.
func TestDoctorWarnsOnReviewerSkippingPermissionsWithoutAllowList(t *testing.T) {
	warn := []struct {
		name, binding string
	}{
		{"grok acp stock spawn (peer reports no permission mode)", "\n[judge]\nrole = \"reviewer\"\ninterface = \"acp\"\ncommand = \"grok agent stdio\"\ntools = \"Read,Grep,Glob\"\n"},
		{"grok acp in yolo", "\n[judge]\nrole = \"reviewer\"\ninterface = \"acp\"\ncommand = \"grok agent --always-approve stdio\"\ntools = \"Read,Grep,Glob\"\n"},
		{"grok command always-approve, no --tools", "\n[judge]\nrole = \"reviewer\"\ncommand = \"grok -p {payload} --system-prompt-override {system} --always-approve\"\ntools = \"Read,Grep,Glob\"\n"},
		{"grok command always-approve, wider --tools", "\n[judge]\nrole = \"reviewer\"\ncommand = \"grok -p {payload} --tools read_file,run_terminal_command --always-approve\"\ntools = \"Read,Grep,Glob\"\n"},
		{"claude bypass, no --tools", "\n[judge]\nrole = \"reviewer\"\ncommand = \"claude -p --dangerously-skip-permissions --append-system-prompt {system}\"\ntools = \"Read,Grep,Glob\"\n"},
		{"claude command bypass + scoped grant", "\n[judge]\nrole = \"reviewer\"\ncommand = \"claude -p --dangerously-skip-permissions --tools {tools} --append-system-prompt {system}\"\ntools = \"Read,Grep,Glob,Bash(satelle:*)\"\n"},
		{"claude stream bypassPermissions + scoped grant", "\n[judge]\nrole = \"reviewer\"\ninterface = \"stream\"\ncommand = \"claude -p --input-format stream-json --output-format stream-json --permission-mode bypassPermissions --tools {tools}\"\ntools = \"Read,Grep,Glob,Bash(satelle:*)\"\n"},
	}
	for _, tc := range warn {
		t.Run(tc.name, func(t *testing.T) {
			got := isolationFindingsFor(t, tc.binding)
			f, ok := got["judge"]
			if !ok {
				t.Fatalf("no reviewer.isolation finding naming the binding %q", "judge")
			}
			if f.Severity != health.SeverityWarn {
				t.Errorf("severity = %s, want warn (advisory)", f.Severity)
			}
			if !strings.Contains(f.Detail, "skips permission requests without a tool allow-list equal to its grant") {
				t.Errorf("detail = %q", f.Detail)
			}
		})
	}

	// An unrecognised harness is refused unless the binding attests isolation; a
	// key on a claude/grok binding waives nothing.
	t.Run("unrecognised harness without the key warns it will be refused", func(t *testing.T) {
		got := isolationFindingsFor(t, "\n[judge]\nrole = \"reviewer\"\ncommand = \"verdict.sh -p {payload}\"\ntools = \"Read,Grep,Glob\"\n")
		f, ok := got["judge"]
		if !ok || !strings.Contains(f.Detail, "will be refused at dispatch") {
			t.Fatalf("want a refusal warning naming the binding, got %+v", got)
		}
	})
	t.Run("attested unrecognised harness is visible, naming the binding", func(t *testing.T) {
		got := isolationFindingsFor(t, "\n[judge]\nrole = \"reviewer\"\ncommand = \"verdict.sh -p {payload}\"\ntools = \"Read,Grep,Glob\"\nisolation = \"operator-attested\"\n")
		f, ok := got["judge"]
		if !ok {
			t.Fatal("an attested binding must be reported, never silent")
		}
		if f.Severity != health.SeverityWarn || strings.Contains(f.Detail, "will be refused") || !strings.Contains(f.Detail, "operator-attested") {
			t.Errorf("attested finding = %+v", f)
		}
	})
	t.Run("the key does not waive a grok acp refusal", func(t *testing.T) {
		got := isolationFindingsFor(t, "\n[judge]\nrole = \"reviewer\"\ninterface = \"acp\"\ncommand = \"grok agent stdio\"\ntools = \"Read,Grep,Glob\"\nisolation = \"operator-attested\"\n")
		if f, ok := got["judge"]; !ok || !strings.Contains(f.Detail, "will be refused at dispatch") {
			t.Fatalf("want the refusal warning, got %+v", got)
		}
	})

	// Every role=reviewer dispatch shares the one isolation path, so the summary
	// and consult bindings are judged (and named) exactly like a gate reviewer.
	t.Run("the summary and consult bindings are judged too", func(t *testing.T) {
		got := isolationFindingsFor(t,
			"\n[reviewer-summary]\nrole = \"reviewer\"\ninterface = \"acp\"\ncommand = \"grok agent stdio\"\ntools = \"Read,Grep,Glob\"\n"+
				"\n[reviewer-consult]\nrole = \"reviewer\"\ncommand = \"grok -p {payload} --system-prompt-override {system} --always-approve\"\ntools = \"Read,Grep,Glob\"\n")
		for _, name := range []string{"reviewer-summary", "reviewer-consult"} {
			if f, ok := got[name]; !ok || !strings.Contains(f.Detail, "will be refused at dispatch") {
				t.Errorf("want a refusal warning naming %q, got %+v", name, got)
			}
		}
	})

	clean := []struct {
		name, binding string
	}{
		{"stock grok command preset", "\n[judge]\nrole = \"reviewer\"\ncommand = \"" + agentcli.DefaultGrokCommand + "\"\ntools = \"Read,Grep,Glob\"\n"},
		{"claude default", "\n[judge]\nrole = \"reviewer\"\ncommand = \"" + agentcli.DefaultClaudeCommand + "\"\ntools = \"Read,Grep,Glob\"\n"},
		{"claude default with a scoped grant", "\n[judge]\nrole = \"reviewer\"\ncommand = \"" + agentcli.DefaultClaudeCommand + "\"\ntools = \"Read,Grep,Glob,Bash(satelle:*)\"\n"},
	}
	for _, tc := range clean {
		t.Run("no warning: "+tc.name, func(t *testing.T) {
			if got := isolationFindingsFor(t, tc.binding); len(got) != 0 && got["judge"].ID != "" {
				t.Fatalf("unexpected reviewer.isolation warning: %+v", got["judge"])
			}
		})
	}
}
