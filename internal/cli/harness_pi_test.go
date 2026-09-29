package cli

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/store"
)

// piBindingsOf reads the bindings table back out of a rendered extension, so a
// test asserts what the FILE says rather than what the Go table intends.
func piBindingsOf(t *testing.T, ext []byte) []piBinding {
	t.Helper()
	s := string(ext)
	const open = "const BINDINGS: Binding[] = "
	i := strings.Index(s, open)
	j := strings.Index(s, ";\n// END satelle bindings")
	if i < 0 || j < i {
		t.Fatalf("bindings block not found in the extension")
	}
	var got []piBinding
	if err := json.Unmarshal([]byte(s[i+len(open):j]), &got); err != nil {
		t.Fatalf("bindings block is not JSON: %v", err)
	}
	return got
}

// AC1: `agents install pi` writes the extension, and it names all five hooks.
func TestAgentsInstallPi_WritesExtension(t *testing.T) {
	repo := tempRepo(t)
	t.Chdir(repo)
	out, err := runRootIn(t, "", "agents", "install", "pi")
	if err != nil {
		t.Fatalf("agents install pi: %v\n%s", err, out)
	}
	if !strings.Contains(out, "created pi scaffold") || strings.Contains(out, "launcher") {
		t.Errorf("pi has a scaffold and no launcher: %s", out)
	}
	raw, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(piExtensionRel)))
	if err != nil {
		t.Fatalf("extension not written: %v", err)
	}
	if !strings.Contains(string(raw), piOwnedMarker) {
		t.Errorf("extension lacks the satelle-owned marker")
	}
	wrapper := filepath.Join(repo, filepath.FromSlash(satelleHookScriptRel))
	if st, err := os.Stat(wrapper); err != nil || st.Mode()&0o111 == 0 {
		t.Errorf("the wrapper the extension calls must exist and be executable: %v", err)
	}
	if !strings.Contains(string(raw), filepath.ToSlash(wrapper)) {
		t.Errorf("extension must call the wrapper by absolute path %s", wrapper)
	}
	forms := map[string]string{}
	for _, b := range piBindingsOf(t, raw) {
		forms[b.Verb] = b.Form
	}
	for verb, form := range map[string]string{
		"gate": piFormWrapper, "commitgate": piFormWrapper,
		"context": piFormDirect, "prompt": piFormDirect, "stopcheck": piFormDirect,
	} {
		if forms[verb] != form {
			t.Errorf("hook %q bound as %q, want %q", verb, forms[verb], form)
		}
	}
	// Idempotent: a second install converges.
	out, err = runRootIn(t, "", "agents", "install", "pi")
	if err != nil || !strings.Contains(out, "unchanged pi scaffold") {
		t.Errorf("second install should be unchanged: %v\n%s", err, out)
	}
}

