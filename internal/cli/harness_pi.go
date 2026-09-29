// harness_pi.go — the pi harness scaffold (sty_b3c7b37d). pi has no hook config
// file: its only extension point is a TypeScript extension, so where the claude
// and grok scaffolds write a settings JSON, this writes .pi/extensions/satelle.ts.
// The extension calls the same `satelle hook` handlers the other scaffolds wire
// and decides nothing itself; harnessHooks("pi") supplies the tool matchers and
// the event set, piBindings() the pi event each satelle event rides on.
package cli

import (
	_ "embed"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

//go:embed satelle_pi_extension.ts.tmpl
var piExtensionTemplate string

const (
	// piExtensionRel is the repo-relative path of the satelle-owned pi extension.
	piExtensionRel = ".pi/extensions/satelle.ts"
	// piOwnedMarker is what makes the file satelle's to overwrite and remove; a
	// satelle.ts without it is the operator's and is left alone.
	piOwnedMarker = "// satelle-owned:"

	// Invocation forms — the same split buildClaudeHookSettings makes: PreToolUse
	// goes through the fail-visible wrapper, everything else is a direct command.
	piFormWrapper = "wrapper"
	piFormDirect  = "direct"

	piWrapperTimeoutSec = 60
	piDirectTimeoutSec  = 30
)

// piBinding is one row of the pi event map: which satelle hook event a pi event
// serves, how it is invoked and which handler it calls. It is rendered into the
// extension as JSON, so the TypeScript never carries a tool name or verb of its own.
type piBinding struct {
	Event    string   `json:"event"`           // the fullHookEvents entry this row serves
	Pi       string   `json:"pi"`              // the pi extension event it is registered on
	Form     string   `json:"form"`            // piFormWrapper | piFormDirect
	Verb     string   `json:"verb"`            // the satelle hook handler
	Argv     []string `json:"argv,omitempty"`  // direct rows: the satelle argv
	Tools    string   `json:"tools,omitempty"` // wrapper rows: anchored pi tool-name alternation
	TimeoutS int      `json:"timeout_s"`
}

// piBindings is the pi event map, in registration order. The tool matchers come
// from harnessHooks("pi") — beside the claude and grok matchers — and the Stop
// timeout from the constant the claude scaffold uses, so neither is restated.
func piBindings() []piBinding {
	hs := harnessHooks("pi")
	direct := func(verb string) []string { return []string{"hook", verb, "--harness", "pi"} }
	return []piBinding{
		{Event: "PreToolUse", Pi: "tool_call", Form: piFormWrapper, Verb: "gate", Tools: hs.gateMatcher, TimeoutS: piWrapperTimeoutSec},
		{Event: "PreToolUse", Pi: "tool_call", Form: piFormWrapper, Verb: "commitgate", Tools: hs.commitMatcher, TimeoutS: piWrapperTimeoutSec},
		{Event: "SessionStart", Pi: "session_start", Form: piFormDirect, Verb: "reindex", Argv: []string{"reindex"}, TimeoutS: piDirectTimeoutSec},
		{Event: "SessionStart", Pi: "session_start", Form: piFormDirect, Verb: "context", Argv: direct("context"), TimeoutS: piDirectTimeoutSec},
		{Event: "UserPromptSubmit", Pi: "before_agent_start", Form: piFormDirect, Verb: "prompt", Argv: direct("prompt"), TimeoutS: piDirectTimeoutSec},
		{Event: "Stop", Pi: "agent_settled", Form: piFormDirect, Verb: "stopcheck", Argv: direct("stopcheck"), TimeoutS: stopHookTimeoutSec},
	}
}

// buildPiExtension returns the .pi/extensions/satelle.ts bytes for repoRoot. The
// wrapper path is absolute for the same reason renderHookCommand's is: a shell
// that changed directory must not brick PreToolUse.
func buildPiExtension(repoRoot string) []byte {
	root := repoRoot
	if abs, err := filepath.Abs(repoRoot); err == nil {
		root = abs
	}
	wrapper := filepath.ToSlash(filepath.Join(root, filepath.FromSlash(satelleHookScriptRel)))
	bindings, _ := json.MarshalIndent(piBindings(), "", "\t")
	return []byte(strings.NewReplacer(
		"__SATELLE_REPO_ROOT__", strconv.Quote(filepath.ToSlash(root)),
		"__SATELLE_WRAPPER__", strconv.Quote(wrapper),
		"__SATELLE_BINDINGS__", string(bindings),
	).Replace(piExtensionTemplate))
}

// healPiExtension re-renders an ALREADY installed pi extension during `satelle
// init`. It never installs one: pi is opted into with `satelle agents install pi`,
// so a repo that merely has a .pi directory is left alone, and an operator-owned
// satelle.ts (no marker) is never touched.
func healPiExtension(out io.Writer, repoRoot string) error {
	prev, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(piExtensionRel)))
	if err != nil || !strings.Contains(string(prev), piOwnedMarker) {
		return nil
	}
	added, updated, incomplete, err := scaffoldPiHooks(repoRoot)
	if err != nil {
		return err
	}
	printScaffoldOutcome(out, "pi", piExtensionRel, added, updated, incomplete)
	return nil
}

// scaffoldPiHooks writes the pi extension, or heals it when it has drifted, and
// ensures the shared wrapper it calls exists. added reports a first write;
// updated names what a heal changed. A satelle.ts without the owned marker is the
// operator's: it is never overwritten, and comes back as incomplete.
func scaffoldPiHooks(repoRoot string) (added bool, updated, incomplete []string, err error) {
	if err := writeHookScripts(repoRoot); err != nil {
		return false, nil, nil, err
	}
	path := filepath.Join(repoRoot, filepath.FromSlash(piExtensionRel))
	want := buildPiExtension(repoRoot)
	prev, rerr := os.ReadFile(path)
	switch {
	case os.IsNotExist(rerr):
		added = true
	case rerr != nil:
		return false, nil, nil, rerr
	case !strings.Contains(string(prev), piOwnedMarker):
		return false, nil, []string{"an operator-authored " + piExtensionRel + " is in the way"}, nil
	case string(prev) == string(want):
		return false, nil, nil, nil
	default:
		updated = []string{"extension rewritten from the binary"}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, nil, nil, err
	}
	if err := os.WriteFile(path, want, 0o644); err != nil {
		return false, nil, nil, err
	}
	return added, updated, nil, nil
}
