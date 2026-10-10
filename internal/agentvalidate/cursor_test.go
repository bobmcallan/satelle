package agentvalidate

import (
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/config"
)

// cursor-agent as a dispatched seat (sty_10c52ab3): validation asks agentcli for
// every cursor fact, so these tests drive Validate over cursor bindings and compare
// what it reports with what agentcli itself answers.

func cursorGrant(t *testing.T, r Report, name string) Grant {
	t.Helper()
	for _, g := range r.Grants {
		if g.Name == name {
			return g
		}
	}
	t.Fatalf("no grant %q in %+v", name, r.Grants)
	return Grant{}
}

// validatePerformer validates b as the binding a workflow allocates to a dispatched
// performing node.
func validatePerformer(b config.AgentBinding) Report {
	agents := config.AgentsConfig{
		Executor: config.AgentBinding{Command: "in-loop"},
		Reviewer: config.AgentBinding{Command: agentcli.DefaultGrokCommand, Tools: "read_file,grep,list_dir", Model: "grok-4.5"},
		Agents:   map[string]config.AgentBinding{"coder": b},
	}
	return Validate(agents, nil, channelWF("coder"))
}

// validateReviewer validates b as a named role=reviewer binding. A named binding is
// used because the [reviewer] section defaults an empty tools grant to
// config.DefaultReviewerTools, which would hide what tools="" means.
func validateReviewer(b config.AgentBinding) Report {
	b.Role = "reviewer"
	return Validate(config.AgentsConfig{
		Executor: config.AgentBinding{Command: "in-loop"},
		Reviewer: config.AgentBinding{Command: agentcli.DefaultGrokCommand, Tools: "read_file,grep,list_dir", Model: "grok-4.5"},
		Agents:   map[string]config.AgentBinding{"judge": b},
	}, nil, nil)
}

// AC1/AC6: a cursor performer needs no {system} token and no satelle-shell grant.
func TestCursorPerformerPassesValidate(t *testing.T) {
	r := validatePerformer(config.AgentBinding{Command: agentcli.CursorCommand, Model: "composer-2.5", Role: "agent"})
	if !r.OK() {
		t.Fatalf("a cursor performer with no tools grant must validate: %v", r.Problems)
	}
	g := cursorGrant(t, r, "coder")
	if g.Backend != "isolated:cursor-agent" {
		t.Errorf("backend = %q, want isolated:cursor-agent", g.Backend)
	}
	if !strings.Contains(g.Notes, "system: stdin") {
		t.Errorf("notes must say the instructions ride stdin: %q", g.Notes)
	}
	if p := findingWith(r.Problems, "{system}"); p != "" {
		t.Errorf("cursor must not be asked for {system}: %s", p)
	}
	if p := findingWith(r.Problems, "no context channel"); p != "" {
		t.Errorf("cursor reads material by path, no channel grant is required: %s", p)
	}

	// The same shape for claude still fails on both counts.
	bad := validatePerformer(config.AgentBinding{Command: "claude -p --output-format json --model {model}", Model: "opus", Role: "agent"})
	if findingWith(bad.Problems, "{system}") == "" || findingWith(bad.Problems, "no context channel") == "" {
		t.Errorf("claude without {system} and a channel grant must still fail: %v", bad.Problems)
	}
}

// AC2: a prompt token is refused by agentcli and validate reports the same text.
func TestCursorTemplateTokenRefusalReported(t *testing.T) {
	for _, tok := range []string{"{system}", "{payload}"} {
		t.Run(tok, func(t *testing.T) {
			cmd := "cursor-agent -p --trust --output-format json " + tok
			_, want := agentcli.RunnerFromCommand(cmd)
			if want == nil {
				t.Fatalf("agentcli must refuse %s", tok)
			}
			r := validatePerformer(config.AgentBinding{Command: cmd, Role: "agent"})
			if r.OK() {
				t.Fatalf("%s must fail validate", tok)
			}
			if got := findingWith(r.Problems, "cursor"); !strings.Contains(got, want.Error()) {
				t.Errorf("validate must report agentcli's reason unchanged:\n got: %s\nwant: %s", got, want)
			}
		})
	}
}

