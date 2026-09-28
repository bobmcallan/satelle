// Antigravity (agy) harness compliance scaffold: .agents/hooks.json
// (sty_9e88b82f). Antigravity loads lifecycle hooks from a single hooks.json in
// its customization root; the file is a map of HOOK NAME → event config (unlike
// Claude/Codex's top-level "hooks" key), PreToolUse is grouped by a tool-name
// matcher while PreInvocation and Stop are FLAT handler lists, and command
// handlers run with the directory holding hooks.json as their cwd. PreToolUse
// deny is a top-level {"decision":"deny","reason":…}; Stop blocks ONLY on
// {"decision":"continue","reason":…}. Source: agy's own hooks.md
// (~/.gemini/antigravity-cli/builtin/skills/agy-customizations/docs/hooks.md).
package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// antigravityHarness is the harness token the scaffolded hook commands pass
// (`gate antigravity`, `--harness antigravity`). Detection of an unflagged
// Antigravity payload is deliberately not here: it lives with the agentcli
// adapter.
const antigravityHarness = "antigravity"

// antigravityHooksRel is the repo-relative path of the satelle-owned Antigravity
// hooks file. .agents/ is a shared customization root, so only this one file is
// ever written or removed.
const antigravityHooksRel = ".agents/hooks.json"

// antigravityHookName is the named-hook key the scaffold writes its entries
// under. Removal strips satelle-owned commands from EVERY named hook, so this
// name is where satelle writes, not what it owns.
const antigravityHookName = "satelle"

// antigravityEntry is one satelle hook handler the scaffold installs: where it
// goes (event, and matcher for the grouped PreToolUse), the substring that
// identifies it once installed, and the handler itself.
type antigravityEntry struct {
	event   string
	matcher string // PreToolUse only; "" for the flat events
	marker  string
	handler map[string]any
}

// antigravityEntries is the ONE list the builder writes from and the healer and
// completeness check read, so the three cannot drift (the sty_338a53f8 rule).
// Matchers and events come from harnessHooks("antigravity"). Handlers carry no
// async flag: agy documents that hooks run synchronously.
func antigravityEntries(repoRoot string) []antigravityEntry {
	hs := harnessHooks(antigravityHarness)
	command := func(cmd string) map[string]any { return map[string]any{"type": "command", "command": cmd} }
	return []antigravityEntry{
		{event: "PreToolUse", matcher: hs.gateMatcher, marker: "satelle-hook.sh gate ",
			handler: command(renderHookCommand(repoRoot, antigravityHarness, "gate"))},
		{event: "PreToolUse", matcher: hs.commitMatcher, marker: "satelle-hook.sh commitgate ",
			handler: command(renderHookCommand(repoRoot, antigravityHarness, "commitgate"))},
		{event: "PreInvocation", marker: "satelle hook context",
			handler: command("satelle hook context --harness " + antigravityHarness)},
		// The stopcheck timeout outlasts the wait it makes for a running gate, or
		// agy's 30s default would kill the hook before it can answer (sty_c4b92c9e).
		{event: "Stop", marker: "satelle hook stopcheck",
			handler: stopHookEntry("satelle hook stopcheck --harness " + antigravityHarness)},
	}
}

// buildAntigravityHookSettings returns the .agents/hooks.json scaffold bytes.
// repoRoot makes the PreToolUse script paths absolute: agy runs a handler with
// hooks.json's directory (.agents/) as its cwd, so the relative
// .satelle/hooks/satelle-hook.sh would not resolve.
func buildAntigravityHookSettings(repoRoot string) []byte {
	spec := map[string]any{}
	for _, e := range antigravityEntries(repoRoot) {
		spec[e.event] = appendAntigravityEntry(spec[e.event], e)
	}
	b, _ := json.MarshalIndent(map[string]any{antigravityHookName: spec}, "", "  ")
	return append(b, '\n')
}

// appendAntigravityEntry adds e to an event's list in agy's shape: a matcher
// group for the grouped PreToolUse, the bare handler for the flat events.
func appendAntigravityEntry(list any, e antigravityEntry) []any {
	arr, _ := list.([]any)
	if e.matcher == "" {
		return append(arr, e.handler)
	}
	return append(arr, map[string]any{"matcher": e.matcher, "hooks": []any{e.handler}})
}

