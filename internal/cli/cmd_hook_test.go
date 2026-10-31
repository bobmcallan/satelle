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

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/wfdot"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// anyPerforming reports whether any item performs under its own governing
// route. engagedHere records each item engaged from this tree at its committed
// status, so a route that cannot classify it fails closed instead of skipping.
func anyPerforming(items []workitem.Item, wfs []docindex.Doc, engagedHere bool) (bool, error) {
	status := map[string]string{}
	for _, it := range items {
		status[it.ID] = it.Status
	}
	presence := func(id string) (verb.WorkingOnRow, bool) {
		return verb.WorkingOnRow{WorkingOnPayload: verb.WorkingOnPayload{State: status[id]}}, engagedHere
	}
	live, err := evaluateEngagements(items, wfs, nil, presence)
	return len(live) > 0, err
}

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
	content, omitted := renderAlwaysContent("", []docindex.Doc{doc("c", sessionFM)}, alwaysRender{Budget: 16384})
	if len(omitted) > 0 {
		t.Fatalf("unexpected omission: %v", omitted)
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
	content, _ := renderAlwaysContent("", nil, alwaysRender{Budget: 16384})
	if strings.Contains(content, "Always-resident") {
		t.Fatalf("no header expected with empty set:\n%s", content)
	}
	if !strings.Contains(content, alwaysIndexInstruction) {
		t.Fatalf("instruction must always be present")
	}
}

// The project constitution rides FIRST (order-zero), ahead of the principles.
func TestRenderAlwaysContent_constitutionFirst(t *testing.T) {
	content, omitted := renderAlwaysContent("This repo's constitution.", []docindex.Doc{doc("c", sessionFM)}, alwaysRender{Budget: 16384})
	if len(omitted) > 0 {
		t.Fatalf("unexpected omission: %v", omitted)
	}
	if !strings.Contains(content, "# Project constitution") || !strings.Contains(content, "This repo's constitution.") {
		t.Fatalf("constitution not injected:\n%s", content)
	}
	if ci, pi := strings.Index(content, "This repo's constitution."), strings.Index(content, "resident text"); ci < 0 || pi < 0 || ci > pi {
		t.Fatalf("constitution must precede the principles:\n%s", content)
	}
}

// A budget too small for every body degrades to an index — never a silent cut
// (sty_ce1a2733 AC3): the agent is told what was omitted and how to pull it.
func TestRenderAlwaysContent_overLimitIndexesWithReadInstruction(t *testing.T) {
	big := "---\ndescription: a big rule\ntags: [principles:session]\n---\n" + strings.Repeat("x", 300)
	docs := []docindex.Doc{doc("a", big), doc("b", big), doc("c", big)}
	const budget = 1200 // fits one body once the other two are indexed, not two
	content, omitted := renderAlwaysContent("", docs, alwaysRender{Budget: budget, Harness: "claude"})
	if len(omitted) == 0 || len(omitted) == 3 {
		t.Fatalf("want some but not all omitted, got %v", omitted)
	}
	if len(content) > budget {
		t.Fatalf("content %d bytes exceeds the %d budget:\n%s", len(content), budget, content)
	}
	if !strings.Contains(content, "OMITTED FOR THE CLAUDE CONTEXT LIMIT (1200 bytes)") {
		t.Fatalf("directive header missing:\n%s", content)
	}
	for _, name := range omitted {
		if !strings.Contains(content, "satelle doc get principles "+name) {
			t.Errorf("no pull command for omitted %q:\n%s", name, content)
		}
		if strings.Contains(content, "### "+name) {
			t.Errorf("omitted %q was also inlined", name)
		}
	}
	if got := strings.Count(content, "### "); got != 3-len(omitted) {
		t.Errorf("inlined %d bodies, want %d", got, 3-len(omitted))
	}
	if !strings.Contains(content, "a big rule") {
		t.Errorf("index line lost the description:\n%s", content)
	}
}

// An oversized constitution gets a read directive, not a silent truncation.
func TestRenderAlwaysContent_oversizedConstitutionIndexed(t *testing.T) {
	cons := strings.Repeat("c", 3000)
	content, omitted := renderAlwaysContent(cons, []docindex.Doc{doc("c", sessionFM)},
		alwaysRender{Budget: 1200, Harness: "grok", ConstitutionPath: "/repo/.satelle/constitution.md"})
	if len(omitted) == 0 || omitted[0] != "constitution" {
		t.Fatalf("constitution not reported omitted: %v", omitted)
	}
	if !strings.Contains(content, "/repo/.satelle/constitution.md") || strings.Contains(content, cons) {
		t.Fatalf("want a read pointer and no inlined constitution:\n%s", content)
	}
	if len(content) > 1200 {
		t.Fatalf("content %d bytes exceeds the budget", len(content))
	}
}

// The same content renders in full under a large limit and indexed under a
// small one, and the limit is configuration per harness, not a constant
// (sty_ce1a2733 AC2).
func TestRenderAlwaysContent_perHarnessLimit(t *testing.T) {
	cfg := config.Config{Harness: map[string]config.HarnessConfig{
		"claude": {ContextLimitBytes: 700},
		"grok":   {ContextLimitBytes: 64000},
	}}
	big := "---\ntags: [principles:session]\n---\n" + strings.Repeat("x", 300)
	docs := []docindex.Doc{doc("a", big), doc("b", big), doc("c", big)}
	small, smallOmit := renderAlwaysContent("", docs, alwaysRender{Budget: cfg.ContextLimit("claude"), Harness: "claude"})
	large, largeOmit := renderAlwaysContent("", docs, alwaysRender{Budget: cfg.ContextLimit("grok"), Harness: "grok"})
	if len(smallOmit) == 0 || !strings.Contains(small, "OMITTED FOR THE CLAUDE") {
		t.Fatalf("claude's small limit should index:\n%s", small)
	}
	if len(largeOmit) != 0 || strings.Contains(large, "OMITTED") || strings.Count(large, "### ") != 3 {
		t.Fatalf("grok's large limit should inline everything:\n%s", large)
	}
}

