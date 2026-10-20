package agentvalidate

import (
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
)

// context (sty_12499ecc): a step offers read-only context sources by name, and
// every name must be declared as [context.<name>] in agents.toml.
func TestValidate_StepContextNamesMustBeDeclared(t *testing.T) {
	declared := func(a *config.AgentsConfig) {
		a.Context = map[string]config.ContextSource{"src": {
			Kind: config.ContextSourceKindMCP, URL: "https://ctx.example.test/mcp", ReadOnly: true, Tools: []string{"get_issue"},
		}}
	}
	cases := []struct {
		name    string
		mutate  func(*config.AgentsConfig)
		coded   string
		wantErr string // "" = must validate clean
	}{
		{name: "declared source accepted", mutate: declared, coded: `context = ["src"]`},
		{name: "no declaration unchanged"},
		{name: "undeclared name refused", coded: `context = ["src"]`, wantErr: `step "in_progress" declares context source "src" with no [context.src] in agents.toml`},
		{name: "one of two undeclared", mutate: declared, coded: `context = ["src", "ghost"]`, wantErr: `context source "ghost" with no [context.ghost]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			agents := remoteAgents()
			if tc.mutate != nil {
				tc.mutate(&agents)
			}
			r := Validate(agents, nil, routeDocs(remoteDone, remoteSteps(tc.coded, "", "")))
			joined := strings.Join(r.Problems, "\n")
			if tc.wantErr == "" {
				if !r.OK() {
					t.Fatalf("must validate clean: %v", r.Problems)
				}
				return
			}
			if r.OK() || !strings.Contains(joined, tc.wantErr) {
				t.Fatalf("want a problem containing %q, got %v", tc.wantErr, r.Problems)
			}
		})
	}
}