// antigravityHandlers returns the handler objects of one event — flattening
// matcher groups — as the live maps, so a caller may edit them in place. Any
// node of an unexpected shape is skipped, as in hookEventHasMarker.
func antigravityHandlers(event any) []map[string]any {
	var out []map[string]any
	list, _ := event.([]any)
	for _, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if inner, grouped := m["hooks"].([]any); grouped {
			for _, h := range inner {
				if hm, ok := h.(map[string]any); ok {
					out = append(out, hm)
				}
			}
			continue
		}
		out = append(out, m)
	}
	return out
}

// antigravityEventHas reports whether any named hook in root carries a handler
// for event whose command contains marker.
func antigravityEventHas(root map[string]any, event, marker string) bool {
	for _, v := range root {
		spec, ok := v.(map[string]any)
		if !ok {
			continue
		}
		for _, h := range antigravityHandlers(spec[event]) {
			if cmd, _ := h["command"].(string); strings.Contains(cmd, marker) {
				return true
			}
		}
	}
	return false
}

// incompleteAntigravityEvents names the events still missing a satelle handler.
func incompleteAntigravityEvents(root map[string]any, repoRoot string) []string {
	seen := map[string]bool{}
	var missing []string
	for _, e := range antigravityEntries(repoRoot) {
		if !antigravityEventHas(root, e.event, e.marker) && !seen[e.event] {
			seen[e.event] = true
			missing = append(missing, e.event)
		}
	}
	return missing
}

// ensureAntigravityHooks writes .agents/hooks.json when absent, and heals an
// existing file: a missing satelle handler is appended, a stale PreToolUse
// script path is rewritten to the absolute form, a too-short stopcheck timeout
// is raised. User hooks are preserved and an already-complete file is not
// rewritten (idempotent).
func ensureAntigravityHooks(repoRoot string) (created bool, updated []string, incomplete []string, err error) {
	if err := writeHookScripts(repoRoot); err != nil {
		return false, nil, nil, err
	}
	dir := filepath.Join(repoRoot, filepath.Dir(filepath.FromSlash(antigravityHooksRel)))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, nil, nil, fmt.Errorf("init: mkdir %s: %w", dir, err)
	}
	path := filepath.Join(repoRoot, filepath.FromSlash(antigravityHooksRel))
	raw, rerr := os.ReadFile(path)
	if os.IsNotExist(rerr) {
		if err := os.WriteFile(path, buildAntigravityHookSettings(repoRoot), 0o644); err != nil {
			return false, nil, nil, fmt.Errorf("init: write %s: %w", path, err)
		}
		return true, nil, nil, nil
	} else if rerr != nil {
		return false, nil, nil, fmt.Errorf("init: read %s: %w", path, rerr)
	}
	var root map[string]any
	if json.Unmarshal(raw, &root) != nil || root == nil {
		return false, nil, []string{"(unparseable)"}, nil // not JSON we can safely mutate
	}
	updated = healAntigravityRoot(root, repoRoot)
	if len(updated) > 0 {
		b, merr := json.MarshalIndent(root, "", "  ")
		if merr != nil {
			return false, nil, nil, merr
		}
		if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
			return false, nil, nil, fmt.Errorf("init: write %s: %w", path, err)
		}
	}
	return false, updated, incompleteAntigravityEvents(root, repoRoot), nil
}

