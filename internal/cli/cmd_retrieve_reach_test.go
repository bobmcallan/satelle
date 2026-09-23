package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/config"
)

// reviewerBindingTOML mirrors THIS repo's own [reviewer] binding in
// .satelle/workflows/agents.toml — the command-transport seat that names
// Bash(satelle:*) in its tools grant and keeps Bash off its --disallowedTools
// list. If that binding ever changes, this fixture (and the test it feeds)
// must change with it — it is deliberately not read live off disk so the test
// stays hermetic (sty_b0577532 AC2, satelle-story-plan-review revision 2).
const reviewerBindingTOML = `[reviewer]
role       = "reviewer"
effort     = "high"
command    = "claude -p --output-format json --disallowedTools Write,Edit,NotebookEdit --append-system-prompt {system} --allowedTools {tools} --model {model} --effort {effort}"
tools      = "Read,Grep,Glob,Bash(satelle:*)"
model      = "opus"
principles = "session"
`

// TestReviewerSeatCanReachRetrieve (AC2 / sty_b0577532): proves the read-only
// reviewer SEAT — this repo's [reviewer] binding — can run `satelle retrieve
// <hash>` end to end.
//
// Command transport (claude -p with argv --allowedTools/--disallowedTools)
// does not consult satelle's own PermissionPolicy; the argv IS the permission
// outcome for this seat, so (a)/(b)/(c) assert the argv-facing config values
// directly. (d) proves the SEPARATE mechanism — satelle's own PreToolUse
// containment/engagement classification — does not itself block the command
// if the reviewer's Bash calls ever run under a wired hook.
func TestReviewerSeatCanReachRetrieve(t *testing.T) {
	dataDir := t.TempDir()
	wfDir := filepath.Join(dataDir, "workflows")
	if err := os.MkdirAll(wfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wfDir, "agents.toml"), []byte(reviewerBindingTOML), 0o644); err != nil {
		t.Fatal(err)
	}
	ac, err := config.LoadAgents(dataDir)
	if err != nil {
		t.Fatalf("LoadAgents: %v", err)
	}
	tools := ac.Reviewer.Tools
	command := ac.Reviewer.Command

	// (a) the --allowedTools value (the binding's tools grant) names Bash(satelle:*).
	if !strings.Contains(tools, "Bash(satelle:*)") {
		t.Fatalf("[reviewer] tools = %q, want it to contain Bash(satelle:*)", tools)
	}
	// (b) no --disallowedTools value denies Bash outright.
	disallowed := disallowedToolsValue(command)
	if strings.Contains(disallowed, "Bash") {
		t.Fatalf("[reviewer] command --disallowedTools = %q, must not deny Bash (it would beat the allow list)", disallowed)
	}
	// (c) the grant stays read-only under the shared mutator classification.
	if agentcli.GrantAllowsMutators(tools) {
		t.Fatalf("GrantAllowsMutators(%q) = true, want false (a Bash(satelle:*)-only grant is read-only)", tools)
	}

	// (d) satelle's own containment/engagement classification does not flag
	// `satelle retrieve <hash>` as a mutation, so a wired PreToolUse hook would
	// not need (and must not require) an engaged seat to let it through.
	repo := tempRepo(t)
	const cmd = "satelle retrieve 0123456789abcdef01234567"
	if bashMutatesTree(cmd, repo) {
		t.Errorf("bashMutatesTree(%q) = true, want false", cmd)
	}
	if _, foreign := bashMutationTargets(cmd, repo); len(foreign) != 0 {
		t.Errorf("bashMutationTargets(%q) foreign = %v, want none", cmd, foreign)
	}
	if target, ok := foreignSatelleVerb([]string{"satelle", "retrieve", "0123456789abcdef01234567"}, repo); ok {
		t.Errorf("foreignSatelleVerb(retrieve) = (%q, true), want ok=false", target)
	}

	// End-to-end: the commitgate hook allows it with NO engaged story at all —
	// a read-only verb needs no seat.
	out, err := runRootIn(t, bashEvent(cmd), "hook", "commitgate")
	if err != nil {
		t.Fatalf("hook commitgate denied a read-only retrieve with no engaged story: %v\n%s", err, out)
	}
}

// disallowedToolsValue extracts the value following --disallowedTools in a
// command template (best-effort, test-only: the templates are fixed-shape
// space-separated argv strings).
func disallowedToolsValue(command string) string {
	fields := strings.Fields(command)
	for i, f := range fields {
		if f == "--disallowedTools" && i+1 < len(fields) {
			return fields[i+1]
		}
	}
	return ""
}