// This repo's real constitution and session set fit the claude default, every
// principle either inlined or indexed with its pull command (AC1 backstop).
func TestRenderAlwaysContent_realSubstrateFitsClaudeLimit(t *testing.T) {
	root := filepath.Join("..", "..", ".satelle")
	constPath := filepath.Join(root, "constitution.md")
	constitution := readConstitution(constPath)
	if constitution == "" {
		t.Skip("no constitution in this tree")
	}
	var docs []docindex.Doc
	for _, p := range principleFiles(root) {
		body, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if docHasTag(string(body), sessionTag) {
			docs = append(docs, docindex.Doc{Kind: "principles", Name: strings.TrimSuffix(filepath.Base(p), ".md"), Body: string(body)})
		}
	}
	limit := config.Config{}.ContextLimit("claude")
	content, _ := renderAlwaysContent(constitution, docs, alwaysRender{Budget: limit, Harness: "claude", ConstitutionPath: constPath})
	if len(content) > limit {
		t.Fatalf("content %d bytes exceeds the claude limit %d", len(content), limit)
	}
	for _, d := range docs {
		if !strings.Contains(content, "### "+d.Name+"\n") && !strings.Contains(content, "satelle doc get principles "+d.Name) {
			t.Errorf("principle %q neither inlined nor indexed", d.Name)
		}
	}
}

func TestResolveContextHarness(t *testing.T) {
	claudeEvt := []byte(`{"hook_event_name":"SessionStart","transcript_path":"/home/u/.claude/projects/x/s.jsonl"}`)
	cases := []struct {
		name, flag string
		raw        []byte
		env        []string
		want       string
	}{
		{"flag wins", "grok", claudeEvt, nil, "grok"},
		{"event sniff", "", claudeEvt, nil, "claude"},
		{"env marker", "", []byte(`{}`), []string{"GROK_AGENT=1"}, "grok"},
		{"nothing recognised is unknown, not claude", "", []byte(`{}`), nil, "unknown"},
	}
	for _, c := range cases {
		if got := resolveContextHarness(c.flag, c.raw, c.env); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
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

// TestHarnessFromEvent (sty_5e4bc568 AC2, sty_719c4a7b AC3): a snake_case
// envelope corroborated by a .claude transcript or a Claude-only tool name →
// claude; camelCase-only toolInput → grok; a bare
// permission_mode with no such corroboration → unknown, because Grok's
// Claude-compat shim echoes that key too (real captured grok payloads carry
// it — see agentcli/testdata/hooks/README.md); ambiguous/empty → unknown,
// never claude by default (sty_37fd5470). The deny shape for unknown stays
// strict (TestDenyPreToolUseUnknownHarnessStaysStrict).
func TestHarnessFromEvent(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"claude envelope", `{"session_id":"s1","transcript_path":"/home/u/.claude/projects/p/s1.jsonl","hook_event_name":"PreToolUse","permission_mode":"default","tool_input":{"file_path":"/x.go"}}`, "claude"},
		{"claude tool name", `{"tool_name":"Edit","tool_input":{"command":"git commit -m x"}}`, "claude"},
		{"grok camelCase", `{"toolInput":{"path":"internal/x.go"}}`, "grok"},
		{"grok bash", `{"toolInput":{"command":"git push"}}`, "grok"},
		{"bare snake_case → unknown", `{"tool_input":{"file_path":"/x.go"}}`, "unknown"},
		{"both present → unknown", `{"tool_input":{},"toolInput":{}}`, "unknown"},
		{"empty", `{}`, "unknown"},
		{"null tool_input", `{"tool_input":null}`, "unknown"},
		{"bare permission_mode → unknown", `{"permission_mode":"default"}`, "unknown"},
		{"permission_mode + tool_input, no transcript → unknown", `{"permission_mode":"default","tool_input":{"file_path":"/x.go"}}`, "unknown"},
	}
	for _, c := range cases {
		if got := harnessFromEvent([]byte(c.raw)); got != c.want {
			t.Errorf("%s: harnessFromEvent = %q, want %q", c.name, got, c.want)
		}
	}
}

// TestEmitPreToolUseDenyClaudeEnvelope: a non-Grok harness gets Claude's
// hookSpecificOutput deny, never the top-level decision shape.
func TestEmitPreToolUseDenyClaudeEnvelope(t *testing.T) {
	var buf bytes.Buffer
	if err := emitPreToolUseDeny(&buf, "claude", "no story engaged"); err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatalf("json: %v body=%s", err, buf.String())
	}
	hso, ok := doc["hookSpecificOutput"].(map[string]any)
	if !ok {
		t.Fatalf("want hookSpecificOutput: %s", buf.String())
	}
	if hso["permissionDecision"] != "deny" || hso["permissionDecisionReason"] != "no story engaged" || hso["hookEventName"] != "PreToolUse" {
		t.Errorf("hookSpecificOutput = %v", hso)
	}
	if _, ok := doc["decision"]; ok {
		t.Error("claude deny must not use top-level decision")
	}
}

