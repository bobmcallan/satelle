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

// sty_2d5e583a AC3: doctor WARNs — naming the binding, the gap and the fix — when
// a reviewer binding cannot be held to its grant (it skips permission requests
// without a tool allow-list equal to its grant, or no adapter can deny an
// out-of-grant tool). The binding still runs. The stock grok command preset and
// the claude default do not warn.
func TestDoctorWarnsOnReviewerSkippingPermissionsWithoutAllowList(t *testing.T) {
	warn := []struct {
		name, binding, wantGap string
	}{
		{"grok acp stock spawn (peer reports no permission mode)", "\n[judge]\nrole = \"reviewer\"\ninterface = \"acp\"\ncommand = \"grok agent stdio\"\ntools = \"Read,Grep,Glob\"\n", "usage accounting not to standard"},
		{"grok acp in yolo", "\n[judge]\nrole = \"reviewer\"\ninterface = \"acp\"\ncommand = \"grok agent --always-approve stdio\"\ntools = \"Read,Grep,Glob\"\n", "tools not held to the grant"},
		{"grok command always-approve, no --tools", "\n[judge]\nrole = \"reviewer\"\ncommand = \"grok -p {payload} --system-prompt-override {system} --always-approve\"\ntools = \"Read,Grep,Glob\"\n", "tools not held to the grant"},
		{"grok command always-approve, wider --tools", "\n[judge]\nrole = \"reviewer\"\ncommand = \"grok -p {payload} --tools read_file,run_terminal_command --always-approve\"\ntools = \"Read,Grep,Glob\"\n", "tools not held to the grant"},
		{"claude bypass, no --tools", "\n[judge]\nrole = \"reviewer\"\ncommand = \"claude -p --dangerously-skip-permissions --append-system-prompt {system}\"\ntools = \"Read,Grep,Glob\"\n", "tools not held to the grant"},
		{"claude command bypass + scoped grant", "\n[judge]\nrole = \"reviewer\"\ncommand = \"claude -p --dangerously-skip-permissions --tools {tools} --append-system-prompt {system}\"\ntools = \"Read,Grep,Glob,Bash(satelle:*)\"\n", "scoped grant not enforced"},
		{"claude stream bypassPermissions + scoped grant", "\n[judge]\nrole = \"reviewer\"\ninterface = \"stream\"\ncommand = \"claude -p --input-format stream-json --output-format stream-json --permission-mode bypassPermissions --tools {tools}\"\ntools = \"Read,Grep,Glob,Bash(satelle:*)\"\n", "scoped grant not enforced"},
		{"unrecognised harness without the key", "\n[judge]\nrole = \"reviewer\"\ncommand = \"verdict.sh -p {payload}\"\ntools = \"Read,Grep,Glob\"\n", "tools not held to the grant"},
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
			if !strings.Contains(f.Detail, tc.wantGap) || !strings.Contains(f.Detail, "runs with a warning") {
				t.Errorf("detail = %q, want the gap %q and that it runs", f.Detail, tc.wantGap)
			}
			if strings.Contains(f.Detail, "refused") || f.Remediation == "" || !strings.Contains(f.Remediation, "operator-attested") {
				t.Errorf("finding must not claim a refusal and must carry the fix incl. the acknowledgement: %+v", f)
			}
		})
	}

	// isolation = "operator-attested" acknowledges any gap: an INFO, not a WARN.
	for _, tc := range []struct{ name, binding string }{
		{"unrecognised harness", "\n[judge]\nrole = \"reviewer\"\ncommand = \"verdict.sh -p {payload}\"\ntools = \"Read,Grep,Glob\"\nisolation = \"operator-attested\"\n"},
		{"grok acp", "\n[judge]\nrole = \"reviewer\"\ninterface = \"acp\"\ncommand = \"grok agent stdio\"\ntools = \"Read,Grep,Glob\"\nisolation = \"operator-attested\"\n"},
		{"grok command always-approve", "\n[judge]\nrole = \"reviewer\"\ncommand = \"grok -p {payload} --always-approve\"\ntools = \"Read,Grep,Glob\"\nisolation = \"operator-attested\"\n"},
	} {
		t.Run("attested "+tc.name+" is acknowledged as info, naming the binding", func(t *testing.T) {
			got := isolationFindingsFor(t, tc.binding)
			f, ok := got["judge"]
			if !ok {
				t.Fatal("an attested binding must be reported, never silent")
			}
			if f.Severity != health.SeverityInfo || !strings.Contains(f.Detail, "operator-attested") || strings.Contains(f.Detail, "refused") {
				t.Errorf("attested finding = %+v, want info acknowledging the gap", f)
			}
		})
	}

	// Every role=reviewer dispatch shares the one isolation path, so the summary
	// and consult bindings are judged (and named) exactly like a gate reviewer.
	t.Run("the summary and consult bindings are judged too", func(t *testing.T) {
		got := isolationFindingsFor(t,
			"\n[reviewer-summary]\nrole = \"reviewer\"\ninterface = \"acp\"\ncommand = \"grok agent stdio\"\ntools = \"Read,Grep,Glob\"\n"+
				"\n[reviewer-consult]\nrole = \"reviewer\"\ncommand = \"grok -p {payload} --system-prompt-override {system} --always-approve\"\ntools = \"Read,Grep,Glob\"\n")
		for _, name := range []string{"reviewer-summary", "reviewer-consult"} {
			if f, ok := got[name]; !ok || f.Severity != health.SeverityWarn || !strings.Contains(f.Detail, "runs with a warning") {
				t.Errorf("want a warning naming %q, got %+v", name, got)
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