// AC5: only json output and the command / acp transports are offered; each other
// shape is refused with agentcli's text, reported unchanged.
func TestCursorTransportRefusalsReported(t *testing.T) {
	cases := []struct {
		name    string
		binding config.AgentBinding
		reason  func() error
	}{
		{"stream-json", config.AgentBinding{Command: "cursor-agent -p --output-format stream-json", Role: "agent"},
			func() error {
				_, err := agentcli.RunnerFromCommand("cursor-agent -p --output-format stream-json")
				return err
			}},
		{"text", config.AgentBinding{Command: "cursor-agent -p --output-format text", Role: "agent"},
			func() error { _, err := agentcli.RunnerFromCommand("cursor-agent -p --output-format text"); return err }},
		{"missing format", config.AgentBinding{Command: "cursor-agent -p --model {model}", Role: "agent"},
			func() error { _, err := agentcli.RunnerFromCommand("cursor-agent -p --model {model}"); return err }},
		{"stream interface", config.AgentBinding{Interface: "stream", Command: "cursor-agent -p --input-format stream-json --output-format stream-json", Role: "agent"},
			func() error {
				_, err := agentcli.RunnerFromBinding("stream", "cursor-agent -p --input-format stream-json --output-format stream-json")
				return err
			}},
		{"cloud interface", config.AgentBinding{Interface: "cloud", Command: "cursor-agent -p", Role: "agent"},
			func() error { return agentcli.CloudLaunchAvailable(agentcli.HarnessCursor) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want := tc.reason()
			if want == nil || !strings.Contains(want.Error(), "cursor") {
				t.Fatalf("agentcli must refuse with a cursor-named reason: %v", want)
			}
			r := validatePerformer(tc.binding)
			if r.OK() {
				t.Fatalf("%s must fail validate", tc.name)
			}
			if got := findingWith(r.Problems, want.Error()); got == "" {
				t.Errorf("validate must report %q unchanged, got problems %v", want, r.Problems)
			}
		})
	}

	// ACP is offered: a performer and a reviewer both validate.
	acp := validatePerformer(config.AgentBinding{Interface: "acp", Command: "cursor-agent acp", Role: "agent"})
	if !acp.OK() {
		t.Errorf("a cursor acp performer must validate: %v", acp.Problems)
	}
	if g := cursorGrant(t, acp, "coder"); g.Backend != "acp:cursor-agent" {
		t.Errorf("acp backend = %q", g.Backend)
	}
	rev := validateReviewer(config.AgentBinding{Interface: "acp", Command: "cursor-agent acp"})
	if !rev.OK() {
		t.Errorf("a cursor acp reviewer with tools=\"\" must validate: %v", rev.Problems)
	}
}

// AC4: a cursor reviewer template whose mode can write is an error, with agentcli's
// reason; a template with no mode, or ask, is read-only, and plan is refused (a
// plan-mode reviewer returns a plan, not a verdict).
func TestCursorReviewerModeAgentIsError(t *testing.T) {
	const bad = "cursor-agent -p --trust --output-format json --mode agent --model {model}"
	_, want := agentcli.ReadOnlyEnforced(agentcli.HarnessCursor, strings.Fields(bad)[1:], agentcli.InterfaceCommand)
	if want == nil {
		t.Fatal("agentcli must refuse --mode agent for a reviewer")
	}
	r := validateReviewer(config.AgentBinding{Command: bad})
	if r.OK() {
		t.Fatal("a reviewer asking --mode agent must be an error")
	}
	if got := findingWith(r.Problems, "cursor"); !strings.Contains(got, want.Error()) {
		t.Errorf("validate must report agentcli's reason unchanged:\n got: %s\nwant: %s", got, want)
	}
	if w := findingWith(r.Warnings, "no read-only ceiling"); w != "" {
		t.Errorf("the refusal must not also be a heuristic warning: %s", w)
	}

	planned := "cursor-agent -p --trust --output-format json --mode plan --model {model}"
	_, planWant := agentcli.ReadOnlyEnforced(agentcli.HarnessCursor, strings.Fields(planned)[1:], agentcli.InterfaceCommand)
	if planWant == nil {
		t.Fatal("agentcli must refuse --mode plan for a reviewer")
	}
	if r := validateReviewer(config.AgentBinding{Command: planned}); r.OK() ||
		!strings.Contains(findingWith(r.Problems, "cursor"), planWant.Error()) {
		t.Errorf("a reviewer asking --mode plan must be an error with agentcli's reason, problems=%v", r.Problems)
	}

	// A repeated --mode is judged on every occurrence: the last one wins in cursor.
	for _, repeated := range []string{
		"cursor-agent -p --trust --output-format json --mode ask --mode agent --model {model}",
		"cursor-agent -p --trust --output-format json --mode ask --mode=agent --model {model}",
	} {
		_, rWant := agentcli.ReadOnlyEnforced(agentcli.HarnessCursor, strings.Fields(repeated)[1:], agentcli.InterfaceCommand)
		if rWant == nil {
			t.Fatalf("agentcli must refuse %q for a reviewer", repeated)
		}
		if r := validateReviewer(config.AgentBinding{Command: repeated}); r.OK() ||
			!strings.Contains(findingWith(r.Problems, "cursor"), rWant.Error()) {
			t.Errorf("%q must be an error with agentcli's reason, problems=%v", repeated, r.Problems)
		}
	}

	for _, ok := range []string{
		"cursor-agent -p --trust --output-format json --model {model}",
		"cursor-agent -p --trust --output-format json --mode ask --model {model}",
	} {
		r := validateReviewer(config.AgentBinding{Command: ok})
		if !r.OK() || findingWith(r.Warnings, "ceiling") != "" {
			t.Errorf("%q must validate with no ceiling finding, problems=%v warnings=%v", ok, r.Problems, r.Warnings)
		}
		if g := cursorGrant(t, r, "judge"); !g.ReadOnly {
			t.Errorf("%q: the forced mode is the ceiling, grant must be ReadOnly", ok)
		}
	}

	// A performer may name any mode: nothing holds it read-only.
	if p := validatePerformer(config.AgentBinding{Command: bad, Role: "agent"}); !p.OK() {
		t.Errorf("a performer naming --mode agent is legitimate: %v", p.Problems)
	}
}

