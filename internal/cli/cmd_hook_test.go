package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/lease"
	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/wfdot"
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
		{"/tmp/claude/scratch/foo.sh", false}, // outside root (withinRoot helper only; fence uses gitRootOf)
		{"/home/u/other/x.go", false},         // outside root (foreign only if that path has a .git tree)
		{"", true},                            // empty target — stay conservative (no path → other rules)
	}
	for _, c := range cases {
		if got := withinRoot(root, c.target); got != c.want {
			t.Errorf("withinRoot(%q, %q) = %v, want %v", root, c.target, got, c.want)
		}
	}
}

// TestOutsideRepoRefusalMessage pins the foreign-tree lock copy (sty_a8454d10):
// names the path and foreign root; points at create/engage in THAT repo.
func TestOutsideRepoRefusalMessage(t *testing.T) {
	msg := outsideRepoEditErr("/home/u/satelle/internal/cli/cmd_publish.go", "/home/u/satelle").Error()
	for _, want := range []string{
		"another repo's tree",
		"/home/u/satelle/internal/cli/cmd_publish.go",
		"/home/u/satelle",
		"create/engage the story there",
		"satelle story create",
		"Temp/non-repo",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("message missing %q:\n%s", want, msg)
		}
	}
}

// TestAnchorFrom (sty_aadd4d6c AC5): env pin beats cfgRoot; falls through when unset.
func TestAnchorFrom(t *testing.T) {
	pin := "/pinned/home"
	cfg := "/cfg/root"
	got := anchorFrom(func(k string) string {
		if k == "SATELLE_PROJECT_DIR" {
			return pin
		}
		return ""
	}, cfg)
	if got != pin {
		// Abs may expand; compare cleaned suffix.
		if !strings.HasSuffix(got, pin) && got != pin {
			// On Linux Abs of absolute is identity.
			if got != pin {
				// filepath.Abs on absolute path returns cleaned absolute.
				want, _ := filepath.Abs(pin)
				if got != want {
					t.Errorf("SATELLE_PROJECT_DIR pin: got %q want %q", got, want)
				}
			}
		}
	}
	got = anchorFrom(func(k string) string {
		if k == "CLAUDE_PROJECT_DIR" {
			return pin
		}
		return ""
	}, cfg)
	want, _ := filepath.Abs(pin)
	if got != want && got != pin {
		t.Errorf("CLAUDE_PROJECT_DIR pin: got %q want %q", got, want)
	}
	// Pin beats divergent cfgRoot.
	got = anchorFrom(func(k string) string {
		if k == "CLAUDE_PROJECT_DIR" {
			return pin
		}
		return ""
	}, "/other/cfg")
	if got != want && got != pin {
		t.Errorf("pin must beat cfgRoot: got %q", got)
	}
	// Fall through to cfgRoot when unset.
	got = anchorFrom(func(string) string { return "" }, cfg)
	wantCfg, _ := filepath.Abs(cfg)
	if got != wantCfg && got != cfg {
		t.Errorf("fallback cfgRoot: got %q want %q", got, wantCfg)
	}
	if got := anchorFrom(func(string) string { return "" }, ""); got != "" {
		t.Errorf("empty getenv+cfg: got %q", got)
	}
}

