// harness_cursor.go — the cursor-agent harness scaffold (sty_7d098d50). cursor
// reads project hooks from .cursor/hooks.json: a flat, matcher-less list per
// event, so where the claude and grok scaffolds filter a gate by tool name, cursor
// sends every tool call to both gates and the verbs classify it themselves
// (harnessHooks(cursor).NoToolMatcher). The file's shape and its ownership rule
// live in agentinstall; this file supplies the commands that go in it.
package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/agentinstall"
)

// cursorHooksRel is the repo-relative path of cursor's project hooks file.
const cursorHooksRel = agentinstall.CursorHooksRel

// cursorHookEvents are cursor's own names for the events the scaffold uses.
const (
	cursorEventPreToolUse   = "preToolUse"
	cursorEventSessionStart = "sessionStart"
	cursorEventStop         = "stop"
)

// withCursorHarness names the harness on a direct satelle hook command.
func withCursorHarness(cmd string) string { return cmd + " --harness " + agentcli.HarnessCursor }

// cursorWantedHooks is the satelle-owned entry set a cursor scaffold carries for
// repoRoot, in the order a fresh file lists them. PreToolUse goes through the
// fail-visible wrapper with an ABSOLUTE path (cursor runs hooks with a cwd other
// than the project root, so a relative script never ran — probe 2e); the context
// and stop entries are the PATH-prefixed direct form probe 13 showed cursor runs.
// The stop entry carries stopHookTimeoutSec like the other harnesses': it waits
// on a gate, and cursor's default timeout dropped a 75s stop hook (20-slow-default)
// where a 1800s one landed (20-slow-1800).
func cursorWantedHooks(repoRoot string) []agentinstall.CursorHook {
	return []agentinstall.CursorHook{
		{Event: cursorEventPreToolUse, Command: renderHookCommand(repoRoot, agentcli.HarnessCursor, "gate"), Role: satelleHookScriptRel + " gate "},
		{Event: cursorEventPreToolUse, Command: renderHookCommand(repoRoot, agentcli.HarnessCursor, "commitgate"), Role: satelleHookScriptRel + " commitgate "},
		{Event: cursorEventSessionStart, Command: withCursorHarness(contextHookCommandPathPrefixed), Role: "satelle hook context"},
		{Event: cursorEventStop, Command: withCursorHarness(stopcheckHookCommand), Role: "satelle hook stopcheck", TimeoutS: stopHookTimeoutSec},
	}
}

// ensureCursorHooks writes .cursor/hooks.json, or merges satelle's entries into
// the one that is there, keeping the user's version, unknown keys and entries.
// created is true when the file did not exist; updated names what a merge
// changed; incomplete explains why the file could not be made whole (it is left
// untouched then).
func ensureCursorHooks(repoRoot string) (created bool, updated, incomplete []string, err error) {
	if err := writeHookScripts(repoRoot); err != nil {
		return false, nil, nil, err
	}
	path := filepath.Join(repoRoot, filepath.FromSlash(cursorHooksRel))
	if st, lerr := os.Lstat(path); lerr == nil && st.Mode()&os.ModeSymlink != 0 {
		// A linked worktree carries the main tree's file (satelle.toml [worktree]
		// include). Writing through the link would rewrite the main tree's wrapper
		// paths with this tree's, so the link is left alone.
		return false, []string{"skipped (linked to another tree's file)"}, nil, nil
	}
	prev, rerr := os.ReadFile(path)
	if rerr != nil && !os.IsNotExist(rerr) {
		return false, nil, nil, rerr
	}
	want := cursorWantedHooks(repoRoot)
	next, merr := agentinstall.RenderCursorHooks(prev, want)
	if merr != nil {
		return false, nil, []string{"unparseable " + cursorHooksRel + " left in place: " + merr.Error()}, nil
	}
	if rerr == nil && string(next) == string(prev) {
		return false, nil, nil, nil
	}
	if rerr == nil {
		updated = agentinstall.CursorHooksMissing(prev, want)
		for _, ev := range agentinstall.CursorHooksShortTimeout(prev, want) {
			updated = append(updated, ev+" timeout raised")
		}
		if len(updated) == 0 {
			updated = []string{"non-canonical entries rewritten"}
		}
	} else {
		created = true
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, nil, nil, err
	}
	if err := os.WriteFile(path, next, 0o644); err != nil {
		return false, nil, nil, err
	}
	return created, updated, nil, nil
}

