package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/workitem"
)

func doc(name, body string) docindex.Doc {
	return docindex.Doc{Kind: "principles", Name: name, Body: body}
}

const sessionFM = "---\nname: c\ntags: [kind:principle, principles:session]\n---\n# Body\nresident text\n"
const plainFM = "---\nname: p\ntags: [kind:principle]\n---\n# Other\nnot resident\n"

func TestFrontmatterTags_inlineAndBlock(t *testing.T) {
	inline := frontmatterTags("---\ntags: [a, principles:session, c]\n---\nx")
	if len(inline) != 3 || inline[1] != "principles:session" {
		t.Fatalf("inline parse: %v", inline)
	}
	block := frontmatterTags("---\nname: x\ntags:\n  - a\n  - principles:session\nother: y\n---\nbody")
	if len(block) != 2 || block[1] != "principles:session" {
		t.Fatalf("block parse: %v", block)
	}
	if frontmatterTags("no frontmatter here") != nil {
		t.Fatalf("expected nil tags for no frontmatter")
	}
}

// selectAlwaysDocs is tag-driven: a principle carrying principles:session is
// system-resident (injected); one without the marker is ondemand (not injected).
// Residency is authored substrate, not a hardcoded name (epic:session-context;
// taxonomy sty_1278fdd9).
func TestSelectAlwaysDocs_byResidencyTag(t *testing.T) {
	got := selectAlwaysDocs([]docindex.Doc{
		doc("satelle-agent-goals", sessionFM),    // tagged → system
		doc("satelle-agile-increments", plainFM), // untagged → ondemand
		doc("satelle-constitution", sessionFM),   // tagged → system
	})
	if len(got) != 2 {
		t.Fatalf("want the 2 system-tagged docs, got %d: %v", len(got), got)
	}
	for _, d := range got {
		if d.Name == "satelle-agile-increments" {
			t.Fatalf("ondemand (untagged) principle must not be injected: %v", got)
		}
	}
	// No session-tagged docs → nothing injected.
	if n := len(selectAlwaysDocs([]docindex.Doc{doc("p", plainFM)})); n != 0 {
		t.Fatalf("want 0 when no doc carries the session tag, got %d", n)
	}
}

// AC4 (sty_1278fdd9): scope is NOT an injection classifier. A principle with
// scope: system but no principles:session tag must not be injected.
func TestSelectAlwaysDocs_scopeIsNotClassifier(t *testing.T) {
	scopeOnly := "---\nname: x\nscope: system\ntype: principle\ntags: [type:principle]\n---\n# X\nscope-only body\n"
	got := selectAlwaysDocs([]docindex.Doc{
		doc("scope-only", scopeOnly),
		doc("tagged", sessionFM),
	})
	if len(got) != 1 || got[0].Name != "tagged" {
		t.Fatalf("want only the session-tagged doc, got %v", got)
	}
}

func TestRenderAlwaysContent_bodyStrippedPlusInstruction(t *testing.T) {
	content, truncated := renderAlwaysContent("", []docindex.Doc{doc("c", sessionFM)}, alwaysContextCeiling)
	if truncated {
		t.Fatalf("unexpected truncation")
	}
	if strings.Contains(content, "principles:session") {
		t.Fatalf("frontmatter leaked into injected content:\n%s", content)
	}
	if !strings.Contains(content, "resident text") {
		t.Fatalf("body missing from content:\n%s", content)
	}
	if !strings.Contains(content, alwaysIndexInstruction) {
		t.Fatalf("standing index instruction missing")
	}
}

func TestRenderAlwaysContent_emptySetStillTeachesIndex(t *testing.T) {
	content, _ := renderAlwaysContent("", nil, alwaysContextCeiling)
	if strings.Contains(content, "Always-resident") {
		t.Fatalf("no header expected with empty set:\n%s", content)
	}
	if !strings.Contains(content, alwaysIndexInstruction) {
		t.Fatalf("instruction must always be present")
	}
}