// healAntigravityRoot mutates root to the scaffold's complete form and returns
// what it changed. Missing handlers go under the satelle named hook; a satelle
// hook name held by a non-object is left alone (and reported incomplete).
func healAntigravityRoot(root map[string]any, repoRoot string) []string {
	var changed []string
	for _, e := range antigravityEntries(repoRoot) {
		if antigravityEventHas(root, e.event, e.marker) {
			continue
		}
		var spec map[string]any
		switch cur := root[antigravityHookName].(type) {
		case nil:
			spec = map[string]any{}
			root[antigravityHookName] = spec
		case map[string]any:
			spec = cur
		default:
			continue
		}
		spec[e.event] = appendAntigravityEntry(spec[e.event], e)
		changed = append(changed, "added "+e.event+" hook")
	}
	for _, v := range root {
		spec, ok := v.(map[string]any)
		if !ok {
			continue
		}
		for _, h := range antigravityHandlers(spec["PreToolUse"]) {
			cmd, _ := h["command"].(string)
			sub := legacyHookSub(cmd)
			if sub == "" {
				continue
			}
			if want := renderHookCommand(repoRoot, antigravityHarness, sub); strings.TrimSpace(cmd) != want {
				h["command"] = want
				changed = append(changed, "upgraded PreToolUse "+sub+" hook to the absolute script path")
			}
		}
		for _, h := range antigravityHandlers(spec["Stop"]) {
			if cmd, _ := h["command"].(string); !strings.Contains(cmd, "satelle hook stopcheck") {
				continue
			}
			if jsonNumber(h["timeout"]) < stopHookTimeoutSec {
				h["timeout"] = stopHookTimeoutSec
				changed = append(changed, "raised Stop timeout")
			}
		}
	}
	sort.Strings(changed)
	return changed
}

// jsonNumber reads a number out of a decoded-or-freshly-built JSON tree: a value
// read from the file is a float64, one the heal just added is a Go int.
func jsonNumber(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	}
	return 0
}

// removeAntigravityHooks strips satelle-owned handlers from .agents/hooks.json.
// The file is deleted only when nothing else remains, and .agents/ only when
// that leaves it empty. User hooks are never touched. Action vocabulary:
// removed | absent | skipped | updated.
func removeAntigravityHooks(repoRoot string) (action, path, note string, err error) {
	path = filepath.Join(repoRoot, filepath.FromSlash(antigravityHooksRel))
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
	pruned, empty, perr := pruneAntigravityHooks(raw)
	if perr != nil {
		return "skipped", path, "unparseable hooks.json with satelle markers — left in place", nil
	}
	if empty {
		if err := os.Remove(path); err != nil {
			return "", path, "", err
		}
		_ = os.Remove(filepath.Dir(path)) // best-effort: only succeeds when .agents/ is empty
		return "removed", path, "", nil
	}
	if err := os.WriteFile(path, pruned, 0o644); err != nil {
		return "", path, "", err
	}
	return "updated", path, "stripped satelle-owned entries; user hooks preserved", nil
}

// pruneAntigravityHooks removes satelle-owned handlers from every named hook,
// through pruneSatelleHookEntries: each named hook's event lists are handed to
// it as a {"hooks": …} document, which is the shape it prunes. A named hook that
// held satelle handlers and has no event left is dropped; a named hook satelle
// did not touch is left exactly as it was. empty is true when no named hook
// remains, i.e. the file was wholly satelle's.
func pruneAntigravityHooks(raw []byte) (pruned []byte, empty bool, err error) {
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, false, err
	}
	for name, v := range root {
		spec, ok := v.(map[string]any)
		if !ok {
			continue
		}
		events := map[string]any{}
		for k, ev := range spec {
			if _, isList := ev.([]any); isList {
				events[k] = ev
			}
		}
		if len(events) == 0 {
			continue
		}
		before, _ := json.Marshal(events)
		in, _ := json.Marshal(map[string]any{"hooks": events})
		out, allGone, perr := pruneSatelleHookEntries(in)
		if perr != nil {
			return nil, false, perr
		}
		kept := map[string]any{}
		if !allGone {
			var back struct {
				Hooks map[string]any `json:"hooks"`
			}
			if err := json.Unmarshal(out, &back); err != nil {
				return nil, false, err
			}
			kept = back.Hooks
		}
		if after, _ := json.Marshal(kept); string(after) == string(before) {
			continue // no satelle handler here — leave the user's hook untouched
		}
		for k := range events {
			if ev, still := kept[k]; still {
				spec[k] = ev
			} else {
				delete(spec, k)
			}
		}
		if len(kept) == 0 && antigravityOnlyEnabledKey(spec) {
			delete(root, name)
		}
	}
	if len(root) == 0 {
		return nil, true, nil
	}
	b, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return nil, false, err
	}
	return append(b, '\n'), false, nil
}

// antigravityOnlyEnabledKey reports whether a named hook has nothing left but
// its optional "enabled" switch — no user content to keep.
func antigravityOnlyEnabledKey(spec map[string]any) bool {
	for k := range spec {
		if k != "enabled" {
			return false
		}
	}
	return true
}
