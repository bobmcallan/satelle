// harness_hooks.go — the ONE definition of each harness's satelle hook surface
// (sty_338a53f8). Three paths used to answer "what does a satelle scaffold look
// like for this harness" independently: the BUILDER that writes a fresh file,
// the HEALER that appends missing entries to an existing one, and the CHECKER
// that decides whether the result is complete. They could disagree, so all
// three now read this table.
//
// This file also owns the uninstall side of the hook scaffolds: stripping
// satelle-owned entries from .claude/settings.json and .grok/hooks/satelle.json
// while preserving everything the user wrote.
package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// harnessHookSpec is one harness's satelle hook surface.
//
// gateMatcher / commitMatcher are the PreToolUse matchers: the tool-name
// alternation the scaffold writes, and therefore the only one the healer may
// expect. events is the set of hook events a satelle scaffold for this harness
// actually installs — and therefore the set the completeness check may demand.
type harnessHookSpec struct {
	gateMatcher   string
	commitMatcher string
	events        []string
}

// hasEvent reports whether this harness's scaffold installs the named event.
func (s harnessHookSpec) hasEvent(event string) bool {
	for _, e := range s.events {
		if e == event {
			return true
		}
	}
	return false
}

// fullHookEvents is the event set a harness gets when it supports all of them.
var fullHookEvents = []string{"SessionStart", "PreToolUse", "UserPromptSubmit", "Stop"}

// harnessHooks returns the hook surface for a harness. An unknown harness gets
// the claude shape, which is what every caller already defaulted to.
func harnessHooks(harness string) harnessHookSpec {
	switch harness {
	case "grok":
		return harnessHookSpec{
			gateMatcher:   "Edit|Write|MultiEdit|NotebookEdit|search_replace|write",
			commitMatcher: "Bash|run_terminal_command",
			events:        fullHookEvents,
		}
	default: // claude
		return harnessHookSpec{
			gateMatcher:   "Edit|Write|MultiEdit|NotebookEdit",
			commitMatcher: "Bash",
			events:        fullHookEvents,
		}
	}
}

// removeClaudeHooks strips satelle-owned entries from .claude/settings.json.
// Never deletes the file or the .claude/ directory.
func removeClaudeHooks(repoRoot string) (action, path, note string, err error) {
	path = filepath.Join(repoRoot, ".claude", "settings.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "absent", path, "", nil
		}
		return "", path, "", err
	}
	// Statusline (sty_4e6f0788) is satelle-owned content too, so its presence
	// alone justifies a remove pass even when no hook entries remain. Stripped
	// first, on the raw bytes, so a foreign statusLine survives untouched.
	stripped, droppedLine := stripSatelleStatusLine(raw)
	if !droppedLine &&
		!strings.Contains(string(raw), "satelle-hook.sh") && !strings.Contains(string(raw), "satelle hook ") {
		return "skipped", path, "no satelle-owned hook entries — left in place", nil
	}
	pruned, _, perr := pruneSatelleHookEntries(stripped)
	if perr != nil {
		return "skipped", path, "unparseable settings.json — left in place", nil
	}
	if string(pruned) == string(raw) {
		return "unchanged", path, "", nil
	}
	if err := os.WriteFile(path, pruned, 0o644); err != nil {
		return "", path, "", err
	}
	return "updated", path, "stripped satelle-owned entries; user keys preserved", nil
}

// removeGrokHooks strips satelle-owned entries from .grok/hooks/satelle.json.
// Deletes the file only when nothing non-satelle remains.
func removeGrokHooks(repoRoot string) (action, path, note string, err error) {
	path = filepath.Join(repoRoot, filepath.FromSlash(grokHooksRel))
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "absent", path, "", nil
		}
		return "", path, "", err
	}
	if !strings.Contains(string(raw), "satelle-hook.sh") && !strings.Contains(string(raw), "satelle hook ") {
		return "skipped", path, "not satelle-owned — left in place", nil
	}
	pruned, empty, perr := pruneSatelleHookEntries(raw)
	if perr != nil {
		return "skipped", path, "unparseable — left in place", nil
	}
	if empty {
		if err := os.Remove(path); err != nil {
			return "", path, "", err
		}
		_ = os.Remove(filepath.Dir(path)) // best-effort empty hooks dir
		return "removed", path, "", nil
	}
	if err := os.WriteFile(path, pruned, 0o644); err != nil {
		return "", path, "", err
	}
	return "updated", path, "stripped satelle-owned entries; user hooks preserved", nil
}