// The project constitution rides FIRST (order-zero), ahead of the principles.
func TestRenderAlwaysContent_constitutionFirst(t *testing.T) {
	content, truncated := renderAlwaysContent("This repo's constitution.", []docindex.Doc{doc("c", sessionFM)}, alwaysContextCeiling)
	if truncated {
		t.Fatalf("unexpected truncation")
	}
	if !strings.Contains(content, "# Project constitution") || !strings.Contains(content, "This repo's constitution.") {
		t.Fatalf("constitution not injected:\n%s", content)
	}
	if ci, pi := strings.Index(content, "This repo's constitution."), strings.Index(content, "resident text"); ci < 0 || pi < 0 || ci > pi {
		t.Fatalf("constitution must precede the principles:\n%s", content)
	}
}

func TestRenderAlwaysContent_ceilingTruncates(t *testing.T) {
	big := "---\ntags: [principles:session]\n---\n" + strings.Repeat("x", 200)
	docs := []docindex.Doc{doc("a", big), doc("b", big), doc("c", big)}
	content, truncated := renderAlwaysContent("", docs, 250) // fits one, not three
	if !truncated {
		t.Fatalf("expected truncation under a tight ceiling")
	}
	if strings.Count(content, "### ") > 1 {
		t.Fatalf("ceiling not enforced — too many docs injected:\n%s", content)
	}
}

// Note: executorStates has been removed. The hook now uses wfdot.Spec.NonTerminalEngagingStates()
// which reads shape markers (Mdiamond=start, Msquare=terminal) from the DOT rather than
// hardcoding state names. See TestNonTerminalEngagingStates in wfdot package.

func TestIsGitCommitOrPush(t *testing.T) {
	yes := []string{"git commit -m x", "cd /r && git push origin main"}
	no := []string{"ls", "git status", "git diff"}
	for _, c := range yes {
		if !isGitCommitOrPush(c) {
			t.Errorf("isGitCommitOrPush(%q) = false, want true", c)
		}
	}
	for _, c := range no {
		// "echo git commit..." DOES contain 'git commit' — accept that v1 is a
		// substring check; only assert the clearly-non-commit cases.
		if c == "echo git commit is a phrase" {
			continue
		}
		if isGitCommitOrPush(c) {
			t.Errorf("isGitCommitOrPush(%q) = true, want false", c)
		}
	}
}

// TestCommitDenyReason (sty_577d292f): deny text teaches pre-execution semantics
// and plan/in_progress; fused engage+commit gets the split-into-two-calls text.
// Gate behavior is unchanged — both paths are still deny strings only.
func TestCommitDenyReason(t *testing.T) {
	// Base deny: pre-execution + both engaging statuses.
	base := commitDenyReason("git commit -m x")
	if base != noEngagedStoryCommitReason {
		t.Fatalf("plain commit deny: got %q", base)
	}
	for _, want := range []string{
		"BEFORE the command executes",
		"SEPARATE, PRIOR",
		"--status plan",
		"--status in_progress",
	} {
		if !strings.Contains(base, want) {
			t.Errorf("noEngagedStoryCommitReason missing %q:\n%s", want, base)
		}
	}

	// Fused pattern: engage + commit in one payload → explicit split message.
	fusedCmd := `./satelle story set sty_abc --status in_progress
git add .
git commit -m "docs (sty_abc)"`
	fused := commitDenyReason(fusedCmd)
	if fused != fusedEngageAndCommitReason {
		t.Fatalf("fused deny: got %q", fused)
	}
	for _, want := range []string{
		"fused engage+commit",
		"BEFORE any line runs",
		"TWO tool calls",
		"plan or in_progress",
	} {
		if !strings.Contains(fused, want) {
			t.Errorf("fusedEngageAndCommitReason missing %q:\n%s", want, fused)
		}
	}

	// story set without --status is not the engage pattern; base deny.
	if got := commitDenyReason("satelle story set sty_x\ngit push"); got != noEngagedStoryCommitReason {
		t.Errorf("story set without --status should use base deny, got %q", got)
	}
	if !isFusedEngageAndCommit(fusedCmd) {
		t.Error("isFusedEngageAndCommit(fused) = false, want true")
	}
	if isFusedEngageAndCommit("git commit -m only") {
		t.Error("isFusedEngageAndCommit(commit only) = true, want false")
	}
	if isFusedEngageAndCommit("satelle story set x --status plan") {
		t.Error("isFusedEngageAndCommit(engage only) = true, want false")
	}
}

