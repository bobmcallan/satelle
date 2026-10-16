// Package agentinstall provisions satelle-owned launcher scripts under
// $SATELLE_HOME/agents/bin/ (sty_aa726901). It does not install third-party
// software, does not touch global [agent] cli, and does not edit repo
// agents.toml — so install can be verified without changing the default
// reviewer.
package agentinstall

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bobmcallan/satelle/internal/agentcli"
)

const (
	// MarkerPrefix identifies a launcher as satelle-owned (remove only deletes marked files).
	MarkerPrefix = "# satelle-owned: satelle agents install"
	// MarkerLine is the substring matched on remove (name-specific lines still match).
	MarkerLine = MarkerPrefix
	// RelBin is the path under SATELLE_HOME for launchers.
	RelBin = "agents/bin"
)

// Agents returns the installable names (stable order): the harnesses that have
// a launcher, as agentcli owns that set.
func Agents() []string { return agentcli.LauncherHarnesses() }

// Result is one install/remove outcome.
type Result struct {
	Name   string // claude | grok
	Path   string
	Action string // created | updated | unchanged | removed | absent | skipped
	Note   string
}

// Install writes satelle-owned launchers under home/agents/bin for name
// (claude|grok|all). home is injected so tests need not set
// SATELLE_HOME.
func Install(home, name string) ([]Result, error) {
	names, err := expandName(name)
	if err != nil {
		return nil, err
	}
	binDir := filepath.Join(home, RelBin)
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return nil, fmt.Errorf("agentinstall: mkdir %s: %w", binDir, err)
	}
	var out []Result
	for _, n := range names {
		r, err := installOne(binDir, n)
		if err != nil {
			return out, err
		}
		out = append(out, r)
	}
	return out, nil
}

// Remove deletes satelle-owned launchers for name. Unmarked files are left with
// Action skipped. Absent is exit-ok (Action absent).
func Remove(home, name string) ([]Result, error) {
	names, err := expandName(name)
	if err != nil {
		return nil, err
	}
	binDir := filepath.Join(home, RelBin)
	var out []Result
	for _, n := range names {
		r, err := removeOne(binDir, n)
		if err != nil {
			return out, err
		}
		out = append(out, r)
	}
	return out, nil
}

// LauncherPath returns the absolute path of the launcher for name under home.
func LauncherPath(home, name string) string {
	return filepath.Join(home, RelBin, "satelle-"+name)
}

// Content returns the launcher script body for name.
func Content(name string) (string, error) {
	if !agentcli.HasLauncher(name) {
		return "", fmt.Errorf("agentinstall: unknown agent %q (want %s, or all)", name, strings.Join(Agents(), ", "))
	}
	return launcherScript(name), nil
}

// NoLauncherResult is the install outcome for a harness with no launcher (pi,
// cursor): nothing is written, and the skip is named rather than silently absent.
func NoLauncherResult(name string) Result {
	return Result{
		Name:   name,
		Action: "skipped",
		Note:   fmt.Sprintf("no satelle launcher for %s", name),
	}
}

// PrereqNote reports PATH/auth hints for name (never a hard failure).
func PrereqNote(name string) string {
	var parts []string
	switch name {
	case "claude":
		if agentcli.Available(agentcli.CLIClaude) {
			parts = append(parts, "claude on PATH")
		} else {
			parts = append(parts, "claude not on PATH")
		}
	case "grok":
		if agentcli.Available(agentcli.CLIGrok) {
			parts = append(parts, "grok on PATH")
		} else {
			parts = append(parts, "grok not on PATH")
		}
	}
	return strings.Join(parts, "; ")
}

// BindingSnippet returns a pasteable agents.toml fragment for the launcher path.
func BindingSnippet(name, launcherPath string) string {
	switch name {
	case "claude":
		return fmt.Sprintf(`# Use launcher or DefaultClaudeCommand template; select with satelle agent set claude
# command = %q  # optional wrapper; full template still required for {system}
`, launcherPath)
	case "grok":
		return fmt.Sprintf(`# Use launcher or DefaultGrokCommand; select with satelle agent set (or ACP spawn).
# command = %q
`, launcherPath)
	default:
		return ""
	}
}

func expandName(name string) ([]string, error) {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "all" {
		return Agents(), nil
	}
	if agentcli.HasLauncher(n) {
		return []string{n}, nil
	}
	return nil, fmt.Errorf("agentinstall: unknown agent %q (want %s, or all)",
		name, strings.Join(Agents(), ", "))
}

// launcherScript is a marker-owned wrapper that execs the bare harness: a stable
// satelle-owned path a binding's `command` can name. It never calls back into
// satelle. The exec target comes from agentcli's launcher set so launchers
// cannot drift from it.
func launcherScript(name string) string {
	return fmt.Sprintf("#!/bin/sh\n%s %s — do not edit\nexec %s \"$@\"\n",
		MarkerPrefix, name, agentcli.LauncherBinary(name))
}

func installOne(binDir, name string) (Result, error) {
	path := filepath.Join(binDir, "satelle-"+name)
	body, err := Content(name)
	if err != nil {
		return Result{}, err
	}
	want := []byte(body)
	r := Result{Name: name, Path: path, Note: PrereqNote(name)}
	existing, err := os.ReadFile(path)
	if err == nil {
		// Never overwrite a non-satelle-owned file (sty_aa726901 AC5).
		if !strings.Contains(string(existing), MarkerLine) {
			r.Action = "skipped"
			r.Note = "not satelle-owned (missing marker) — left in place; " + r.Note
			return r, nil
		}
		if string(existing) == string(want) {
			r.Action = "unchanged"
			return r, nil
		}
		r.Action = "updated"
	} else if os.IsNotExist(err) {
		r.Action = "created"
	} else {
		return Result{}, fmt.Errorf("agentinstall: read %s: %w", path, err)
	}
	if err := os.WriteFile(path, want, 0o755); err != nil {
		return Result{}, fmt.Errorf("agentinstall: write %s: %w", path, err)
	}
	// Ensure executable even if umask stripped bits.
	if err := os.Chmod(path, 0o755); err != nil {
		return Result{}, fmt.Errorf("agentinstall: chmod %s: %w", path, err)
	}
	return r, nil
}

func removeOne(binDir, name string) (Result, error) {
	path := filepath.Join(binDir, "satelle-"+name)
	r := Result{Name: name, Path: path}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			r.Action = "absent"
			return r, nil
		}
		return Result{}, fmt.Errorf("agentinstall: read %s: %w", path, err)
	}
	if !strings.Contains(string(b), MarkerLine) {
		r.Action = "skipped"
		r.Note = "not satelle-owned (missing marker) — left in place"
		return r, nil
	}
	if err := os.Remove(path); err != nil {
		return Result{}, fmt.Errorf("agentinstall: remove %s: %w", path, err)
	}
	r.Action = "removed"
	return r, nil
}

// SortedNames is a helper for stable test assertions.
func SortedNames(rs []Result) []string {
	names := make([]string, len(rs))
	for i, r := range rs {
		names[i] = r.Name
	}
	sort.Strings(names)
	return names
}