// TestEmitPreToolUseDenyEmptyReasonFallback: an empty reason is replaced, so a
// deny never reaches the harness without one.
func TestEmitPreToolUseDenyEmptyReasonFallback(t *testing.T) {
	var buf bytes.Buffer
	if err := emitPreToolUseDeny(&buf, "claude", ""); err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	_ = json.Unmarshal(buf.Bytes(), &doc)
	hso, _ := doc["hookSpecificOutput"].(map[string]any)
	if s, _ := hso["permissionDecisionReason"].(string); strings.TrimSpace(s) == "" {
		t.Fatalf("empty reason must be filled: %s", buf.String())
	}
}

// TestDenyPreToolUseUnknownHarnessStaysStrict (sty_37fd5470): an unrecognised
// envelope is recorded "unknown" but still gets the strict hookSpecificOutput
// deny shape and an error, so the sniff change cannot reopen the inert-gate bug.
func TestDenyPreToolUseUnknownHarnessStaysStrict(t *testing.T) {
	prev := hookHarnessFlag
	hookHarnessFlag = ""
	t.Cleanup(func() { hookHarnessFlag = prev })

	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)
	raw := []byte(`{"tool_input":{"file_path":"/tmp/x.go"}}`)
	if harnessFromEvent(raw) != "unknown" {
		t.Fatalf("precondition: envelope must sniff unknown")
	}
	if err := denyPreToolUse(cmd, raw, "no engaged story"); err == nil {
		t.Fatal("denyPreToolUse must return error")
	}
	var doc map[string]any
	if jerr := json.Unmarshal(buf.Bytes(), &doc); jerr != nil {
		t.Fatalf("json: %v body=%s", jerr, buf.String())
	}
	if hso, _ := doc["hookSpecificOutput"].(map[string]any); hso["permissionDecision"] != "deny" {
		t.Fatalf("unknown harness deny not strict: %s", buf.String())
	}
}

// TestDenyPreToolUseRespectsHarnessFlag: --harness grok forces Grok's top-level
// envelope even when the event would sniff as claude (snake_case tool_input).
func TestDenyPreToolUseRespectsHarnessFlag(t *testing.T) {
	prev := hookHarnessFlag
	hookHarnessFlag = "grok"
	t.Cleanup(func() { hookHarnessFlag = prev })

	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)
	raw := []byte(`{"tool_input":{"file_path":"/tmp/x.go"}}`)
	if err := denyPreToolUse(cmd, raw, "no engaged story"); err == nil {
		t.Fatal("denyPreToolUse must return error")
	}
	var doc map[string]any
	if jerr := json.Unmarshal(buf.Bytes(), &doc); jerr != nil {
		t.Fatalf("json: %v body=%s", jerr, buf.String())
	}
	if doc["decision"] != "deny" {
		t.Fatalf("got %v", doc)
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
	if editExempt([]string{dataDir}, repo, resolveAbsTarget(repo, "internal/config/sync.go")) {
		t.Error("a relative product-code path resolved to inside the data dir (the pre-fix bypass)")
	}
	if !editExempt([]string{dataDir}, repo, resolveAbsTarget(repo, ".satelle/skills/plan.md")) {
		t.Error("a relative substrate path under an exempt prefix should classify exempt")
	}
}

func TestReadOnlyPreflightReasonDerivesFromConfig(t *testing.T) {
	withTmp := config.Config{Gate: config.GateConfig{
		EditExemptPaths: []string{"/tmp/"},
		EditExemptGlobs: []string{"sty_*_body.md", "sty_*_ac.md"},
	}}
	got := readOnlyPreflightReasonFrom(func() (config.Config, string, error) {
		return withTmp, "/home/u/repo/.satelle/satelle.toml", nil
	})
	for _, want := range []string{"/tmp/", "sty_*_body.md", "sty_*_ac.md"} {
		if !strings.Contains(got, want) {
			t.Errorf("configured clause missing %q: %s", want, got)
		}
	}
	empty := config.Config{Gate: config.GateConfig{
		EditExemptPaths: []string{},
		EditExemptGlobs: []string{},
	}}
	got = readOnlyPreflightReasonFrom(func() (config.Config, string, error) {
		return empty, "/home/u/repo/.satelle/satelle.toml", nil
	})
	if strings.Contains(got, "sty_*_body.md") {
		t.Errorf("empty authored exemptions must not promise globs: %s", got)
	}
	// Process temp is a mechanism drafting location even when the authored
	// list is empty (sty_e33f78fe) — in-repo paths stay gated.
	namedTemp := false
	for _, p := range tempDraftRoots() {
		if strings.Contains(got, p) {
			namedTemp = true
			break
		}
	}
	if !namedTemp {
		t.Errorf("empty authored exemptions should still name process temp: %s", got)
	}
}

// TestTempDraftTarget is the pure classifier for process-temp writes
// (sty_e33f78fe): out-of-tree temp drafts allow; in-repo product — including
// a repo that lives under the temp dir — does not.
func TestTempDraftTarget(t *testing.T) {
	tmp := os.TempDir()
	outside := "/home/u/repo"
	if withinRoot(tmp, outside) {
		outside = "/var/satelle-test-repo"
	}
	if !tempDraftTarget(outside, filepath.Join(tmp, "anything.txt")) {
		t.Errorf("temp draft under os.TempDir must allow for a repo outside the temp dir")
	}
	// A short-layout dispatch scratch dir under /tmp (sty_b666bc6e).
	if !tempDraftTarget(outside, "/tmp/satelle/0123456789ab/sty_0123abcd/89abcdef/note.md") {
		t.Error("write inside a dispatch scratch dir must stay a temp draft")
	}
	if !tempDraftTarget(outside, "/tmp/scratch-notes.md") {
		t.Error("/tmp draft must allow even when TMPDIR differs")
	}
	if tempDraftTarget(outside, filepath.Join(outside, "internal", "cli", "cmd_hook.go")) {
		t.Error("in-repo product must not be a temp draft")
	}
	tmpRepo := filepath.Join(tmp, "TestTempDraftRepo", "repo")
	if tempDraftTarget(tmpRepo, filepath.Join(tmpRepo, "internal", "cli", "cmd_hook.go")) {
		t.Error("in-tree product of a temp-dir repo must not be a temp draft")
	}
	if tempDraftTarget(outside, "/tmpfoo/x.md") {
		t.Error("/tmpfoo is not under /tmp")
	}
}