func TestBashCommandFromEvent(t *testing.T) {
	// Claude Code snake_case (tool_input.command).
	if got := bashCommandFromEvent([]byte(`{"tool_input":{"command":"git push"}}`)); got != "git push" {
		t.Errorf("claude bashCommandFromEvent = %q, want 'git push'", got)
	}
	// Grok camelCase (toolInput.command) — sty_0d3665ee.
	if got := bashCommandFromEvent([]byte(`{"hookEventName":"pre_tool_use","toolName":"run_terminal_command","toolInput":{"command":"git commit -m x"}}`)); got != "git commit -m x" {
		t.Errorf("grok bashCommandFromEvent = %q, want 'git commit -m x'", got)
	}
	if got := bashCommandFromEvent([]byte(`not json`)); got != "" {
		t.Errorf("bad event should yield empty command, got %q", got)
	}
}

func TestFilePathFromEvent(t *testing.T) {
	// Claude Code snake_case.
	if got := filePathFromEvent([]byte(`{"tool_input":{"file_path":"/a/b.go"}}`)); got != "/a/b.go" {
		t.Errorf("claude file_path = %q, want /a/b.go", got)
	}
	if got := filePathFromEvent([]byte(`{"tool_input":{"notebook_path":"/a/n.ipynb"}}`)); got != "/a/n.ipynb" {
		t.Errorf("claude notebook_path = %q, want /a/n.ipynb", got)
	}
	// Grok camelCase toolInput + nested filePath / path aliases (sty_0d3665ee).
	if got := filePathFromEvent([]byte(`{"toolInput":{"filePath":"/repo/.satelle/skills/x.md"}}`)); got != "/repo/.satelle/skills/x.md" {
		t.Errorf("grok filePath = %q", got)
	}
	if got := filePathFromEvent([]byte(`{"toolInput":{"path":"/tmp/scratch.go"}}`)); got != "/tmp/scratch.go" {
		t.Errorf("grok path = %q", got)
	}
	if got := filePathFromEvent([]byte(`{"toolInput":{"file_path":"/a/via-snake-under-camel.md"}}`)); got != "/a/via-snake-under-camel.md" {
		t.Errorf("grok toolInput.file_path = %q", got)
	}
	if got := filePathFromEvent([]byte(`{}`)); got != "" {
		t.Errorf("absent path should yield empty, got %q", got)
	}
}

func TestWithinRoot(t *testing.T) {
	const root = "/home/u/repo"
	cases := []struct {
		target string
		want   bool // true = inside repo; false = outside (gate REFUSES, sty_3026d890)
	}{
		{"/home/u/repo/internal/x.go", true},  // absolute, in-repo
		{"internal/x.go", true},               // relative, resolved under the repo cwd
		{"/tmp/claude/scratch/foo.sh", false}, // session scratchpad — outside → refuse
		{"/home/u/other/x.go", false},         // sibling dir (e.g. ../satelle) — refuse
		{"", true},                            // empty target — stay conservative (no path → other rules)
	}
	for _, c := range cases {
		if got := withinRoot(root, c.target); got != c.want {
			t.Errorf("withinRoot(%q, %q) = %v, want %v", root, c.target, got, c.want)
		}
	}
}

// TestOutsideRepoRefusalMessage pins the cross-repo lock copy (sty_3026d890):
// sibling paths and /tmp are refused with guidance to create the story in the
// correct repo. Pure check of the error-shaping helper so the harness can rely
// on a stable agent-facing string.
func TestOutsideRepoRefusalMessage(t *testing.T) {
	msg := outsideRepoEditErr("/home/u/satelle/internal/cli/cmd_publish.go").Error()
	for _, want := range []string{
		"refusing edit outside this repo",
		"/home/u/satelle/internal/cli/cmd_publish.go",
		"another project",
		"create/engage the story there",
		"satelle story create",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("message missing %q:\n%s", want, msg)
		}
	}
}

