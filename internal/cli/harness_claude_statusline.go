package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Claude statusLine ownership (sty_4e6f0788).
//
// Claude Code's settings.json has exactly ONE `statusLine` slot and no
// composition mechanism, so installing satelle's would REPLACE whatever the
// operator wrote. The decision taken is: never clobber. A foreign statusLine is
// left byte-for-byte, the install still succeeds, and the operator is handed the
// snippet to fold satelle's segment into their own script.
//
// This sits inside the ownership boundary satelle's Claude writers already keep:
// creation happens only when the file is absent, healExistingHookFile mutates
// only entries under `hooks`, removeClaudeHooks prunes only satelle hook
// commands, and DetectScaffoldDrift inspects only PreToolUse. Adding one
// top-level key that satelle owns — and can recognise — does not widen it.
//
// Claude ONLY, by capability rather than preference: Grok has no scriptable
// statusline at all, and Codex's built-in `[tui].status_line` accepts a fixed
// item list (model, cwd, git branch, context usage) with no command backing, so
// nothing external can inject into either. Those two harnesses are served by the
// SessionStart availability line (sty_fb5e6d96), which reaches all three. Do not
// "fix" their absence here — it is not an oversight.

// claudeStatusLine is the entry satelle installs. Kept minimal: no padding, no
// refreshInterval — Claude's own debounce is the right cadence and every extra
// key is one more thing an operator has to reconcile.
func claudeStatusLine() map[string]any {
	return map[string]any{"type": "command", "command": statusLineCommand}
}

// isSatelleOwnedStatusLine reports whether an existing statusLine value is ours.
// Ownership is containment of the canonical invocation, not equality, so an
// operator who wrapped it in their own script still reads as satelle-owned and
// keeps getting refreshes instead of a spurious foreign-skip notice.
func isSatelleOwnedStatusLine(v any) bool {
	m, ok := v.(map[string]any)
	if !ok {
		return false
	}
	cmd, _ := m["command"].(string)
	return strings.Contains(cmd, statusLineCommand)
}

// ensureClaudeStatusLine adds or refreshes satelle's statusLine in an EXISTING
// settings.json. It returns a human-readable note when it changed something.
//
// Three outcomes, and only one of them writes:
//   - absent      → add satelle's entry
//   - satelle's   → refresh only if the command drifted from canonical
//   - foreign     → leave untouched, no write, no note (the caller surfaces the
//     skip notice and the paste snippet)
func ensureClaudeStatusLine(path string) (note string, err error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		return "", nil // unparseable — never mutate what we cannot read
	}
	existing, present := root["statusLine"]
	switch {
	case present && !isSatelleOwnedStatusLine(existing):
		return "", nil // foreign — byte-for-byte untouched
	case present:
		if m, ok := existing.(map[string]any); ok {
			if cmd, _ := m["command"].(string); cmd == statusLineCommand {
				return "", nil // already canonical — idempotent no-op
			}
		}
		note = "refreshed statusLine"
	default:
		note = "added statusLine"
	}
	root["statusLine"] = claudeStatusLine()
	b, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		return "", err
	}
	return note, nil
}

// stripSatelleStatusLine removes a satelle-owned statusLine from settings JSON,
// leaving a foreign one and every other top-level key intact. Returns the
// rewritten bytes and whether anything changed.
func stripSatelleStatusLine(raw []byte) (out []byte, changed bool) {
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		return raw, false
	}
	v, present := root["statusLine"]
	if !present || !isSatelleOwnedStatusLine(v) {
		return raw, false
	}
	delete(root, "statusLine")
	b, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return raw, false
	}
	return append(b, '\n'), true
}

// foreignStatusLineNotice returns the skip notice plus the exact snippet the
// operator can fold into their own statusline script, or "" when there is no
// foreign statusLine to report. Read-only.
func foreignStatusLineNotice(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		return ""
	}
	v, present := root["statusLine"]
	if !present || isSatelleOwnedStatusLine(v) {
		return ""
	}
	return fmt.Sprintf(`  note: a statusLine you own is already configured — left unchanged.
        Claude supports only one, so satelle did not install its own. To show
        satelle's segment, add this to your own statusline script:
          %s`, statusLineCommand)
}