// TestEditExemptClassification proves exemption is CONFIG-only (sty_8c3d345c):
// a path under a configured exempt prefix is exempt, code outside every prefix is
// gated, no prefixes means nothing is exempt (no hardcoded data-dir leg), and a
// blank prefix must NOT silently exempt everything. Targets are absolute, as
// callers pass them (resolveAbsTarget runs first).
func TestEditExemptClassification(t *testing.T) {
	const repo = "/home/u/repo"
	exemptRoots := []string{"/home/u/repo/.satelle", "/home/u/repo/.claude"}
	tmpRoots := append(append([]string{}, exemptRoots...), "/tmp/")
	tmpRepo := "/tmp/TestRepo/repo"
	cases := []struct {
		name        string
		exemptRoots []string
		repoRoot    string
		target      string
		want        bool
	}{
		{"configured data dir exempt", exemptRoots, repo, "/home/u/repo/.satelle/skills/x.md", true},
		{"configured harness dir exempt", exemptRoots, repo, "/home/u/repo/.claude/skills/foo/SKILL.md", true},
		{"in-repo code stays gated", exemptRoots, repo, "/home/u/repo/internal/cli/app.go", false},
		{"no prefixes: data dir NOT exempt", nil, repo, "/home/u/repo/.satelle/skills/x.md", false},
		{"no prefixes: code not exempt", nil, repo, "/home/u/repo/internal/cli/app.go", false},
		{"blank prefix does not exempt everything", []string{"  "}, repo, "/home/u/repo/internal/cli/app.go", false},
		{"out-of-tree /tmp draft exempt", tmpRoots, repo, "/tmp/itest-epic-buildout/c1-title.txt", true},
		{"out-of-tree /tmp/x.md exempt", tmpRoots, repo, "/tmp/x.md", true},
		{"/tmpfoo is not under /tmp/", tmpRoots, repo, "/tmpfoo/x.md", false},
		{"/tmp/ prefix does not exempt product in a /tmp repo", append([]string{"/tmp/"}, tmpRepo+"/.satelle"), tmpRepo, tmpRepo + "/internal/cli/cmd_hook.go", false},
		{"no /tmp/ config: out-of-tree /tmp denied", exemptRoots, repo, "/tmp/x.md", false},
	}
	for _, c := range cases {
		if got := editExempt(c.exemptRoots, c.repoRoot, c.target); got != c.want {
			t.Errorf("%s: editExempt(%q) = %v, want %v", c.name, c.target, got, c.want)
		}
	}
}

// TestEditExemptPrefixUnchangedWhenGlobsPresent (sty_fefc88cd AC2): editExempt
// is prefix-only. A populated glob list is a different classifier — it must
// not change prefix results, including a dump name that the glob matcher
// would allow.
func TestEditExemptPrefixUnchangedWhenGlobsPresent(t *testing.T) {
	roots := []string{"/home/u/repo/.satelle"}
	if editExempt(roots, "/home/u/repo", "/home/u/repo/internal/cli/app.go") {
		t.Fatal("product path must stay prefix-gated")
	}
	if editExempt(roots, "/home/u/repo", "/home/u/repo/sty_abc_body.md") {
		t.Fatal("dump basename is not a prefix exemption")
	}
}

// TestEditExemptPatternClassification is the pure matcher table for
// edit_exempt_globs (sty_fefc88cd AC2/AC5): basename match, repo-relative
// when the pattern contains /, blank/bad patterns skipped conservatively.
func TestEditExemptPatternClassification(t *testing.T) {
	const root = "/home/u/repo"
	cases := []struct {
		name     string
		patterns []string
		target   string
		want     bool
	}{
		{"match root", []string{"sty_*_body.md"}, root + "/sty_abc_body.md", true},
		{"match subdir basename", []string{"sty_*_ac.md"}, root + "/notes/sty_abc_ac.md", true},
		{"non-match product", []string{"sty_*_body.md"}, root + "/internal/cli/cmd_hook.go", false},
		{"non-match near miss", []string{"sty_*_body.md"}, root + "/sty_abc_body.go", false},
		{"blank does not exempt everything", []string{"", "sty_*_ac.md"}, root + "/main.go", false},
		{"bad pattern skipped", []string{"sty_[_body.md"}, root + "/sty_x_body.md", false},
		{"path-form match", []string{"docs/sty_*_body.md"}, root + "/docs/sty_a_body.md", true},
		{"path-form does not match other dir", []string{"docs/sty_*_body.md"}, root + "/sty_a_body.md", false},
		{"path-form outside root skipped", []string{"docs/*.md"}, "/tmp/docs/x.md", false},
		{"nil patterns no-op", nil, root + "/sty_abc_body.md", false},
	}
	for _, c := range cases {
		if got := editExemptPattern(c.patterns, root, c.target); got != c.want {
			t.Errorf("%s: editExemptPattern(%v, %q) = %v, want %v", c.name, c.patterns, c.target, got, c.want)
		}
	}
}

