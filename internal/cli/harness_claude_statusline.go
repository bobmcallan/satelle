package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Claude statusLine ownership (sty_4e6f0788, narrowed by sty_325df80c).
//
// satelle INSTALLS NO STATUSLINE. A repo's `.claude/settings.json` is shared
// scaffold — every session in the repo inherits it, and in most repos it is
// committed — while a statusline is an OPERATOR preference: which line the
// person at the terminal wants to look at. Seeding it per-repo imposed one
// operator's choice on everyone who opened the repo, silently. The renderer
// (`satelle status --line`) stays; only its automatic installation is gone.
//
// The opt-in home is the operator's OWN `~/.claude/settings.json`, and satelle
// names it rather than writing it. Writing to the user's global file to fix
// "we wrote to a file we should not have" would be the same mistake at a wider
// scope, so there is deliberately no install verb for it either.
//
// What remains here is REMOVAL and RECOGNITION: init heals repos seeded before
// this change by stripping the entry satelle put there, and ownership is decided
// by containment of statusLineCommand so a foreign statusLine — including one
// that wraps satelle's — is never touched.
//
// Claude ONLY, by capability rather than preference: Grok has no scriptable
// statusline at all, and Codex's built-in `[tui].status_line` accepts a fixed
// item list (model, cwd, git branch, context usage) with no command backing, so
// nothing external can inject into either. Those two harnesses are served by the
// SessionStart availability line (sty_fb5e6d96), which reaches all three. Do not
// "fix" their absence here — it is not an oversight.

// claudeUserSettingsRel is where an operator who WANTS satelle's line puts it:
// their own settings, not the repo's. Named in every surface that mentions the
// opt-in so the answer to "where does it go?" has one spelling.
const claudeUserSettingsRel = "~/.claude/settings.json"

// isSatelleOwnedStatusLine reports whether an existing statusLine value is ours.
// Ownership is containment of the canonical invocation, not equality — so an
// entry an operator DELIBERATELY re-homed into their repo settings, wrapper and
// all, is recognised as satelle's and healed away rather than left behind.
func isSatelleOwnedStatusLine(v any) bool {
	m, ok := v.(map[string]any)
	if !ok {
		return false
	}
	cmd, _ := m["command"].(string)
	return strings.Contains(cmd, statusLineCommand)
}

// unsetClaudeStatusLine heals an EXISTING settings.json by removing the
// statusLine satelle seeded into it before sty_325df80c. It returns a
// human-readable note when it changed something.
//
// Three outcomes, and only one of them writes:
//   - satelle's   → remove it, and say where the operator can re-home it
//   - foreign     → leave untouched, no write, no note (never clobber)
//   - absent      → nothing to do, so a second init is a clean no-op
//
// The ownership decision lives entirely in stripSatelleStatusLine, which reads
// raw bytes; the foreign and absent cases both fall out of it reporting no
// change, so there is no second ownership rule to keep in step with the first.
func unsetClaudeStatusLine(path string) (note string, err error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	out, changed := stripSatelleStatusLine(raw)
	if !changed {
		return "", nil
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return "", err
	}
	return "removed statusLine (an operator preference — re-home it in your own " +
		claudeUserSettingsRel + ")", nil
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

// statusLineOptInNotice states where an operator who wants satelle's line puts
// it. Unconditional and read-only: satelle no longer inspects the repo's
// settings to decide whether to say this, because it no longer proposes to write
// there at all. The snippet is composed from statusLineCommand so the documented
// invocation cannot drift from the verb that serves it.
func statusLineOptInNotice() string {
	return fmt.Sprintf(`  note: satelle installs no statusLine — it is an operator preference, and a
        repo's .claude/settings.json is shared scaffold. To show satelle's line
        (satelled link plus the engaged <story_id>::<stage>), add this to your own
        %s:
          "statusLine": { "type": "command", "command": %q }`,
		claudeUserSettingsRel, statusLineCommand)
}