// TestEmitPreToolUseDenyClaudeSchema (sty_5e4bc568 AC1/AC5): Claude-branch deny
// JSON must validate against Claude Code's PreToolUse output shape — root keys
// exactly {hookSpecificOutput}, nested object has the three required fields, and
// NO top-level decision/reason (those fail schema validation and inert the gate).
// Map-key assertion encodes additionalProperties=false without a JSON-schema lib.
func TestEmitPreToolUseDenyClaudeSchema(t *testing.T) {
	var buf bytes.Buffer
	reason := noEngagedStoryEditReason
	if err := emitPreToolUseDeny(&buf, "claude", reason); err != nil {
		t.Fatal(err)
	}
	line := strings.TrimSpace(buf.String())
	var root map[string]any
	if err := json.Unmarshal([]byte(line), &root); err != nil {
		t.Fatalf("deny JSON: %v\n%s", err, line)
	}
	if len(root) != 1 {
		t.Errorf("Claude deny root keys = %v, want exactly {hookSpecificOutput}", keysOf(root))
	}
	if _, ok := root["hookSpecificOutput"]; !ok {
		t.Fatalf("Claude deny missing hookSpecificOutput:\n%s", line)
	}
	if _, ok := root["decision"]; ok {
		t.Errorf("Claude deny must NOT have top-level decision (schema reject):\n%s", line)
	}
	if _, ok := root["reason"]; ok {
		t.Errorf("Claude deny must NOT have top-level reason (schema reject):\n%s", line)
	}
	hso, ok := root["hookSpecificOutput"].(map[string]any)
	if !ok {
		t.Fatalf("hookSpecificOutput not an object: %T", root["hookSpecificOutput"])
	}
	if len(hso) != 3 {
		t.Errorf("hookSpecificOutput keys = %v, want exactly 3", keysOf(hso))
	}
	if hso["hookEventName"] != "PreToolUse" {
		t.Errorf("hookEventName = %v", hso["hookEventName"])
	}
	if hso["permissionDecision"] != "deny" {
		t.Errorf("permissionDecision = %v", hso["permissionDecision"])
	}
	if hso["permissionDecisionReason"] != reason {
		t.Errorf("permissionDecisionReason mismatch")
	}
	// Canonical no-story edit copy: both clauses the operator asked for, plus the
	// session-contract language (sty_8c3d345c) — a story session opens on engage and
	// stays open until a terminal/parked state (done, cancelled, or blocked).
	for _, want := range []string{
		"mutating the tree without a performing story",
		"wrong tool for reading",
		"read_file",
		"search_replace",
		"Open a story session",
		"stays open",
		"done, cancelled, or blocked",
	} {
		if !strings.Contains(reason, want) {
			t.Errorf("canonical reason missing %q:\n%s", want, reason)
		}
	}
}

// TestEmitPreToolUseDenyGrok (sty_5e4bc568 AC2): Grok-branch deny is top-level
// decision=deny + reason with NO hookSpecificOutput.
func TestEmitPreToolUseDenyGrok(t *testing.T) {
	var buf bytes.Buffer
	reason := noEngagedStoryEditReason
	if err := emitPreToolUseDeny(&buf, "grok", reason); err != nil {
		t.Fatal(err)
	}
	line := strings.TrimSpace(buf.String())
	var root map[string]any
	if err := json.Unmarshal([]byte(line), &root); err != nil {
		t.Fatalf("deny JSON: %v\n%s", err, line)
	}
	if root["decision"] != "deny" {
		t.Errorf("Grok decision = %v, want deny", root["decision"])
	}
	if root["reason"] != reason {
		t.Errorf("Grok reason mismatch")
	}
	if _, ok := root["hookSpecificOutput"]; ok {
		t.Errorf("Grok deny must NOT carry hookSpecificOutput:\n%s", line)
	}
	if len(root) != 2 {
		t.Errorf("Grok deny root keys = %v, want {decision,reason}", keysOf(root))
	}
}