// Note: TestExecutorStatesDOT, TestExecutorStatesNamedAgent, and TestExecutorStatesCoderNodeEngaged
// have been removed. The hook now uses wfdot.Spec.NonTerminalEngagingStates() which reads
// shape markers from the DOT. See tests in the wfdot package for shape-based engagement logic.

// routeWFs builds the two halves of a derived route — the only representation a
// lifecycle has (sty_d953c5d8). A category-specific lane is a `[<category>]`
// TABLE in done.toml rather than a second workflow file, and frontmatter is the
// `[meta]` table the TOML form uses (sty_81bb0dde).
func routeWFs(done, step string) []docindex.Doc {
	mk := func(name, what, body string) docindex.Doc {
		return docindex.Doc{Kind: "workflows", Name: name,
			Body: "[meta]\nname = \"" + name + "\"\ntype = \"workflow\"\nscope = \"project\"\ndescription = \"" +
				what + "\"\n\n" + body}
	}
	return []docindex.Doc{
		mk("done", "fixture declaration of done", done),
		mk("step", "fixture step catalogue", step),
	}
}

func TestAnyPerformingCountsTasks(t *testing.T) {
	// One wildcard workflow governs both the story and the task; commit_push is a
	// performing named-agent node.
	wfs := routeWFs(
		`["*"]
obligations = ["raised", "coded", "pushed", "closed"]

# The shipped task category stays in force under a repo file (sty_a4603ea2), so
# the task rides the same lane explicitly.
[task]
obligations = ["raised", "coded", "pushed", "closed"]
`,
		`[raised]
status = "backlog"
start = true

[coded]
status = "in_progress"
agent = "executor"
requires = ["raised"]

[pushed]
status = "commit_push"
agent = "commit-agent"
skills = ["commit-push"]
requires = ["coded"]

[closed]
status = "done"
terminal = true
requires = ["pushed"]
`)
	// A task in a performing state counts as engaged, exactly like a story.
	engaged, err := anyPerforming([]workitem.Item{
		{Kind: workitem.KindTask, Status: "commit_push"},
		{Kind: workitem.KindStory, Status: "backlog"},
	}, wfs, false)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if !engaged {
		t.Error("a task in a performing state should count as engaged")
	}
	// Nothing engaged when no item is in a performing state.
	engaged, err = anyPerforming([]workitem.Item{
		{Kind: workitem.KindTask, Status: "backlog"},
		{Kind: workitem.KindStory, Status: "done"},
	}, wfs, false)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if engaged {
		t.Error("no item in a performing state should not count as engaged")
	}
}

// TestAnyPerformingPerStoryWorkflow: engagement is judged against the story's OWN
// governing workflow, not one global "primary" workflow (sty_f5bd176f). A feature
// story in a feature-workflow's performing `plan` state is engaged even though the
// wildcard workflow has no `plan` node — the exact gap that blocked a dispatched
// coder reached from `plan`.
func TestAnyPerformingPerStoryWorkflow(t *testing.T) {
	wfs := routeWFs(
		`["*"]
obligations = ["raised", "coded", "closed"]

[feature]
obligations = ["raised", "planned", "feat-coded", "feat-closed"]
`,
		`[raised]
status = "backlog"
start = true

[coded]
status = "in_progress"
agent = "executor"
requires = ["raised"]

[planned]
status = "plan"
agent = "executor"
requires = ["raised"]

[feat-coded]
status = "in_progress"
agent = "coder"
skills = ["code"]
requires = ["planned"]

[closed]
status = "done"
terminal = true
requires = ["coded"]

[feat-closed]
status = "done"
terminal = true
requires = ["feat-coded"]
`)
	// A feature story in plan is engaged via the feature workflow (plan performs).
	engaged, err := anyPerforming([]workitem.Item{{Kind: workitem.KindStory, Category: "feature", Status: "plan"}}, wfs, false)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if !engaged {
		t.Error("a feature story in its workflow's performing plan state should be engaged")
	}
	// The same status under the wildcard workflow (no plan node) is NOT engaged —
	// engagement follows the governing workflow, not a global state list.
	engaged, err = anyPerforming([]workitem.Item{{Kind: workitem.KindStory, Category: "chore", Status: "plan"}}, wfs, false)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if engaged {
		t.Error("a chore story in plan is governed by the wildcard workflow (no plan state) — not engaged")
	}
}

// TestAnyPerformingFailClosedNoWorkflow: anyPerforming returns (false, error) — NOT
// (false, nil) — when an item has NO resolving workflow (AC3 fail-closed). With
// no workflow docs at all nothing governs the item, so the hook blocks the edit
// rather than silently allowing it. (A repo route file overlays the shipped route
// by name, so a partial file can no longer leave a category unresolved.)
func TestAnyPerformingFailClosedNoWorkflow(t *testing.T) {
	var wfs []docindex.Doc
	engaged, err := anyPerforming([]workitem.Item{{Kind: workitem.KindStory, Category: "chore", Status: "in_progress"}}, wfs, true)
	if engaged {
		t.Error("a chore item with no resolving workflow must not be engaged")
	}
	if err == nil {
		t.Fatal("anyPerforming must return an error when an item has no resolving workflow (fail-closed)")
	}
}