// pruneSatelleHookEntries removes hook handlers whose command carries a satelle
// marker (satelle-hook.sh or "satelle hook "). Empty matcher groups are dropped.
// empty is true when no hooks remain under any event and no non-hooks top-level
// keys remain that would justify keeping the file.
func pruneSatelleHookEntries(raw []byte) (pruned []byte, empty bool, err error) {
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, false, err
	}
	hooks, ok := root["hooks"].(map[string]any)
	if !ok {
		return raw, false, nil
	}
	for event, val := range hooks {
		groups, ok := val.([]any)
		if !ok {
			continue
		}
		var keptGroups []any
		for _, g := range groups {
			gm, ok := g.(map[string]any)
			if !ok {
				keptGroups = append(keptGroups, g)
				continue
			}
			hs, ok := gm["hooks"].([]any)
			if !ok {
				keptGroups = append(keptGroups, g)
				continue
			}
			var kept []any
			for _, h := range hs {
				hm, ok := h.(map[string]any)
				if !ok {
					kept = append(kept, h)
					continue
				}
				cmd, _ := hm["command"].(string)
				if isSatelleOwnedHookCommand(cmd) {
					continue
				}
				kept = append(kept, h)
			}
			if len(kept) == 0 {
				continue // drop empty matcher group
			}
			gm["hooks"] = kept
			keptGroups = append(keptGroups, gm)
		}
		if len(keptGroups) == 0 {
			delete(hooks, event)
		} else {
			hooks[event] = keptGroups
		}
	}
	root["hooks"] = hooks
	// empty: no events left and no other top-level content. Any other top-level
	// key is user (or unknown) content and keeps the file.
	if len(hooks) == 0 {
		for k := range root {
			if k == "hooks" {
				continue
			}
			b, err := json.MarshalIndent(root, "", "  ")
			if err != nil {
				return nil, false, err
			}
			return append(b, '\n'), false, nil
		}
		return nil, true, nil
	}
	b, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return nil, false, err
	}
	return append(b, '\n'), false, nil
}

// isSatelleOwnedHookCommand reports whether a hook command is satelle-managed.
func isSatelleOwnedHookCommand(cmd string) bool {
	return strings.Contains(cmd, "satelle-hook.sh") ||
		strings.Contains(cmd, "satelle hook ") ||
		strings.Contains(cmd, "satelle reindex") ||
		cmd == "satelle reindex" ||
		cmd == promptHookCommand ||
		cmd == stopcheckHookCommand
}

// maybeRemoveSharedHookScript deletes .satelle/hooks/satelle-hook.sh only when
// no harness scaffold still references it (sty_9e86f407 AC3).
func maybeRemoveSharedHookScript(repoRoot string) (action, path, note string, err error) {
	path = filepath.Join(repoRoot, filepath.FromSlash(satelleHookScriptRel))
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return "absent", path, "", nil
	} else if err != nil {
		return "", path, "", err
	}
	for _, p := range []string{
		filepath.Join(repoRoot, ".claude", "settings.json"),
		filepath.Join(repoRoot, filepath.FromSlash(grokHooksRel)),
	} {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		if strings.Contains(string(b), "satelle-hook.sh") {
			return "skipped", path, "still referenced by a harness scaffold", nil
		}
	}
	if err := os.Remove(path); err != nil {
		return "", path, "", err
	}
	return "removed", path, "", nil
}