// TestHarnessFromEvent (sty_5e4bc568 AC2): snake_case tool_input → claude;
// camelCase-only toolInput → grok; ambiguous/empty → claude (strict default).
func TestHarnessFromEvent(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"claude snake_case", `{"tool_input":{"file_path":"/x.go"}}`, "claude"},
		{"claude bash", `{"tool_input":{"command":"git commit -m x"}}`, "claude"},
		{"grok camelCase", `{"toolInput":{"path":"internal/x.go"}}`, "grok"},
		{"grok bash", `{"toolInput":{"command":"git push"}}`, "grok"},
		{"both present → claude", `{"tool_input":{},"toolInput":{}}`, "claude"},
		{"empty", `{}`, "claude"},
		{"null tool_input", `{"tool_input":null}`, "claude"},
	}
	for _, c := range cases {
		if got := harnessFromEvent([]byte(c.raw)); got != c.want {
			t.Errorf("%s: harnessFromEvent = %q, want %q", c.name, got, c.want)
		}
	}
}

// keysOf returns sorted map keys for stable test diagnostics.
func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestResolveAbsTarget pins the relative-target fix (sty_8c3d345c): a repo-relative
// edit path (as Grok sends) resolves against the REPO ROOT, not against whatever
// narrower root a later containment test uses. This is what stops a relative
// "internal/x.go" from nesting under a tested ".satelle" root and looking exempt.
func TestResolveAbsTarget(t *testing.T) {
	const repo = "/home/u/repo"
	cases := []struct {
		target string
		want   string
	}{
		{"internal/config/sync.go", "/home/u/repo/internal/config/sync.go"}, // relative → under repo root
		{"/home/u/repo/internal/x.go", "/home/u/repo/internal/x.go"},        // absolute → unchanged (cleaned)
		{"/home/u/other/x.go", "/home/u/other/x.go"},                        // absolute outside → unchanged
		{"", ""}, // blank passes through
	}
	for _, c := range cases {
		if got := resolveAbsTarget(repo, c.target); got != c.want {
			t.Errorf("resolveAbsTarget(%q, %q) = %q, want %q", repo, c.target, got, c.want)
		}
	}
	// The crux: a relative code path, once resolved against the repo root, is NOT
	// classed as inside the data dir — the exact mis-classification that let Grok
	// (relative-path) edits bypass the gate before the fix.
	dataDir := "/home/u/repo/.satelle"
	if editExempt([]string{dataDir}, resolveAbsTarget(repo, "internal/config/sync.go")) {
		t.Error("a relative product-code path resolved to inside the data dir (the pre-fix bypass)")
	}
	if !editExempt([]string{dataDir}, resolveAbsTarget(repo, ".satelle/skills/plan.md")) {
		t.Error("a relative substrate path under an exempt prefix should classify exempt")
	}
}

// TestEditExemptClassification proves exemption is CONFIG-only (sty_8c3d345c):
// a path under a configured exempt prefix is exempt, code outside every prefix is
// gated, no prefixes means nothing is exempt (no hardcoded data-dir leg), and a
// blank prefix must NOT silently exempt everything. Targets are absolute, as
// callers pass them (resolveAbsTarget runs first).
func TestEditExemptClassification(t *testing.T) {
	exemptRoots := []string{"/home/u/repo/.satelle", "/home/u/repo/.claude"}
	cases := []struct {
		name        string
		exemptRoots []string
		target      string
		want        bool
	}{
		{"configured data dir exempt", exemptRoots, "/home/u/repo/.satelle/skills/x.md", true},
		{"configured harness dir exempt", exemptRoots, "/home/u/repo/.claude/skills/foo/SKILL.md", true},
		{"in-repo code stays gated", exemptRoots, "/home/u/repo/internal/cli/app.go", false},
		{"no prefixes: data dir NOT exempt", nil, "/home/u/repo/.satelle/skills/x.md", false},
		{"no prefixes: code not exempt", nil, "/home/u/repo/internal/cli/app.go", false},
		{"blank prefix does not exempt everything", []string{"  "}, "/home/u/repo/internal/cli/app.go", false},
	}
	for _, c := range cases {
		if got := editExempt(c.exemptRoots, c.target); got != c.want {
			t.Errorf("%s: editExempt(%q) = %v, want %v", c.name, c.target, got, c.want)
		}
	}
}