// healCursorHooks re-renders an ALREADY installed cursor scaffold during
// `satelle init`. An absent file, a file with no satelle entry, and a linked
// (symlinked) file are all left alone: init never creates cursor wiring in a
// repo that did not ask for it.
func healCursorHooks(out io.Writer, repoRoot string) error {
	path := filepath.Join(repoRoot, filepath.FromSlash(cursorHooksRel))
	st, err := os.Lstat(path)
	if err != nil || st.Mode()&os.ModeSymlink != 0 {
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil || len(agentinstall.CursorHookCommands(raw, cursorEventPreToolUse))+
		len(agentinstall.CursorHookCommands(raw, cursorEventSessionStart))+
		len(agentinstall.CursorHookCommands(raw, cursorEventStop)) == 0 {
		return nil
	}
	created, updated, incomplete, err := ensureCursorHooks(repoRoot)
	if err != nil {
		return err
	}
	printScaffoldOutcome(out, agentcli.HarnessCursor, cursorHooksRel, created, updated, incomplete)
	return nil
}

// removeCursorHooks strips satelle-owned entries from .cursor/hooks.json and
// deletes the file only when nothing but `version` remains (and .cursor/ only
// when that leaves it empty). A symlinked file is another tree's: left in place.
func removeCursorHooks(repoRoot string) (action, path, note string, err error) {
	path = filepath.Join(repoRoot, filepath.FromSlash(cursorHooksRel))
	if st, lerr := os.Lstat(path); lerr == nil && st.Mode()&os.ModeSymlink != 0 {
		return "skipped", path, "linked to another tree's file — left in place", nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "absent", path, "", nil
		}
		return "", path, "", err
	}
	next, empty, changed, perr := agentinstall.RemoveCursorHooks(raw)
	if perr != nil {
		return "skipped", path, "unparseable — left in place", nil
	}
	if !changed {
		return "skipped", path, "no satelle-owned hook entries — left in place", nil
	}
	if empty {
		if err := os.Remove(path); err != nil {
			return "", path, "", err
		}
		_ = os.Remove(filepath.Dir(path)) // best-effort: refuses a non-empty .cursor/
		return "removed", path, "", nil
	}
	if err := os.WriteFile(path, next, 0o644); err != nil {
		return "", path, "", err
	}
	return "updated", path, "stripped satelle-owned entries; user keys and hooks preserved", nil
}

// driftCursorHooks reports a deployed .cursor/hooks.json whose satelle entries are
// not what this binary would write for repoRoot (a stale wrapper path, a missing
// entry, a wrapper script gone). An absent file is not drift — cursor is not
// deployed — and neither is a file with no satelle entry at all: that is the
// operator's own.
func driftCursorHooks(repoRoot string) []ScaffoldFinding {
	raw, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(cursorHooksRel)))
	if err != nil {
		return nil
	}
	want := cursorWantedHooks(repoRoot)
	owned := 0
	for _, w := range want {
		owned += len(agentinstall.CursorHookCommands(raw, w.Event))
	}
	if owned == 0 {
		return nil
	}
	var findings []ScaffoldFinding
	for _, w := range want {
		have := agentinstall.CursorHookCommands(raw, w.Event)
		var slot []string
		for _, c := range have {
			if strings.Contains(c, w.Role) {
				slot = append(slot, c)
			}
		}
		switch {
		case len(slot) == 0:
			findings = append(findings, ScaffoldFinding{
				Path: cursorHooksRel, Kind: "missing",
				Detail: fmt.Sprintf("%s entry for %q is missing (want %q)", w.Event, strings.TrimSpace(w.Role), w.Command),
			})
		case slot[0] != w.Command:
			findings = append(findings, ScaffoldFinding{
				Path: cursorHooksRel, Kind: "command",
				Detail: fmt.Sprintf("%s command is not the canonical form (want %q)", w.Event, w.Command),
			})
		}
	}
	for _, ev := range agentinstall.CursorHooksShortTimeout(raw, want) {
		findings = append(findings, ScaffoldFinding{
			Path: cursorHooksRel, Kind: "timeout",
			Detail: fmt.Sprintf("%s entry has no timeout or one under %ds — cursor drops a stop hook that outlasts it (run satelle init)", ev, stopHookTimeoutSec),
		})
	}
	if _, err := os.Stat(filepath.Join(repoRoot, filepath.FromSlash(satelleHookScriptRel))); err != nil {
		findings = append(findings, ScaffoldFinding{
			Path: satelleHookScriptRel, Kind: "missing",
			Detail: "canonical wrapper script missing — run satelle init",
		})
	}
	return findings
}