// TestOutsideAnchorBashReason names the path, foreign root, and opt-in (sty_a8454d10).
func TestOutsideAnchorBashReason(t *testing.T) {
	msg := outsideAnchorBashReason("/home/u/other/file.go", "/home/u/other")
	for _, want := range []string{
		"/home/u/other/file.go",
		"/home/u/other",
		"another repo's tree",
		"allow_outside_tree_edits",
		"open a session",
		"Temp/non-repo",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("outsideAnchorBashReason missing %q:\n%s", want, msg)
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

// routeWFs builds the two halves of a derived route — the only representation a
// lifecycle has (sty_d953c5d8). A category-specific lane is a `## <category>`
// SECTION in done.md rather than a second workflow file.
func routeWFs(done, step string) []docindex.Doc {
	mk := func(name, what, body string) docindex.Doc {
		return docindex.Doc{Kind: "workflows", Name: name,
			Body: "---\nname: " + name + "\ntype: workflow\nscope: project\ndescription: " + what + "\n---\n\n" + body}
	}
	return []docindex.Doc{
		mk("done", "fixture declaration of done", done),
		mk("step", "fixture step catalogue", step),
	}
}

func TestAnyEngagedCountsTasks(t *testing.T) {
	// One wildcard workflow governs both the story and the task; commit_push is a
	// performing named-agent node.
	wfs := routeWFs(
		"## *\n- raised\n- coded\n- pushed\n- closed\n",
		"## backlog\nstart: true\nprovides: raised\n\n"+
			"## in_progress\nagent: executor\nprovides: coded\nrequires: raised\n\n"+
			"## commit_push\nagent: commit-agent\nskills: commit-push\nprovides: pushed\nrequires: coded\n\n"+
			"## done\nterminal: true\nprovides: closed\nrequires: pushed\n")
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
	wfs := routeWFs(
		"## *\n- raised\n- coded\n- closed\n\n"+
			"## feature\n- raised\n- planned\n- feat-coded\n- feat-closed\n",
		"## backlog\nstart: true\nprovides: raised\n\n"+
			"## in_progress\nagent: executor\nprovides: coded\nrequires: raised\n\n"+
			"## plan\nagent: executor\nprovides: planned\nrequires: raised\n\n"+
			"## in_progress\nagent: coder\nskills: code\nprovides: feat-coded\nrequires: planned\n\n"+
			"## done\nterminal: true\nprovides: closed\nrequires: coded\n\n"+
			"## done\nterminal: true\nprovides: feat-closed\nrequires: feat-coded\n")
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
	// No wildcard section: a chore item resolves to nothing, which is the
	// fail-closed case under test.
	wfs := routeWFs(
		"## feature\n- raised\n- coded\n- closed\n",
		"## backlog\nstart: true\nprovides: raised\n\n"+
			"## in_progress\nagent: executor\nprovides: coded\nrequires: raised\n\n"+
			"## done\nterminal: true\nprovides: closed\nrequires: coded\n")
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
	wired := `{"hooks":{"PreToolUse":[{"matcher":"Edit|Write","hooks":[{"type":"command","command":"sh -c '#satelle-failvisible\nb hook gate'"}]}]}}`
	if !settingsWiresGate([]byte(wired)) {
		t.Errorf("wired settings should report gate wired")
	}
	// sty_adfb9862 script-file form (no "hook gate" substring in the command).
	script := `{"hooks":{"PreToolUse":[{"matcher":"Edit|Write","hooks":[{"type":"command","command":"sh .satelle/hooks/satelle-hook.sh gate claude"}]}]}}`
	if !settingsWiresGate([]byte(script)) {
		t.Errorf("script-file PreToolUse gate must count as wired")
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
		`{"hooks":{"PreToolUse":[{"matcher":"Edit|Write|MultiEdit|NotebookEdit","hooks":[{"type":"command","command":"sh -c '#satelle-failvisible b hook gate'"}]}]}}`)
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
	// Prompt self-check may load global/config paths via GlobalDir (sty_c36c211f).
	t.Setenv("SATELLE_HOME", t.TempDir())
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

// TestEvaluateSeatPerformingAndStale: the pure engagement predicate
// (sty_1738f973 AC2/AC5/AC6).
func TestEvaluateSeatPerformingAndStale(t *testing.T) {
	wfs := routeWFs(
		"## *\n- raised\n- planned\n- coded\n- closed\n",
		"## backlog\nstart: true\nprovides: raised\n\n"+
			"## plan\nagent: executor\nprovides: planned\nrequires: raised\n\n"+
			"## in_progress\nagent: executor\nprovides: coded\nrequires: planned\n\n"+
			"## done\nterminal: true\nprovides: closed\nrequires: coded\n")
	now := time.Now().UTC()
	items := []workitem.Item{
		{ID: "sty_live", Kind: workitem.KindStory, Status: "in_progress", Category: "feature"},
		{ID: "sty_backlog", Kind: workitem.KindStory, Status: "backlog", Category: "feature"},
		{ID: "sty_done", Kind: workitem.KindStory, Status: "done", Category: "feature"},
	}

	// Settled live lease on in_progress → engaged.
	live, other, err := evaluateSeat([]lease.Lease{{
		ItemID: "sty_live", State: "in_progress", Owner: "alice",
		AcquiredAt: now.Add(-time.Hour), HeartbeatAt: now.Add(-time.Minute),
	}}, items, wfs, now)
	if err != nil {
		t.Fatal(err)
	}
	if live.ItemID != "sty_live" || !live.Engaged {
		t.Fatalf("live seat: %+v", live)
	}
	if other.ItemID != "" {
		t.Fatalf("unexpected other: %+v", other)
	}

	// Settled lease on done → not engaged.
	live, other, err = evaluateSeat([]lease.Lease{{
		ItemID: "sty_done", State: "done", Owner: "alice",
		AcquiredAt: now.Add(-time.Hour), HeartbeatAt: now.Add(-time.Minute),
	}}, items, wfs, now)
	if err != nil {
		t.Fatal(err)
	}
	if live.ItemID != "" {
		t.Fatalf("done lease must not be live: %+v", live)
	}
	if other.ItemID != "sty_done" {
		t.Fatalf("other should name residue: %+v", other)
	}

	// Stale orphan (in_flight, committed backlog, heartbeat past TTL) → not engaged.
	staleHB := now.Add(-lease.HeartbeatTTL - time.Minute)
	live, other, err = evaluateSeat([]lease.Lease{{
		ItemID: "sty_backlog", State: "plan", Owner: "dead", InFlight: true,
		AcquiredAt: staleHB, HeartbeatAt: staleHB,
	}}, items, wfs, now)
	if err != nil {
		t.Fatal(err)
	}
	if live.ItemID != "" {
		t.Fatalf("stale orphan must not be live: %+v", live)
	}
	if other.ItemID != "sty_backlog" || !other.Stale {
		t.Fatalf("other must name stale orphan: %+v", other)
	}
	// formatSeat / seatSuffix name the holder + inspect path.
	desc := formatSeat(other, now)
	if !strings.Contains(desc, "sty_backlog") || !strings.Contains(desc, "stale") {
		t.Fatalf("formatSeat = %q", desc)
	}
	suf := seatSuffix(other, now)
	if !strings.Contains(suf, "satelle story seat") || !strings.Contains(suf, "seat release sty_backlog") {
		t.Fatalf("seatSuffix = %q", suf)
	}

	// Fresh in-flight at start state (backlog) → engaged (acquire-at-start window).
	live, _, err = evaluateSeat([]lease.Lease{{
		ItemID: "sty_backlog", State: "plan", Owner: "alice", InFlight: true,
		AcquiredAt: now.Add(-time.Minute), HeartbeatAt: now.Add(-time.Second),
	}}, items, wfs, now)
	if err != nil {
		t.Fatal(err)
	}
	if live.ItemID != "sty_backlog" || !live.Engaged {
		t.Fatalf("fresh in-flight at start state must be live: %+v", live)
	}
}

// TestSeatInjectHelpers: SessionStart / prompt seat inject pure paths
// (sty_1738f973 AC6). Removing appendSeatToContext / appendSeatToPrompt /
// formatSeatBlock would fail these — the discoverability path the AC names.
func TestSeatInjectHelpers(t *testing.T) {
	now := time.Now().UTC()
	stale := seatInfo{
		ItemID: "sty_2db78343", State: "plan", Owner: "dead",
		AcquiredAt: now.Add(-40 * time.Minute), HeartbeatAt: now.Add(-40 * time.Minute),
		Stale: true,
	}
	block := formatSeatBlock(stale, now)
	if !strings.Contains(block, "## Engagement seat") {
		t.Fatalf("block missing heading: %q", block)
	}
	if !strings.Contains(block, "sty_2db78343") || !strings.Contains(block, "stale") {
		t.Fatalf("block missing seat id/stale: %q", block)
	}
	if !strings.Contains(block, "satelle story seat release sty_2db78343") {
		t.Fatalf("block missing release path: %q", block)
	}

	merged := appendSeatToContext("principles here", block, alwaysContextCeiling)
	if !strings.Contains(merged, "## Engagement seat") || !strings.HasPrefix(merged, "principles here") {
		t.Fatalf("appendSeatToContext = %q", merged)
	}
	// Ceiling too tight: keep content, drop seat.
	tight := appendSeatToContext("already long content that fills budget", block, 40)
	if strings.Contains(tight, "## Engagement seat") {
		t.Fatalf("ceiling should drop seat: %q", tight)
	}
	// Empty content still injects seat.
	only := appendSeatToContext("", block, alwaysContextCeiling)
	if only != block {
		t.Fatalf("empty content: %q", only)
	}

	prompt := appendSeatToPrompt("edits require an ENGAGED story", stale, now)
	if !strings.Contains(prompt, "sty_2db78343") || !strings.Contains(prompt, "stale") {
		t.Fatalf("prompt seat line: %q", prompt)
	}
	if !strings.Contains(prompt, "satelle story seat") {
		t.Fatalf("prompt missing inspect: %q", prompt)
	}
	// Empty seatInfo is a no-op.
	if got := appendSeatToPrompt("base", seatInfo{}, now); got != "base" {
		t.Fatalf("empty info: %q", got)
	}

	// Live seat (no STALE) still injects owner+ages.
	live := seatInfo{
		ItemID: "sty_live", State: "in_progress", Owner: "alice",
		AcquiredAt: now.Add(-10 * time.Minute), HeartbeatAt: now.Add(-time.Minute),
		Engaged: true,
	}
	liveBlock := formatSeatBlock(live, now)
	if strings.Contains(liveBlock, "STALE") || strings.Contains(strings.ToLower(liveBlock), "stale ") {
		// formatSeat uses "stale Nm" only when Stale=true; live must not.
		if strings.Contains(liveBlock, ", stale ") {
			t.Fatalf("live seat must not say stale: %q", liveBlock)
		}
	}
	if !strings.Contains(liveBlock, "alice") || !strings.Contains(liveBlock, "sty_live") {
		t.Fatalf("live block: %q", liveBlock)
	}
}

func TestDroppedSeatEditReason(t *testing.T) {
	got := droppedSeatEditReason("sty_abc", "plan")
	for _, want := range []string{"sty_abc", "plan", "engagement seat was dropped", "story set sty_abc --status plan"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
	if strings.Contains(got, "Open a story session") {
		t.Error("dropped-seat reason must not use generic create-story message")
	}
}

func TestEditGateDenyReasonPrefersDroppedSeat(t *testing.T) {
	// Pure string path when firstDroppedPerformingSeat finds nothing uses generic.
	// We cannot open real store without isolation; unit-test droppedSeatEditReason
	// content was covered. Here: editGateDenyReason with empty info and no store
	// home falls through to noEngagedStoryEditReason.
	t.Setenv("SATELLE_HOME", t.TempDir())
	got := editGateDenyReason(seatInfo{}, time.Now().UTC())
	if !strings.Contains(got, "without a performing story") {
		t.Fatalf("empty store should use generic no-story reason: %q", got)
	}
	// droppedSeatEditReason must differ from generic.
	drop := droppedSeatEditReason("sty_x", "in_progress")
	if strings.Contains(drop, "without a performing story") {
		t.Fatal("dropped reason must not use generic phrase")
	}
	if !strings.Contains(drop, "seat was dropped") {
		t.Fatalf("want dropped: %q", drop)
	}
	if drop == noEngagedStoryEditReason {
		t.Fatal("dropped and generic must differ")
	}
}

// --- sty_e16a2cd7: per-turn engaged prompt injection ---

// TestPromptNoSeatIsUnchanged: no live seat → body is byte-identical to today's
// static reminder (AC1). Gate-not-wired warning path also unchanged.
func TestPromptNoSeatIsUnchanged(t *testing.T) {
	t.Setenv("SATELLE_HOME", t.TempDir())
	var buf bytes.Buffer
	if err := runHookPrompt(&buf); err != nil {
		t.Fatalf("runHookPrompt: %v", err)
	}
	var got hookContextOut
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("prompt output not valid JSON: %v (%s)", err, buf.String())
	}
	if got.HookSpecificOutput.AdditionalContext != hookPromptReminder {
		t.Errorf("no-seat body not byte-identical to hookPromptReminder:\n got %q\nwant %q",
			got.HookSpecificOutput.AdditionalContext, hookPromptReminder)
	}
}

// TestFormatEngagedPromptNamesNextGate: AC2 — id, status, gates, story set; no static reminder.
func TestFormatEngagedPromptNamesNextGate(t *testing.T) {
	now := time.Now().UTC()
	info := seatInfo{
		ItemID:      "sty_live",
		State:       "in_progress",
		HeartbeatAt: now.Add(-6 * time.Second),
		Engaged:     true,
		Advance: []wfdot.Advance{
			{To: "integration", Gates: []string{"satelle-code-ac-review", "satelle-story-scope-review"}},
		},
	}
	out := formatEngagedPrompt(info, now)
	if out == "" {
		t.Fatal("formatEngagedPrompt returned empty")
	}
	for _, want := range []string{
		"sty_live",
		"in_progress",
		"integration",
		"satelle-code-ac-review",
		"satelle story set sty_live --status integration",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("engaged form missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "edits require an ENGAGED story") {
		t.Errorf("static create-and-engage reminder must be absent:\n%s", out)
	}
	if len(out) > promptEngagedCeiling {
		t.Errorf("engaged form length %d > ceiling %d:\n%s", len(out), promptEngagedCeiling, out)
	}
}

// TestEngagedPromptQuotesOwnWorkflow: AC3 — two invented workflows; each injection
// quotes its own edges/gates and none of the other's. Invented names cannot be
// hardcoded in Go.
func TestEngagedPromptQuotesOwnWorkflow(t *testing.T) {
	wfs := routeWFs(
		"## cat-a\n- started\n- drafted\n- worked\n- shipped\n\n"+
			"## cat-b\n- started\n- triaged\n- built\n- verified\n",
		"## start\nstart: true\nprovides: started\n\n"+
			"## draft\nagent: executor\nprovides: drafted\nrequires: started\n\n"+
			"## work\nagent: executor\nreviewers: wfa-work-gate\nreviewer_agent: reviewer\n"+
			"provides: worked\nrequires: drafted\n\n"+
			"## ship\nterminal: true\nprovides: shipped\nrequires: worked\n\n"+
			"## triage\nagent: executor\nprovides: triaged\nrequires: started\n\n"+
			"## build\nagent: executor\nreviewers: wfb-build-gate\nreviewer_agent: reviewer\n"+
			"provides: built\nrequires: triaged\n\n"+
			"## verify\nterminal: true\nprovides: verified\nrequires: built\n")
	now := time.Now().UTC()
	items := []workitem.Item{
		{ID: "sty_a", Kind: workitem.KindStory, Status: "draft", Category: "cat-a"},
		{ID: "sty_b", Kind: workitem.KindStory, Status: "triage", Category: "cat-b"},
	}
	leases := []lease.Lease{
		{ItemID: "sty_a", State: "draft", Owner: "alice", AcquiredAt: now.Add(-time.Hour), HeartbeatAt: now.Add(-time.Minute)},
		{ItemID: "sty_b", State: "triage", Owner: "bob", AcquiredAt: now.Add(-time.Hour), HeartbeatAt: now.Add(-time.Minute)},
	}
	// evaluateSeat returns the first live seat; probe each lease alone.
	for _, c := range []struct {
		lease    lease.Lease
		item     workitem.Item
		wantTo   string
		wantGate string
		notGate  string
		notState string
	}{
		{leases[0], items[0], "work", "wfa-work-gate", "wfb-build-gate", "build"},
		{leases[1], items[1], "build", "wfb-build-gate", "wfa-work-gate", "work"},
	} {
		live, _, err := evaluateSeat([]lease.Lease{c.lease}, []workitem.Item{c.item}, wfs, now)
		if err != nil {
			t.Fatalf("%s: evaluateSeat: %v", c.item.ID, err)
		}
		if !live.Engaged || live.ItemID != c.item.ID {
			t.Fatalf("%s: live=%+v", c.item.ID, live)
		}
		if len(live.Advance) != 1 || live.Advance[0].To != c.wantTo {
			t.Fatalf("%s: Advance=%+v, want [{%s …}]", c.item.ID, live.Advance, c.wantTo)
		}
		out := formatEngagedPrompt(live, now)
		if !strings.Contains(out, c.wantGate) || !strings.Contains(out, c.wantTo) {
			t.Errorf("%s: engaged form missing own edge/gate:\n%s", c.item.ID, out)
		}
		if strings.Contains(out, c.notGate) || strings.Contains(out, c.notState) {
			t.Errorf("%s: engaged form leaked other workflow:\n%s", c.item.ID, out)
		}
		if strings.Contains(out, "edits require an ENGAGED story") {
			t.Errorf("%s: static reminder present:\n%s", c.item.ID, out)
		}
	}
}

// TestEngagedPromptWithinCeiling: AC4 — pathological DOT still ≤ ceiling or "".
func TestEngagedPromptWithinCeiling(t *testing.T) {
	now := time.Now().UTC()
	// Five long gate names + long id — force degradation ladder.
	longGates := make([]string, 5)
	for i := range longGates {
		longGates[i] = fmt.Sprintf("very-long-gate-name-number-%02d-xxxxxxxx", i)
	}
	info := seatInfo{
		ItemID:      "sty_pathologically_long_identifier_xx",
		State:       "in_progress_with_a_very_long_state_name",
		HeartbeatAt: now,
		Engaged:     true,
		Advance: []wfdot.Advance{
			{To: "next_state_also_quite_long_name_aa", Gates: longGates},
			{To: "next_state_also_quite_long_name_bb", Gates: longGates},
		},
	}
	out := formatEngagedPrompt(info, now)
	if out != "" && len(out) > promptEngagedCeiling {
		t.Errorf("length %d > ceiling %d:\n%s", len(out), promptEngagedCeiling, out)
	}
}

// TestEvaluateSeatAdvanceSkipsTerminalPark: AC5 at evaluateSeat level.
func TestEvaluateSeatAdvanceSkipsTerminalPark(t *testing.T) {
	wfs := routeWFs(
		"## *\n- raised\n- coded\n- integrated\n- released\n- closed\n"+
			"cancel: cancelled @cancel\nrecover: in_progress\n",
		"## backlog\nstart: true\nprovides: raised\n\n"+
			"## in_progress\nagent: executor\nprovides: coded\nrequires: raised\n\n"+
			"## integration\nagent: executor\nreviewers: ac-rev\nreviewer_agent: reviewer\n"+
			"provides: integrated\nrequires: coded\n\n"+
			"## release\nagent: executor\nreviewers: int-rev\nreviewer_agent: reviewer\n"+
			"provides: released\nrequires: integrated\n\n"+
			"## done\nreviewers: rel-rev\nreviewer_agent: reviewer\nterminal: true\n"+
			"provides: closed\nrequires: released\n")
	now := time.Now().UTC()
	// release: only terminal + back-edge → Advance empty.
	live, _, err := evaluateSeat([]lease.Lease{{
		ItemID: "sty_rel", State: "release", Owner: "a",
		AcquiredAt: now.Add(-time.Hour), HeartbeatAt: now.Add(-time.Minute),
	}}, []workitem.Item{{ID: "sty_rel", Kind: workitem.KindStory, Status: "release", Category: "feature"}}, wfs, now)
	if err != nil {
		t.Fatal(err)
	}
	if !live.Engaged {
		t.Fatal("release seat should be engaged")
	}
	if len(live.Advance) != 0 {
		t.Fatalf("release Advance = %+v, want empty", live.Advance)
	}
	// in_progress → integration only.
	live, _, err = evaluateSeat([]lease.Lease{{
		ItemID: "sty_ip", State: "in_progress", Owner: "a",
		AcquiredAt: now.Add(-time.Hour), HeartbeatAt: now.Add(-time.Minute),
	}}, []workitem.Item{{ID: "sty_ip", Kind: workitem.KindStory, Status: "in_progress", Category: "feature"}}, wfs, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(live.Advance) != 1 || live.Advance[0].To != "integration" {
		t.Fatalf("in_progress Advance = %+v", live.Advance)
	}
}

// TestPromptFailsOpen: AC6 — when evaluateSeat cannot resolve the governing
// workflow DOT, runHookPrompt still emits the static reminder and returns nil.
// Empty SATELLE_HOME is the clean no-seat path (covered by TestPromptNoSeatIsUnchanged);
// this forces a real resolve error via a DOT-less workflow doc.
func TestPromptFailsOpen(t *testing.T) {
	repo := tempRepo(t)
	t.Chdir(repo)
	wfDir := filepath.Join(repo, ".satelle", "workflows")
	if err := os.MkdirAll(wfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Workflow with applies_to but NO fenced dot block — GoverningWorkflow finds
	// it, Parse fails, evaluateSeat returns an error.
	wfBody := "---\nname: broken-wf\ntype: workflow\napplies_to: [\"*\"]\n---\n\nNo DOT here.\n"
	if err := os.WriteFile(filepath.Join(wfDir, "broken-wf.md"), []byte(wfBody), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(runtimeDBPath(t))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := db.DocIndex.Sync(ctx, map[string]string{"workflows": wfDir}, time.Now().UTC()); err != nil {
		_ = db.Close()
		t.Fatalf("doc sync: %v", err)
	}
	sty, err := db.Stories.Create(ctx, workitem.CreateInput{
		Kind: workitem.KindStory, Title: "x", Body: "b", AcceptanceCriteria: "1. a",
		Status: "in_progress", Category: "chore",
	}, time.Now().UTC())
	if err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	owner := lease.ResolveOwner()
	if _, _, _, err := db.Leases.Acquire(ctx, sty.ID, "story", owner, "in_progress", true); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Leases.Confirm(ctx, sty.ID, "in_progress"); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	_ = db.Close()

	out, err := runRootIn(t, `{}`, "hook", "prompt")
	if err != nil {
		t.Fatalf("runHookPrompt must return nil on DOT resolve failure: %v\n%s", err, out)
	}
	if !strings.Contains(out, "edits require an ENGAGED story") {
		t.Errorf("reminder missing on fail-open path:\n%s", out)
	}
	// Must not crash into an engaged form that invents gates from a missing DOT.
	if strings.Contains(out, "advance:") {
		t.Errorf("fail-open path must not emit engaged advance form:\n%s", out)
	}
}
