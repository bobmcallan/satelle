// Codex harness compliance scaffold: .codex/hooks.json (sty_9e86f407).
// Codex CLI 0.145+ loads project hooks from <repo>/.codex/hooks.json with a
// Claude-shaped event map under a top-level "hooks" key. PreToolUse deny uses
// the Claude envelope (permissionDecision=deny + non-empty reason).
package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// codexHooksRel is the repo-relative path of the satelle-owned Codex hooks file.
const codexHooksRel = ".codex/hooks.json"

// satelleCodexDesc is the description written by a wholly satelle-owned scaffold.
// On remove, this alone does not keep the file (AC3: pure satelle scaffold deleted).
const satelleCodexDesc = "satelle-owned compliance hooks — preserve non-satelle entries on remove"

// buildCodexHookSettings returns .codex/hooks.json scaffold bytes.
// Matchers cover Codex's documented mutating tool names: apply_patch (also
// Edit|Write aliases) and Bash for shell/commit commands. SessionStart and
// UserPromptSubmit mirror Claude/Grok.
// repoRoot makes PreToolUse script paths absolute (cwd-safe).
func buildCodexHookSettings(repoRoot string) []byte {
	commandHook := func(command string) map[string]any {
		// Codex's configured command-handler schema requires an explicit async
		// value. Compliance hooks must synchronously decide before a mutation,
		// so they are always false.
		return map[string]any{"type": "command", "command": command, "async": false}
	}
	doc := map[string]any{
		"description": satelleCodexDesc,
		"hooks": map[string]any{
			"SessionStart": []any{
				map[string]any{"hooks": []any{
					commandHook("satelle reindex"),
					commandHook("satelle hook context"),
				}},
			},
			"PreToolUse": []any{
				map[string]any{
					// Codex documents Bash and apply_patch; Edit|Write are supported
					// apply_patch aliases. Do not carry non-Codex matcher guesses here.
					"matcher": "apply_patch|Edit|Write|Bash",
					"hooks":   []any{commandHook(renderHookCommand(repoRoot, "codex", "gate"))},
				},
				map[string]any{
					"matcher": "Bash",
					"hooks":   []any{commandHook(renderHookCommand(repoRoot, "codex", "commitgate"))},
				},
			},
			"UserPromptSubmit": []any{
				map[string]any{"hooks": []any{
					commandHook(promptHookCommand),
				}},
			},
			// Codex documents Stop and SessionEnd; we deliberately omit Stop here
			// so the scaffold only uses events proven in the sty_9e86f407 plan probe
			// (SessionStart, PreToolUse, UserPromptSubmit). stopcheck remains on
			// Claude/Grok scaffolds.
		},
	}
	b, _ := json.MarshalIndent(doc, "", "  ")
	return append(b, '\n')
}

// ensureCodexHooks writes .codex/hooks.json when absent, and reconciles/heals an
// existing satelle-owned file. User keys and non-satelle hook entries are preserved.
func ensureCodexHooks(repoRoot string) (created bool, updated []string, incomplete []string, err error) {
	if err := writeHookScripts(repoRoot); err != nil {
		return false, nil, nil, err
	}
	dir := filepath.Join(repoRoot, ".codex")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, nil, nil, fmt.Errorf("init: mkdir %s: %w", dir, err)
	}
	path := filepath.Join(repoRoot, filepath.FromSlash(codexHooksRel))
	if _, err := os.Stat(path); err == nil {
		updated, incomplete, herr := healExistingHookFile(path, "codex", repoRoot)
		if herr != nil {
			return false, nil, nil, herr
		}
		return false, updated, incomplete, nil
	} else if !os.IsNotExist(err) {
		return false, nil, nil, fmt.Errorf("init: stat %s: %w", path, err)
	}
	if err := os.WriteFile(path, buildCodexHookSettings(repoRoot), 0o644); err != nil {
		return false, nil, nil, fmt.Errorf("init: write %s: %w", path, err)
	}
	return true, nil, nil, nil
}

// removeCodexHooks strips satelle-owned hook entries from .codex/hooks.json.
// Deletes the file only when no non-satelle content remains. Never removes
// user-owned entries. Action vocabulary: removed | absent | skipped | updated.
func removeCodexHooks(repoRoot string) (action, path, note string, err error) {
	path = filepath.Join(repoRoot, filepath.FromSlash(codexHooksRel))
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "absent", path, "", nil
		}
		return "", path, "", err
	}
	if !strings.Contains(string(raw), "satelle-hook.sh") && !strings.Contains(string(raw), "satelle hook ") {
		return "skipped", path, "no satelle-owned hook entries — left in place", nil
	}
	pruned, empty, perr := pruneSatelleHookEntries(raw)
	if perr != nil {
		// Unparseable: only remove if wholly satelle-shaped (marker + no foreign signal).
		return "skipped", path, "unparseable hooks.json with satelle markers — left in place", nil
	}
	if empty {
		if err := os.Remove(path); err != nil {
			return "", path, "", err
		}
		return "removed", path, "", nil
	}
	if err := os.WriteFile(path, pruned, 0o644); err != nil {
		return "", path, "", err
	}
	return "updated", path, "stripped satelle-owned entries; user hooks preserved", nil
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
	if !strings.Contains(string(raw), "satelle-hook.sh") && !strings.Contains(string(raw), "satelle hook ") {
		return "skipped", path, "no satelle-owned hook entries — left in place", nil
	}
	pruned, _, perr := pruneSatelleHookEntries(raw)
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
// Deletes the file only when nothing non-satelle remains (same ownership rule as codex).
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
// keys other than description remain that would justify keeping the file.
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
	// empty: no events left and no *user* top-level content. A satelle-owned
	// description alone does not retain the file (wholly satelle scaffold → delete).
	// A different description or any other top-level key keeps the file.
	if len(hooks) == 0 {
		for k, v := range root {
			if k == "hooks" {
				continue
			}
			if k == "description" {
				if s, ok := v.(string); ok && s == satelleCodexDesc {
					continue // satelle scaffold description — not user content
				}
			}
			// User (or unknown) top-level content — keep the file.
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
		filepath.Join(repoRoot, filepath.FromSlash(codexHooksRel)),
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