// Note: TestExecutorStatesDOT, TestExecutorStatesNamedAgent, and TestExecutorStatesCoderNodeEngaged
// have been removed. The hook now uses wfdot.Spec.NonTerminalEngagingStates() which reads
// shape markers from the DOT. See tests in the wfdot package for shape-based engagement logic.

// wfDoc builds a workflow Doc with the given name, applies_to frontmatter value
// (e.g. `["*"]`), and DOT node/edge lines.
func wfDoc(name, appliesTo, dot string) docindex.Doc {
	body := "---\nname: " + name + "\napplies_to: " + appliesTo + "\n---\n" +
		"```dot\ndigraph w {\n" + dot + "\n}\n```\n"
	return docindex.Doc{Kind: "workflows", Name: name, Body: body}
}

func TestAnyEngagedCountsTasks(t *testing.T) {
	// One wildcard workflow governs both the story and the task; commit_push is a
	// performing named-agent node.
	wfs := []docindex.Doc{wfDoc("wf", `["*"]`, `
  backlog     [shape=Mdiamond]
  in_progress [agent=executor]
  commit_push [agent=commit-agent, prompt="@skill:commit-push"]
  done        [shape=Msquare]
  backlog -> in_progress -> commit_push -> done`)}
	// A task in a performing state counts as engaged, exactly like a story.
	engaged, err := anyEngaged([]workitem.Item{
		{Kind: workitem.KindTask, Status: "commit_push"},
		{Kind: workitem.KindStory, Status: "backlog"},
	}, wfs)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if !engaged {
		t.Error("a task in a performing state should count as engaged")
	}
	// Nothing engaged when no item is in a performing state.
	engaged, err = anyEngaged([]workitem.Item{
		{Kind: workitem.KindTask, Status: "backlog"},
		{Kind: workitem.KindStory, Status: "done"},
	}, wfs)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if engaged {
		t.Error("no item in a performing state should not count as engaged")
	}
}

// TestAnyEngagedPerStoryWorkflow: engagement is judged against the story's OWN
// governing workflow, not one global "primary" workflow (sty_f5bd176f). A feature
// story in a feature-workflow's performing `plan` state is engaged even though the
// wildcard workflow has no `plan` node — the exact gap that blocked a dispatched
// coder reached from `plan`.
func TestAnyEngagedPerStoryWorkflow(t *testing.T) {
	wfs := []docindex.Doc{
		wfDoc("wild", `["*"]`, `
  backlog     [shape=Mdiamond]
  in_progress [agent=executor]
  done        [shape=Msquare]
  backlog -> in_progress -> done`),
		wfDoc("feat", `["feature"]`, `
  backlog     [shape=Mdiamond]
  plan        [agent=executor]
  in_progress [agent=coder, prompt="@skill:code"]
  done        [shape=Msquare]
  backlog -> plan -> in_progress -> done`),
	}
	// A feature story in plan is engaged via the feature workflow (plan performs).
	engaged, err := anyEngaged([]workitem.Item{{Kind: workitem.KindStory, Category: "feature", Status: "plan"}}, wfs)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if !engaged {
		t.Error("a feature story in its workflow's performing plan state should be engaged")
	}
	// The same status under the wildcard workflow (no plan node) is NOT engaged —
	// engagement follows the governing workflow, not a global state list.
	engaged, err = anyEngaged([]workitem.Item{{Kind: workitem.KindStory, Category: "chore", Status: "plan"}}, wfs)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if engaged {
		t.Error("a chore story in plan is governed by the wildcard workflow (no plan state) — not engaged")
	}
}