// TestAnyPerformingFailClosedNoDOT: anyPerforming returns (false, error) when the
// governing workflow body carries no DOT block (AC3 fail-closed) — a non-DOT
// workflow cannot be parsed for shape markers, so the hook blocks rather than guess.
func TestAnyPerformingFailClosedNoDOT(t *testing.T) {
	// A wildcard workflow (governs any item) whose body has no ```dot block.
	noDotBody := "---\nname: wild\napplies_to: [\"*\"]\n---\n# wild\n\nno dot block here\n"
	wfs := []docindex.Doc{{Kind: "workflows", Name: "wild", Body: noDotBody}}
	engaged, err := anyPerforming([]workitem.Item{{Kind: workitem.KindStory, Status: "in_progress"}}, wfs, true)
	if engaged {
		t.Error("an item governed by a non-DOT workflow must not be engaged")
	}
	if err == nil {
		t.Fatal("anyPerforming must return an error when the governing workflow has no DOT (fail-closed)")
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

// TestStopcheckHarnessDoesNotLeak: a cursor stopcheck run in this process
// leaves no harness behind, so a later Stop block is encoded for the default
// harness (the strict shuffled run caught TestEmitStopBlock inheriting cursor).
func TestStopcheckHarnessDoesNotLeak(t *testing.T) {
	t.Setenv("SATELLE_DISPATCH_AGENT", "leak-test") // skip cursor usage recording
	var sink bytes.Buffer
	_ = runHookStopcheck([]byte(`{"stop_hook_active":true,"hook_event_name":"stop","cursor_version":"2026.10.01-e373342","loop_count":0}`), &sink)
	if stopEmitHarness != "" {
		t.Fatalf("stopEmitHarness = %q after stopcheck, want empty", stopEmitHarness)
	}
	var buf bytes.Buffer
	if err := emitStopBlock(&buf, "ungated edits: a.go"); err != nil {
		t.Fatal(err)
	}
	var got stopBlockOut
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil || got.Decision != "block" {
		t.Errorf("stop block after a cursor stopcheck = %s, want decision=block", buf.String())
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
	var got agentcli.HookContextOut
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

// TestEvaluateEngagementsPerformingAndInFlight: the pure engagement predicate.
// An item performs when its committed status is a performing step of its route,
// or when a transition on it is open; the working_on row only says where from.
func TestEvaluateEngagementsPerformingAndInFlight(t *testing.T) {
	wfs := routeWFs(
		`["*"]
obligations = ["raised", "planned", "coded", "closed"]
`,
		`[raised]
status = "backlog"
start = true

[planned]
status = "plan"
agent = "executor"
requires = ["raised"]

[coded]
status = "in_progress"
agent = "executor"
requires = ["planned"]

[closed]
status = "done"
terminal = true
requires = ["coded"]
`)
	now := time.Now().UTC()
	items := []workitem.Item{
		{ID: "sty_live", Kind: workitem.KindStory, Status: "in_progress", Category: "feature"},
		{ID: "sty_backlog", Kind: workitem.KindStory, Status: "backlog", Category: "feature"},
		{ID: "sty_done", Kind: workitem.KindStory, Status: "done", Category: "feature"},
	}
	none := func(string) (verb.WorkingOnRow, bool) { return verb.WorkingOnRow{}, false }

	// Committed performing status → engaged; backlog and done are not.
	live, err := evaluateEngagements(items, wfs, nil, none)
	if err != nil {
		t.Fatal(err)
	}
	if len(live) != 1 || live[0].ItemID != "sty_live" || !live[0].Engaged || live[0].InFlight {
		t.Fatalf("live = %+v, want only sty_live, settled", live)
	}

	// The working_on row names the tree and session it was engaged from.
	here := func(id string) (verb.WorkingOnRow, bool) {
		if id != "sty_live" {
			return verb.WorkingOnRow{}, false
		}
		return verb.WorkingOnRow{WorkingOnPayload: verb.WorkingOnPayload{Session: "s1", Worktree: "/tree/a", State: "in_progress"}, At: now}, true
	}
	live, err = evaluateEngagements(items, wfs, nil, here)
	if err != nil || len(live) != 1 {
		t.Fatalf("live = %+v err = %v", live, err)
	}
	if live[0].Worktree != "/tree/a" || live[0].SessionID != "s1" || !live[0].EngagedAt.Equal(now) {
		t.Fatalf("presence not carried: %+v", live[0])
	}

	// An open transition out of backlog engages the item, in flight toward its
	// target, with no forward options yet.
	open := map[string]ledger.Transition{"sty_backlog": {
		ID: "led_x", StoryID: "sty_backlog", StartedAt: now,
		TransitionStartedPayload: ledger.TransitionStartedPayload{From: "backlog", To: "plan", Pid: os.Getpid()},
	}}
	live, err = evaluateEngagements(items, wfs, open, none)
	if err != nil {
		t.Fatal(err)
	}
	var moving engagement
	for _, e := range live {
		if e.ItemID == "sty_backlog" {
			moving = e
		}
	}
	if !moving.Engaged || !moving.InFlight || moving.TargetState != "plan" || moving.StoryStatus != "backlog" || len(moving.Advance) != 0 {
		t.Fatalf("in-flight engagement = %+v", moving)
	}
	if editPermitted(moving, dispatchMarker{}) {
		t.Fatalf("an edit must be refused while a transition is in flight: %+v", moving)
	}

	// A performing, edit-capable status with nothing in flight permits the edit.
	live, err = evaluateEngagements([]workitem.Item{{ID: "sty_plan", Status: "plan", Kind: workitem.KindStory}}, wfs, nil, none)
	if err != nil || len(live) != 1 {
		t.Fatalf("live = %+v err = %v", live, err)
	}
	if live[0].InFlight || !editPermitted(live[0], dispatchMarker{}) {
		t.Fatalf("edit must be permitted at a settled performing status: %+v", live[0])
	}
}

// TestEngagementInjectHelpers: SessionStart / prompt engagement inject pure
// paths. Removing appendBlockToContext / appendEngagementToPrompt /
// formatEngagement would fail these.
func TestEngagementInjectHelpers(t *testing.T) {
	now := time.Now().UTC()
	e := engagement{
		ItemID: "sty_2db78343", State: "plan", StoryStatus: "plan",
		Worktree: "/work/tree", SessionID: "sess-1", EngagedAt: now, Engaged: true,
	}
	desc := formatEngagement(e)
	for _, want := range []string{"sty_2db78343", "plan", "/work/tree", "sess-1"} {
		if !strings.Contains(desc, want) {
			t.Fatalf("formatEngagement missing %q: %q", want, desc)
		}
	}
	if formatEngagement(engagement{}) != "" {
		t.Fatal("no engagement must format empty")
	}
	moving := e
	moving.InFlight, moving.TargetState = true, "in_progress"
	if !strings.Contains(formatEngagement(moving), `transition to "in_progress" in flight`) {
		t.Fatalf("in-flight engagement must say so: %q", formatEngagement(moving))
	}

	block := "## Engaged stories\n" + desc
	merged := appendBlockToContext("principles here", block, 16384)
	if !strings.Contains(merged, "## Engaged stories") || !strings.HasPrefix(merged, "principles here") {
		t.Fatalf("appendBlockToContext = %q", merged)
	}
	// Ceiling too tight: keep content, drop the block.
	if tight := appendBlockToContext("already long content that fills budget", block, 40); strings.Contains(tight, "## Engaged stories") {
		t.Fatalf("ceiling should drop the block: %q", tight)
	}
	// Empty content still injects the block.
	if only := appendBlockToContext("", block, 16384); only != block {
		t.Fatalf("empty content: %q", only)
	}

	prompt := appendEngagementToPrompt("edits require an ENGAGED story", e)
	if !strings.Contains(prompt, "sty_2db78343") || !strings.Contains(prompt, "/work/tree") {
		t.Fatalf("prompt engagement line: %q", prompt)
	}
	if got := appendEngagementToPrompt("base", engagement{}); got != "base" {
		t.Fatalf("empty info: %q", got)
	}
}

// TestConcurrencyLine: the session inject names the configured concurrency
// rule, so an agent meets it before a refusal does.
func TestConcurrencyLine(t *testing.T) {
	unlimited := concurrencyLine(0)
	if !strings.Contains(unlimited, "unlimited") || !strings.Contains(unlimited, "sequential epic") {
		t.Errorf("unlimited line: %q", unlimited)
	}
	one := concurrencyLine(1)
	for _, want := range []string{"[engagement] limit = 1", "at most 1 performing", "sequential epic"} {
		if !strings.Contains(one, want) {
			t.Errorf("limit line missing %q: %q", want, one)
		}
	}
}

// TestEditGateDenyReasonNamesPerformingTrees: with nothing performing the deny
// is the generic open-a-story text; with stories performing from other trees it
// names each with its tree instead of attributing the edit to one.
func TestEditGateDenyReasonNamesPerformingTrees(t *testing.T) {
	if got := editGateDenyReason(nil, "/here"); got != noEngagedStoryEditReason {
		t.Fatalf("nothing performing should use the generic reason: %q", got)
	}
	live := []engagement{{ItemID: "sty_x", StoryStatus: "in_progress", Worktree: "/there", Engaged: true}}
	got := editGateDenyReason(live, "/here")
	for _, want := range []string{"sty_x", "/there", "/here", "no performing story was engaged from this tree"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
	if strings.Contains(got, "without a performing story") {
		t.Fatal("a performing story elsewhere must not get the generic reason")
	}
}

// --- sty_e16a2cd7: per-turn engaged prompt injection ---

// TestPromptNoEngagementIsUnchanged: nothing engaged → body is byte-identical to
// today's static reminder (AC1). Gate-not-wired warning path also unchanged.
func TestPromptNoEngagementIsUnchanged(t *testing.T) {
	t.Setenv("SATELLE_HOME", t.TempDir())
	var buf bytes.Buffer
	if err := runHookPrompt(&buf); err != nil {
		t.Fatalf("runHookPrompt: %v", err)
	}
	var got agentcli.HookContextOut
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("prompt output not valid JSON: %v (%s)", err, buf.String())
	}
	if got.HookSpecificOutput.AdditionalContext != hookPromptReminder {
		t.Errorf("unengaged body not byte-identical to hookPromptReminder:\n got %q\nwant %q",
			got.HookSpecificOutput.AdditionalContext, hookPromptReminder)
	}
}

// TestFormatEngagedPromptNamesNextGate: AC2 — id, status, gates, story set; no static reminder.
func TestFormatEngagedPromptNamesNextGate(t *testing.T) {
	info := engagement{
		ItemID:  "sty_live",
		State:   "in_progress",
		Engaged: true,
		Advance: []wfdot.Advance{
			{To: "integration", Gates: []string{"satelle-code-ac-review", "satelle-story-scope-review"}},
		},
	}
	out := formatEngagedPrompt(info)
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
		`[cat-a]
obligations = ["started", "drafted", "worked", "shipped"]

[cat-b]
obligations = ["started", "triaged", "built", "verified"]
`,
		`[started]
status = "start"
start = true

[drafted]
status = "draft"
agent = "executor"
requires = ["started"]

[worked]
status = "work"
agent = "executor"
reviewers = ["wfa-work-gate"]
reviewer_agent = "reviewer"
requires = ["drafted"]

[shipped]
status = "ship"
terminal = true
requires = ["worked"]

[triaged]
status = "triage"
agent = "executor"
requires = ["started"]

[built]
status = "build"
agent = "executor"
reviewers = ["wfb-build-gate"]
reviewer_agent = "reviewer"
requires = ["triaged"]

[verified]
status = "verify"
terminal = true
requires = ["built"]
`)
	none := func(string) (verb.WorkingOnRow, bool) { return verb.WorkingOnRow{}, false }
	for _, c := range []struct {
		item     workitem.Item
		wantTo   string
		wantGate string
		notGate  string
		notState string
	}{
		{workitem.Item{ID: "sty_a", Kind: workitem.KindStory, Status: "draft", Category: "cat-a"}, "work", "wfa-work-gate", "wfb-build-gate", "build"},
		{workitem.Item{ID: "sty_b", Kind: workitem.KindStory, Status: "triage", Category: "cat-b"}, "build", "wfb-build-gate", "wfa-work-gate", "work"},
	} {
		live, err := evaluateEngagements([]workitem.Item{c.item}, wfs, nil, none)
		if err != nil {
			t.Fatalf("%s: evaluateEngagements: %v", c.item.ID, err)
		}
		if len(live) != 1 || !live[0].Engaged || live[0].ItemID != c.item.ID {
			t.Fatalf("%s: live=%+v", c.item.ID, live)
		}
		if len(live[0].Advance) != 1 || live[0].Advance[0].To != c.wantTo {
			t.Fatalf("%s: Advance=%+v, want [{%s …}]", c.item.ID, live[0].Advance, c.wantTo)
		}
		out := formatEngagedPrompt(live[0])
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
	// Five long gate names + long id — force degradation ladder.
	longGates := make([]string, 5)
	for i := range longGates {
		longGates[i] = fmt.Sprintf("very-long-gate-name-number-%02d-xxxxxxxx", i)
	}
	info := engagement{
		ItemID:  "sty_pathologically_long_identifier_xx",
		State:   "in_progress_with_a_very_long_state_name",
		Engaged: true,
		Advance: []wfdot.Advance{
			{To: "next_state_also_quite_long_name_aa", Gates: longGates},
			{To: "next_state_also_quite_long_name_bb", Gates: longGates},
		},
	}
	out := formatEngagedPrompt(info)
	if out != "" && len(out) > promptEngagedCeiling {
		t.Errorf("length %d > ceiling %d:\n%s", len(out), promptEngagedCeiling, out)
	}
}

// TestEvaluateEngagementsAdvanceSkipsTerminalPark: AC5 at evaluateEngagements level.
func TestEvaluateEngagementsAdvanceSkipsTerminalPark(t *testing.T) {
	wfs := routeWFs(
		`["*"]
obligations = ["raised", "coded", "integrated", "released", "closed"]
cancel = { state = "cancelled", gate = "cancel" }
recover = { step = "in_progress" }
`,
		`[raised]
status = "backlog"
start = true

[coded]
status = "in_progress"
agent = "executor"
requires = ["raised"]

[integrated]
status = "integration"
agent = "executor"
reviewers = ["ac-rev"]
reviewer_agent = "reviewer"
requires = ["coded"]

[released]
status = "release"
agent = "executor"
reviewers = ["int-rev"]
reviewer_agent = "reviewer"
requires = ["integrated"]

[closed]
status = "done"
reviewers = ["rel-rev"]
reviewer_agent = "reviewer"
terminal = true
requires = ["released"]
`)
	none := func(string) (verb.WorkingOnRow, bool) { return verb.WorkingOnRow{}, false }
	// release: only terminal + back-edge → Advance empty.
	live, err := evaluateEngagements([]workitem.Item{{ID: "sty_rel", Kind: workitem.KindStory, Status: "release", Category: "feature"}}, wfs, nil, none)
	if err != nil {
		t.Fatal(err)
	}
	if len(live) != 1 || !live[0].Engaged {
		t.Fatalf("release should be engaged: %+v", live)
	}
	if len(live[0].Advance) != 0 {
		t.Fatalf("release Advance = %+v, want empty", live[0].Advance)
	}
	// in_progress → integration only.
	live, err = evaluateEngagements([]workitem.Item{{ID: "sty_ip", Kind: workitem.KindStory, Status: "in_progress", Category: "feature"}}, wfs, nil, none)
	if err != nil || len(live) != 1 {
		t.Fatalf("live = %+v err = %v", live, err)
	}
	if len(live[0].Advance) != 1 || live[0].Advance[0].To != "integration" {
		t.Fatalf("in_progress Advance = %+v", live[0].Advance)
	}
}

// TestPromptFailsOpen: AC6 — when the engagement cannot resolve the governing
// workflow, runHookPrompt still emits the static reminder and returns nil.
// Empty SATELLE_HOME is the clean unengaged path (covered by
// TestPromptNoEngagementIsUnchanged); this forces a real resolve error via a
// DOT-less workflow doc and a story engaged here at its status.
func TestPromptFailsOpen(t *testing.T) {
	repo := tempRepo(t)
	t.Chdir(repo)
	wfDir := filepath.Join(repo, ".satelle", "workflows")
	if err := os.MkdirAll(wfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Workflow with applies_to but NO fenced dot block — GoverningWorkflow finds
	// it, Parse fails, evaluateEngagements returns an error.
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
	recordEngagementOn(t, db, sty.ID, "in_progress", sessionWorktreeUncached(), config.ResolveSession())
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