// AC6: tools="" is acceptable on a cursor ACP reviewer because the mode is its
// ceiling; claude and grok keep today's requirement.
func TestCursorACPReviewerCeiling(t *testing.T) {
	if r := validateReviewer(config.AgentBinding{Interface: "acp", Command: "cursor-agent acp"}); !r.OK() {
		t.Errorf("cursor acp reviewer, tools=\"\": %v", r.Problems)
	}
	for _, b := range []config.AgentBinding{
		{Interface: "acp", Command: "grok agent stdio"},
		{Interface: "acp", Command: "pi-agent acp"},
	} {
		r := validateReviewer(b)
		if r.OK() || findingWith(r.Problems, "requires tools=") == "" {
			t.Errorf("%q with tools=\"\" must still fail: %v", b.Command, r.Problems)
		}
	}
	// claude is not an acp-capable binding; its stream reviewer keeps the requirement.
	r := validateReviewer(config.AgentBinding{Interface: "stream", Command: agentcli.DefaultClaudeStreamCommand})
	if r.OK() || findingWith(r.Problems, "requires tools=") == "" {
		t.Errorf("claude stream reviewer with tools=\"\" must still fail: %v", r.Problems)
	}
}

// AC6: the validate and reviewer descriptions say the tool grant is advisory and
// read-only is enforced by the mode.
func TestCursorGrantDescriptions(t *testing.T) {
	const note = "tools: advisory (cursor has no allow-list); read-only enforced by mode"
	const ceiling = "forced ask mode"

	cmd := validateReviewer(config.AgentBinding{Command: agentcli.CursorCommand})
	g := cursorGrant(t, cmd, "judge")
	if !strings.Contains(g.Notes, note) {
		t.Errorf("command notes lack the advisory-tools note: %q", g.Notes)
	}
	if !strings.Contains(g.Notes, ceiling) {
		t.Errorf("command notes lack the ceiling line (%s): %q", ceiling, g.Notes)
	}
	acp := validateReviewer(config.AgentBinding{Interface: "acp", Command: "cursor-agent acp"})
	ga := cursorGrant(t, acp, "judge")
	if !strings.Contains(ga.Notes, note) || !strings.Contains(ga.Notes, ceiling) {
		t.Errorf("acp notes = %q, want the advisory note and the %s ceiling line", ga.Notes, ceiling)
	}
	if strings.Contains(ga.Notes, "acp permission policy + tools") {
		t.Errorf("acp notes must not claim the tools grant is the ceiling: %q", ga.Notes)
	}

	// The reviewer description (what a dispatch records) and the isolation preflight.
	for _, tc := range []struct{ iface, command, wantAdapter string }{
		{agentcli.InterfaceCommand, agentcli.CursorCommand, "cursor command"},
		{agentcli.InterfaceACP, "cursor-agent acp", "cursor acp"},
	} {
		runner, err := agentcli.RunnerFromBinding(tc.iface, tc.command)
		if err != nil {
			t.Fatal(err)
		}
		iso := agentcli.DescribeReviewer(runner, agentcli.Request{ReadOnly: true})
		if iso.Adapter != tc.wantAdapter {
			t.Errorf("%s: adapter = %q", tc.iface, iso.Adapter)
		}
		for _, want := range []string{"cursor", "no --tools allow-list", "read-only is enforced by"} {
			if !strings.Contains(iso.OfferedSource, want) {
				t.Errorf("%s: OfferedSource %q lacks %q", tc.iface, iso.OfferedSource, want)
			}
		}
		if gaps := agentcli.PreflightReviewer(tc.iface, tc.command, ""); len(gaps) != 0 {
			t.Errorf("%s: a mode-held cursor reviewer has no preflight gap: %+v", tc.iface, gaps)
		}
	}
	if gaps := agentcli.PreflightReviewer(agentcli.InterfaceCommand, "cursor-agent -p --output-format json --mode agent", ""); len(gaps) != 1 ||
		!strings.Contains(gaps[0].What, "cursor") {
		t.Errorf("a writing mode must be a cursor-named preflight gap: %+v", gaps)
	}

	// Other adapters' notes are unchanged: claude carries no advisory-tools note.
	cl := validateReviewer(config.AgentBinding{Command: agentcli.DefaultClaudeCommand, Tools: "Read,Grep,Glob"})
	if n := cursorGrant(t, cl, "judge").Notes; strings.Contains(n, "advisory") || strings.Contains(n, "forced") {
		t.Errorf("claude notes must not carry cursor's: %q", n)
	}
}