// TestAnyEngagedFailClosedNoWorkflow: anyEngaged returns (false, error) — NOT
// (false, nil) — when an item has NO resolving workflow (AC3 fail-closed). A chore
// item with only a feature-applies workflow and no wildcard has no governing
// workflow, so the hook blocks the edit rather than silently allowing it.
func TestAnyEngagedFailClosedNoWorkflow(t *testing.T) {
	wfs := []docindex.Doc{wfDoc("feat", `["feature"]`, `
  backlog     [shape=Mdiamond]
  in_progress [agent=executor]
  done        [shape=Msquare]
  backlog -> in_progress -> done`)}
	engaged, err := anyEngaged([]workitem.Item{{Kind: workitem.KindStory, Category: "chore", Status: "in_progress"}}, wfs)
	if engaged {
		t.Error("a chore item with no resolving workflow must not be engaged")
	}
	if err == nil {
		t.Fatal("anyEngaged must return an error when an item has no resolving workflow (fail-closed)")
	}
}

// TestAnyEngagedFailClosedNoDOT: anyEngaged returns (false, error) when the
// governing workflow body carries no DOT block (AC3 fail-closed) — a non-DOT
// workflow cannot be parsed for shape markers, so the hook blocks rather than guess.
func TestAnyEngagedFailClosedNoDOT(t *testing.T) {
	// A wildcard workflow (governs any item) whose body has no ```dot block.
	noDotBody := "---\nname: wild\napplies_to: [\"*\"]\n---\n# wild\n\nno dot block here\n"
	wfs := []docindex.Doc{{Kind: "workflows", Name: "wild", Body: noDotBody}}
	engaged, err := anyEngaged([]workitem.Item{{Kind: workitem.KindStory, Status: "in_progress"}}, wfs)
	if engaged {
		t.Error("an item governed by a non-DOT workflow must not be engaged")
	}
	if err == nil {
		t.Fatal("anyEngaged must return an error when the governing workflow has no DOT (fail-closed)")
	}
}

// --- Reinforcement hooks: gate-liveness self-check + Stop post-hoc detector
// (sty_949e8739) -------------------------------------------------------------

// TestSettingsWiresGate: a settings JSON counts as wiring the edit gate only when
// a PreToolUse Edit-matcher hook invokes `satelle hook gate`.
func TestSettingsWiresGate(t *testing.T) {
	wired := `{"hooks":{"PreToolUse":[{"matcher":"Edit|Write","hooks":[{"type":"command","command":"PATH=x satelle hook gate || exit 2"}]}]}}`
	if !settingsWiresGate([]byte(wired)) {
		t.Errorf("wired settings should report gate wired")
	}
	// Bash-only gate (commitgate) but no Edit-matcher gate → not wired.
	noEdit := `{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"satelle hook commitgate || exit 2"}]}]}}`
	if settingsWiresGate([]byte(noEdit)) {
		t.Errorf("a Bash-only commitgate must NOT count as the edit gate")
	}
	// Edit matcher but a different command → not wired.
	wrongCmd := `{"hooks":{"PreToolUse":[{"matcher":"Edit","hooks":[{"type":"command","command":"echo hi"}]}]}}`
	if settingsWiresGate([]byte(wrongCmd)) {
		t.Errorf("an Edit matcher without `satelle hook gate` must NOT count")
	}
	if settingsWiresGate([]byte("{ not json")) {
		t.Errorf("malformed settings must not report a confident wire")
	}
}

// TestGateWiredInSettings: checked=false when no settings file exists (fail open);
// checked=true+wired reflects the committed files.
func TestGateWiredInSettings(t *testing.T) {
	empty := t.TempDir()
	if wired, checked := gateWiredInSettings(empty); wired || checked {
		t.Errorf("no settings file → want (false,false), got (%v,%v)", wired, checked)
	}

	wiredRepo := t.TempDir()
	mkfile(t, filepath.Join(wiredRepo, ".claude", "settings.json"),
		`{"hooks":{"PreToolUse":[{"matcher":"Edit|Write|MultiEdit|NotebookEdit","hooks":[{"type":"command","command":"satelle hook gate || exit 2"}]}]}}`)
	if wired, checked := gateWiredInSettings(wiredRepo); !wired || !checked {
		t.Errorf("wired .claude → want (true,true), got (%v,%v)", wired, checked)
	}

	// A settings file present but WITHOUT the edit gate → confident missing wire.
	unwired := t.TempDir()
	mkfile(t, filepath.Join(unwired, ".claude", "settings.json"),
		`{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"satelle reindex"}]}]}}`)
	if wired, checked := gateWiredInSettings(unwired); wired || !checked {
		t.Errorf("present-but-unwired → want (false,true), got (%v,%v)", wired, checked)
	}
}