// AC7: install then remove leaves nothing in the pi config dir; a foreign
// extension and an operator-authored satelle.ts survive.
func TestAgentsRemovePi_NoResidue(t *testing.T) {
	t.Run("clean", func(t *testing.T) {
		repo := tempRepo(t)
		t.Chdir(repo)
		if out, err := runRootIn(t, "", "agents", "install", "pi"); err != nil {
			t.Fatalf("install: %v\n%s", err, out)
		}
		out, err := runRootIn(t, "", "agents", "remove", "pi")
		if err != nil {
			t.Fatalf("remove: %v\n%s", err, out)
		}
		if _, err := os.Stat(filepath.Join(repo, ".pi")); !os.IsNotExist(err) {
			t.Errorf(".pi must be gone entirely: %v", err)
		}
		if _, err := os.Stat(filepath.Join(repo, filepath.FromSlash(satelleHookScriptRel))); !os.IsNotExist(err) {
			t.Errorf("nothing else references the wrapper, so it goes too: %v", err)
		}
		// Removing again is a no-op, not an error.
		if out, err := runRootIn(t, "", "agents", "remove", "pi"); err != nil || !strings.Contains(out, "absent pi scaffold") {
			t.Errorf("second remove: %v\n%s", err, out)
		}
	})
	t.Run("foreign extension survives", func(t *testing.T) {
		repo := tempRepo(t)
		t.Chdir(repo)
		other := filepath.Join(repo, ".pi", "extensions", "other.ts")
		if err := os.MkdirAll(filepath.Dir(other), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(other, []byte("export default () => {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		_, _ = runRootIn(t, "", "agents", "install", "pi")
		if out, err := runRootIn(t, "", "agents", "remove", "pi"); err != nil {
			t.Fatalf("remove: %v\n%s", err, out)
		}
		if _, err := os.Stat(other); err != nil {
			t.Errorf("a foreign extension must survive: %v", err)
		}
		if _, err := os.Stat(filepath.Join(repo, filepath.FromSlash(piExtensionRel))); !os.IsNotExist(err) {
			t.Errorf("satelle's extension must be gone: %v", err)
		}
	})
	t.Run("unowned satelle.ts is left alone", func(t *testing.T) {
		repo := tempRepo(t)
		t.Chdir(repo)
		mine := filepath.Join(repo, filepath.FromSlash(piExtensionRel))
		if err := os.MkdirAll(filepath.Dir(mine), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(mine, []byte("// my own\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		out, err := runRootIn(t, "", "agents", "install", "pi")
		if err != nil || !strings.Contains(out, "an operator-authored") {
			t.Fatalf("install must report the file in the way: %v\n%s", err, out)
		}
		out, err = runRootIn(t, "", "agents", "remove", "pi")
		if err != nil || !strings.Contains(out, "skipped pi scaffold") {
			t.Fatalf("remove must skip it: %v\n%s", err, out)
		}
		if b, _ := os.ReadFile(mine); string(b) != "// my own\n" {
			t.Errorf("operator file was modified: %q", b)
		}
	})
}

// The shared wrapper stays while ANY scaffold references it, pi included.
func TestRemoveClaude_KeepsWrapperWhilePiReferences(t *testing.T) {
	repo := tempRepo(t)
	t.Chdir(repo)
	if out, err := runRootIn(t, "", "agents", "install", "claude"); err != nil {
		t.Fatalf("install claude: %v\n%s", err, out)
	}
	if out, err := runRootIn(t, "", "agents", "install", "pi"); err != nil {
		t.Fatalf("install pi: %v\n%s", err, out)
	}
	wrapper := filepath.Join(repo, filepath.FromSlash(satelleHookScriptRel))
	out, err := runRootIn(t, "", "agents", "remove", "claude")
	if err != nil || !strings.Contains(out, "still referenced") {
		t.Fatalf("remove claude: %v\n%s", err, out)
	}
	if _, err := os.Stat(wrapper); err != nil {
		t.Fatalf("wrapper removed while pi still calls it: %v", err)
	}
	if out, err := runRootIn(t, "", "agents", "remove", "pi"); err != nil {
		t.Fatalf("remove pi: %v\n%s", err, out)
	}
	if _, err := os.Stat(wrapper); !os.IsNotExist(err) {
		t.Errorf("wrapper must go once nothing references it: %v", err)
	}
}

// AC8: with the extension absent nothing about a session changes, and installing
// another harness never creates pi files.
func TestPiNoExtension_NoChange(t *testing.T) {
	repo := tempRepo(t)
	t.Chdir(repo)
	for _, target := range []string{"claude", "grok"} {
		if out, err := runRootIn(t, "", "agents", "install", target); err != nil {
			t.Fatalf("install %s: %v\n%s", target, err, out)
		}
	}
	if _, err := os.Stat(filepath.Join(repo, ".pi")); !os.IsNotExist(err) {
		t.Errorf("installing claude/grok must not create .pi: %v", err)
	}
	for _, f := range DetectScaffoldDrift(repo) {
		if strings.Contains(f.Path, ".pi") {
			t.Errorf("no extension deployed, yet drift reported: %+v", f)
		}
	}
	// init must not adopt a repo that merely has a .pi directory.
	if err := os.MkdirAll(filepath.Join(repo, ".pi"), 0o755); err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	if err := ensureProcessHooks(&sb, repo, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(repo, filepath.FromSlash(piExtensionRel))); !os.IsNotExist(err) {
		t.Errorf("init must not install pi on its own: %v", err)
	}
	if strings.Contains(sb.String(), "pi scaffold") {
		t.Errorf("init output mentions pi: %s", sb.String())
	}
}

// AC9: the pi scaffold covers the same event set as claude and grok, in the same
// invocation form per event, so the three cannot drift apart.
func TestHarnessHooks_PiMatchesFullEventSet(t *testing.T) {
	want := append([]string(nil), fullHookEvents...)
	sort.Strings(want)
	sorted := func(in []string) string {
		out := append([]string(nil), in...)
		sort.Strings(out)
		return strings.Join(out, ",")
	}
	for _, h := range []string{"claude", "grok", "pi"} {
		if got := sorted(harnessHooks(h).events); got != strings.Join(want, ",") {
			t.Errorf("%s events = %s, want %s", h, got, strings.Join(want, ","))
		}
	}

	// piBindings serves exactly the full event set, no more and no fewer.
	byEvent := map[string]map[string]string{} // event -> verb -> form
	for _, b := range piBindings() {
		if byEvent[b.Event] == nil {
			byEvent[b.Event] = map[string]string{}
		}
		byEvent[b.Event][b.Verb] = b.Form
	}
	var events []string
	for e := range byEvent {
		events = append(events, e)
	}
	if sorted(events) != strings.Join(want, ",") {
		t.Fatalf("pi event map serves %s, want %s", sorted(events), strings.Join(want, ","))
	}

	// Per event, pi's verbs and invocation forms equal what the claude scaffold
	// and the grok scaffold actually write.
	for name, build := range map[string][]byte{
		"claude": buildClaudeHookSettings("/repo"),
		"grok":   buildGrokHookSettings("/repo"),
	} {
		var doc struct {
			Hooks map[string][]struct {
				Hooks []struct {
					Command string `json:"command"`
				} `json:"hooks"`
			} `json:"hooks"`
		}
		if err := json.Unmarshal(build, &doc); err != nil {
			t.Fatal(err)
		}
		for _, event := range fullHookEvents {
			got := map[string]string{}
			for _, g := range doc.Hooks[event] {
				for _, h := range g.Hooks {
					verb, form := verbAndForm(h.Command)
					got[verb] = form
				}
			}
			if pi := byEvent[event]; !sameMap(pi, got) {
				t.Errorf("%s: %s verbs/forms = %v, but the %s scaffold writes %v", "pi", event, pi, name, got)
			}
		}
	}

	// The rendered file carries the same table.
	repo := t.TempDir()
	rendered := piBindingsOf(t, buildPiExtension(repo))
	if len(rendered) != len(piBindings()) {
		t.Fatalf("rendered %d bindings, table has %d", len(rendered), len(piBindings()))
	}
}

// verbAndForm classifies one claude/grok scaffold command: the wrapper form
// (`sh …/satelle-hook.sh <sub> <harness>`) or a direct satelle command.
func verbAndForm(cmd string) (verb, form string) {
	f := strings.Fields(cmd)
	for i, w := range f {
		switch {
		case strings.HasSuffix(w, "satelle-hook.sh") && i+1 < len(f):
			return f[i+1], piFormWrapper
		case w == "hook" && i+1 < len(f) && i > 0 && strings.HasSuffix(f[i-1], "satelle"):
			return f[i+1], piFormDirect
		case w == "reindex" && i > 0 && strings.HasSuffix(f[i-1], "satelle"):
			return "reindex", piFormDirect
		}
	}
	return cmd, "?"
}

func sameMap(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// The tool matchers live beside claude's and grok's, anchored whole names.
func TestPiToolMatchersLiveInTheHarnessTable(t *testing.T) {
	hs := harnessHooks("pi")
	if hs.gateMatcher != "edit|write" || hs.commitMatcher != "bash" {
		t.Fatalf("pi matchers = %q / %q", hs.gateMatcher, hs.commitMatcher)
	}
	for _, b := range piBindings() {
		if b.Form == piFormWrapper && b.Tools == "" {
			t.Errorf("wrapper row %s has no tool matcher", b.Verb)
		}
	}
}

// pi's SessionStart limit is a row of its own, deliberately the neutral budget —
// it must not quietly inherit claude's.
func TestPiContextLimitIsItsOwnRow(t *testing.T) {
	emb := config.EmbeddedHarness()
	row, ok := emb["pi"]
	if !ok || row.ContextLimitBytes <= 0 {
		t.Fatalf("[harness.pi] must be a real row: %+v ok=%v", row, ok)
	}
	if got := (config.Config{}).ContextLimit("pi"); got != row.ContextLimitBytes {
		t.Errorf("ContextLimit(pi) = %d, want the pi row %d", got, row.ContextLimitBytes)
	}
	if row.ContextLimitBytes != emb["unknown"].ContextLimitBytes {
		t.Errorf("pi should deliberately take the neutral budget (%d), got %d", emb["unknown"].ContextLimitBytes, row.ContextLimitBytes)
	}
	if got := resolveContextHarness("pi", nil, nil); got != "pi" {
		t.Errorf("--harness pi must select the pi row, got %q", got)
	}
}

// drift: an edited or stale extension is reported, and `satelle init` heals it.
func TestScaffoldDrift_PiExtension(t *testing.T) {
	repo := tempRepo(t)
	t.Chdir(repo)
	if _, _, _, err := scaffoldPiHooks(repo); err != nil {
		t.Fatal(err)
	}
	piDrift := func() []ScaffoldFinding {
		var out []ScaffoldFinding
		for _, f := range DetectScaffoldDrift(repo) {
			if f.Path == piExtensionRel || f.Path == satelleHookScriptRel {
				out = append(out, f)
			}
		}
		return out
	}
	if d := piDrift(); len(d) != 0 {
		t.Fatalf("fresh install must not drift: %+v", d)
	}
	path := filepath.Join(repo, filepath.FromSlash(piExtensionRel))
	raw, _ := os.ReadFile(path)
	if err := os.WriteFile(path, append(raw, []byte("// hand edit\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	if d := piDrift(); len(d) != 1 || d[0].Path != piExtensionRel || d[0].Kind != "content" {
		t.Fatalf("an edited extension must drift: %+v", d)
	}
	var sb strings.Builder
	if err := ensureProcessHooks(&sb, repo, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sb.String(), "updated pi scaffold") {
		t.Errorf("init should say it healed the extension: %s", sb.String())
	}
	if d := piDrift(); len(d) != 0 {
		t.Fatalf("init must heal the extension: %+v", d)
	}
	// A wrapper the extension calls but that is gone is drift too.
	if err := os.Remove(filepath.Join(repo, filepath.FromSlash(satelleHookScriptRel))); err != nil {
		t.Fatal(err)
	}
	if d := piDrift(); len(d) != 1 || d[0].Kind != "missing" {
		t.Fatalf("a missing wrapper must drift: %+v", d)
	}
	// An operator-authored satelle.ts is never drift and never healed.
	if err := os.WriteFile(path, []byte("// mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if d := piDrift(); len(d) != 0 {
		t.Fatalf("an unowned file is not satelle's to report: %+v", d)
	}
}

// piHookEvent is a claude-shaped event, which is what the extension sends.
func piHookEvent(tool, key, val string) string {
	b, _ := json.Marshal(map[string]any{
		"hook_event_name": "PreToolUse", "tool_name": tool,
		"tool_input": map[string]any{key: val},
	})
	return string(b)
}

// noPerformingStoryRepo is a governed repo where no story is in a performing
// state: the only story sits in backlog, so nothing is engaged or engageable.
func noPerformingStoryRepo(t *testing.T) string {
	t.Helper()
	repo, id := stopcheckRepo(t, seatNone)
	db, err := store.Open(runtimeDBPath(t))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Stories.SetStatus(context.Background(), id, "backlog", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	return repo
}

// AC2: the edit gate, invoked as the extension invokes it, REFUSES an ungated
// edit and the refusal names the edit-gate rule.
func TestHookGate_PiHarness_DeniesUngatedEdit(t *testing.T) {
	noPerformingStoryRepo(t)
	out, err := runRootIn(t, piHookEvent("Edit", "file_path", "internal/foo.go"), "hook", "gate", "--harness", "pi")
	if err == nil {
		t.Fatalf("an ungated edit must be refused; got allow:\n%s", out)
	}
	var doc claudePreToolUseDenyOut
	if jerr := json.Unmarshal([]byte(strings.TrimSpace(out)), &doc); jerr != nil {
		t.Fatalf("pi takes the claude deny envelope: %v\n%s", jerr, out)
	}
	if doc.HookSpecificOutput.PermissionDecision != "deny" || !strings.Contains(doc.HookSpecificOutput.PermissionDecisionReason, noEngagedStoryEditReason) {
		t.Fatalf("deny must name the edit-gate rule: %+v", doc.HookSpecificOutput)
	}
}

// AC3: the shipped exemptions hold with no story engaged — one path prefix and
// one glob, taken from the seeded defaults rather than restated here.
func TestHookGate_PiHarness_HonoursExemptions(t *testing.T) {
	repo := noPerformingStoryRepo(t)
	cfg := "[review]\ngate_create = false\n\n[gate]\nedit_exempt_paths = " + defaultEditExemptTOML() +
		"\nedit_exempt_globs = " + defaultEditExemptGlobsTOML() + "\n"
	if err := os.WriteFile(filepath.Join(repo, ".satelle", "satelle.toml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	pathExempt := defaultEditExemptPaths()[0] + "documents/note.md"
	globExempt := strings.Replace(managedEditExemptGlobs[0], "*", "sty_0123abcd", 1)
	for _, target := range []string{pathExempt, globExempt} {
		if out, err := runRootIn(t, piHookEvent("Write", "file_path", target), "hook", "gate", "--harness", "pi"); err != nil {
			t.Errorf("%s is a shipped exemption and must be allowed: %v\n%s", target, err, out)
		}
	}
	// Control: the same session, a product path, is still refused.
	if _, err := runRootIn(t, piHookEvent("Edit", "file_path", "internal/foo.go"), "hook", "gate", "--harness", "pi"); err == nil {
		t.Errorf("the exemptions must not open the gate for product code")
	}
}

// AC4 (allow): an engaged story's edit passes the gate.
func TestHookGate_PiHarness_AllowsEngagedEdit(t *testing.T) {
	stopcheckRepo(t, seatMine)
	if out, err := runRootIn(t, piHookEvent("Edit", "file_path", "internal/foo.go"), "hook", "gate", "--harness", "pi"); err != nil {
		t.Fatalf("an engaged story's edit must be allowed: %v\n%s", err, out)
	}
}

// AC4: commit/push is held to commitgate — allowed when engaged, refused when
// not, refused into a foreign tree, and refused by a step-scoped git policy.
func TestHookCommitgate_PiHarness(t *testing.T) {
	commit := func(t *testing.T, command string) (string, error) {
		return runRootIn(t, piHookEvent("Bash", "command", command), "hook", "commitgate", "--harness", "pi")
	}
	t.Run("engaged commit allowed", func(t *testing.T) {
		stopcheckRepo(t, seatMine)
		if out, err := commit(t, "git commit -m x"); err != nil {
			t.Fatalf("engaged commit refused: %v\n%s", err, out)
		}
	})
	t.Run("no story refused", func(t *testing.T) {
		stopcheckRepo(t, seatNone)
		out, err := commit(t, "git commit -m x")
		if err == nil || !strings.Contains(out, `"permissionDecision":"deny"`) || !strings.Contains(out, "no engaged story") {
			t.Fatalf("ungated commit must be refused with the commit rule: %v\n%s", err, out)
		}
	})
	t.Run("foreign tree refused", func(t *testing.T) {
		stopcheckRepo(t, seatMine)
		foreign := t.TempDir()
		if o, err := exec.Command("git", "-C", foreign, "init", "-q").CombinedOutput(); err != nil {
			t.Fatalf("git init: %v\n%s", err, o)
		}
		out, err := commit(t, "git -C "+foreign+" commit -m x")
		if err == nil || !strings.Contains(out, `"permissionDecision":"deny"`) {
			t.Fatalf("a commit into another working tree must be refused: %v\n%s", err, out)
		}
		if !strings.Contains(out, "allow_outside_tree_edits") {
			t.Errorf("the refusal should name the escape hatch: %s", out)
		}
	})
	t.Run("step-scoped git policy refuses push", func(t *testing.T) {
		repo, _ := stopcheckRepo(t, seatMine)
		cfg := "[review]\ngate_create = false\n\n[gate]\nedit_exempt_paths = [\".satelle/\"]\n\n[gate.command_allow]\npush = [\"release\"]\n"
		if err := os.WriteFile(filepath.Join(repo, ".satelle", "satelle.toml"), []byte(cfg), 0o644); err != nil {
			t.Fatal(err)
		}
		out, err := commit(t, "git push origin main")
		if err == nil || !strings.Contains(out, `"permissionDecision":"deny"`) || !strings.Contains(out, "release") {
			t.Fatalf("push at in_progress must be refused by the step policy: %v\n%s", err, out)
		}
		if out, err := commit(t, "git commit -m x"); err != nil {
			t.Errorf("the policy scopes push only; commit stays allowed: %v\n%s", err, out)
		}
	})
}

// AC5 (Go side): the context and prompt hooks answer a pi caller with the
// injected context and the edits-require-a-story reminder.
func TestHookContextAndPrompt_PiHarness(t *testing.T) {
	stopcheckRepo(t, seatNone)
	out, err := runRootIn(t, `{"hook_event_name":"SessionStart","source":"startup"}`, "hook", "context", "--harness", "pi")
	if err != nil {
		t.Fatalf("hook context: %v\n%s", err, out)
	}
	var ctxOut hookContextOut
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &ctxOut); err != nil {
		t.Fatalf("context output is not the additionalContext envelope: %v\n%s", err, out)
	}
	if !strings.Contains(ctxOut.HookSpecificOutput.AdditionalContext, "principles") {
		t.Errorf("session context missing the principles pointer: %q", ctxOut.HookSpecificOutput.AdditionalContext)
	}
	out, err = runRootIn(t, `{"hook_event_name":"UserPromptSubmit","prompt":"go"}`, "hook", "prompt", "--harness", "pi")
	if err != nil {
		t.Fatalf("hook prompt: %v\n%s", err, out)
	}
	var promptOut hookContextOut
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &promptOut); err != nil {
		t.Fatalf("prompt output is not the additionalContext envelope: %v\n%s", err, out)
	}
	if !strings.Contains(promptOut.HookSpecificOutput.AdditionalContext, "edits require an ENGAGED story") {
		t.Errorf("prompt reminder missing the edits-require-a-story rule: %q", promptOut.HookSpecificOutput.AdditionalContext)
	}
}

// AC6 (Go side): stopcheck refuses to finish on an ungated edit, and honours
// stop_hook_active so a re-prompt cannot loop.
func TestHookStopcheck_PiHarness_RefusesUngatedEdit(t *testing.T) {
	repo, _ := stopcheckRepo(t, seatNone)
	dirtyTree(t, repo)
	out, err := runRootIn(t, `{"hook_event_name":"Stop","stop_hook_active":false}`, "hook", "stopcheck", "--harness", "pi")
	if err != nil {
		t.Fatalf("stopcheck: %v\n%s", err, out)
	}
	var blk stopBlockOut
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &blk); err != nil || blk.Decision != "block" {
		t.Fatalf("an ungated edit must block the stop: %v\n%s", err, out)
	}
	if !strings.Contains(blk.Reason, "STOP BLOCKED") || !strings.Contains(blk.Reason, "internal/foo.go") {
		t.Errorf("block reason should name the ungated file: %q", blk.Reason)
	}
	out, err = runRootIn(t, `{"hook_event_name":"Stop","stop_hook_active":true}`, "hook", "stopcheck", "--harness", "pi")
	if err != nil || strings.TrimSpace(out) != "" {
		t.Errorf("a re-prompt already in flight must not be blocked again: %v %q", err, out)
	}
}