// TestStopHookActive honours both snake_case (Claude) and camelCase (Grok) forms.
func TestStopHookActive(t *testing.T) {
	if !stopHookActive([]byte(`{"stop_hook_active":true}`)) {
		t.Errorf("snake_case stop_hook_active=true not detected")
	}
	if !stopHookActive([]byte(`{"stopHookActive":true}`)) {
		t.Errorf("camelCase stopHookActive=true not detected")
	}
	if stopHookActive([]byte(`{"stop_hook_active":false}`)) {
		t.Errorf("false must not report active")
	}
	if stopHookActive([]byte(`{}`)) {
		t.Errorf("absent flag must not report active")
	}
}

// TestEmitStopBlock: the Stop block payload carries decision=block + the reason.
func TestEmitStopBlock(t *testing.T) {
	var buf bytes.Buffer
	if err := emitStopBlock(&buf, "ungated edits: a.go"); err != nil {
		t.Fatalf("emitStopBlock: %v", err)
	}
	var got stopBlockOut
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("stop block not valid JSON: %v (%s)", err, buf.String())
	}
	if got.Decision != "block" || !strings.Contains(got.Reason, "a.go") {
		t.Errorf("stop block = %+v, want decision=block + reason naming the file", got)
	}
}

// TestStopBlockShape (sty_5e4bc568 AC6): Stop schema honors top-level
// decision=block + reason (NOT PreToolUse hookSpecificOutput). Closed-key check.
func TestStopBlockShape(t *testing.T) {
	var buf bytes.Buffer
	if err := emitStopBlock(&buf, "ungated edits: a.go"); err != nil {
		t.Fatalf("emitStopBlock: %v", err)
	}
	var root map[string]any
	if err := json.Unmarshal(buf.Bytes(), &root); err != nil {
		t.Fatalf("stop block not valid JSON: %v (%s)", err, buf.String())
	}
	if len(root) != 2 {
		t.Errorf("stop block root keys = %v, want {decision,reason}", keysOf(root))
	}
	if root["decision"] != "block" {
		t.Errorf("decision = %v, want block", root["decision"])
	}
	if reason, _ := root["reason"].(string); !strings.Contains(reason, "a.go") {
		t.Errorf("reason should name the file: %v", root["reason"])
	}
	if _, ok := root["hookSpecificOutput"]; ok {
		t.Errorf("Stop block must not use PreToolUse hookSpecificOutput shape")
	}
}

// TestStopcheckReasonCaps: a large dirty set is capped with a "+N more" suffix so
// the reason cannot flood the model.
func TestStopcheckReasonCaps(t *testing.T) {
	var many []string
	for i := 0; i < 25; i++ {
		many = append(many, "f")
	}
	r := stopcheckReason(many)
	if !strings.Contains(r, "+15 more") {
		t.Errorf("expected a +15 more cap suffix, got: %s", r)
	}
	if !strings.Contains(r, "NO story is engaged") {
		t.Errorf("stopcheck reason should state no story is engaged: %s", r)
	}
}

// TestRunHookPromptEmitsReminder: every prompt re-injects the concise rule via a
// UserPromptSubmit additionalContext payload.
func TestRunHookPromptEmitsReminder(t *testing.T) {
	var buf bytes.Buffer
	if err := runHookPrompt(&buf); err != nil {
		t.Fatalf("runHookPrompt: %v", err)
	}
	var got hookContextOut
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("prompt output not valid JSON: %v (%s)", err, buf.String())
	}
	if got.HookSpecificOutput.HookEventName != "UserPromptSubmit" {
		t.Errorf("event = %q, want UserPromptSubmit", got.HookSpecificOutput.HookEventName)
	}
	if !strings.Contains(got.HookSpecificOutput.AdditionalContext, "edits require an ENGAGED story") {
		t.Errorf("prompt reminder missing the engaged-story rule: %s", got.HookSpecificOutput.AdditionalContext)
	}
}

func mkfile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
