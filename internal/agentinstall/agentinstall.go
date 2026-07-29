// Package agentinstall provisions satelle-owned launcher scripts under
// $SATELLE_HOME/agents/bin/ (sty_aa726901). It does not install third-party
// software, does not touch global [agent] cli, and does not edit repo
// agents.toml — so install can be verified without changing the default
// reviewer.
package agentinstall

import (
	"fmt"
	"os"
	"os/exec"
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

// known agents installable by name.
var known = []string{"claude", "grok", "codex"}

// Agents returns the installable names (stable order).
func Agents() []string {
	out := make([]string, len(known))
	copy(out, known)
	return out
}

// Result is one install/remove outcome.
type Result struct {
	Name   string // claude | grok | codex
	Path   string
	Action string // created | updated | unchanged | removed | absent | skipped
	Note   string
}

// Install writes satelle-owned launchers under home/agents/bin for name
// (claude|grok|codex|all). home is injected so tests need not set SATELLE_HOME.
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
	switch name {
	case "claude":
		return launcherScript("claude", "claude"), nil
	case "grok":
		return launcherScript("grok", "grok"), nil
	case "codex":
		// Preferred ACP adapter spawn (DefaultCodexACPCommand).
		return launcherScript("codex", "npx -y @agentclientprotocol/codex-acp"), nil
	default:
		return "", fmt.Errorf("agentinstall: unknown agent %q (want claude, grok, codex, or all)", name)
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
	case "codex":
		if agentcli.Available(agentcli.CLICodex) {
			parts = append(parts, "codex on PATH")
		} else {
			parts = append(parts, "codex not on PATH (optional for ACP adapter)")
		}
		if _, err := exec.LookPath("npx"); err == nil {
			parts = append(parts, "npx on PATH")
		} else {
			parts = append(parts, "npx not on PATH — needed for DefaultCodexACPCommand")
		}
		if os.Getenv("CODEX_API_KEY") != "" || os.Getenv("OPENAI_API_KEY") != "" {
			parts = append(parts, "CODEX_API_KEY/OPENAI_API_KEY set")
		} else {
			parts = append(parts, "set CODEX_API_KEY or OPENAI_API_KEY for live dogfood")
		}
	}
	return strings.Join(parts, "; ")
}

// BindingSnippet returns a pasteable agents.toml fragment for the launcher path.
// Codex: multi-token ACP spawn via `sh <launcher>` so interface=acp accepts it
// (bare single-token paths are rejected). The launcher body execs
// DefaultCodexACPCommand with no stdio subcommand (sty_9e86f407 AC4/AC5).
func BindingSnippet(name, launcherPath string) string {
	switch name {
	case "codex":
		// sh + launcher is multi-token; launcher runs DefaultCodexACPCommand.
		cmd := "sh " + launcherPath
		return fmt.Sprintf(`[reviewer-codex-acp]
role       = "reviewer"
interface  = "acp"
command    = %q
tools      = "read_file,grep,list_dir"
principles = "session"
# Paste into .satelle/agents.toml — does not change the default [reviewer].
# Launcher execs: %s  (no stdio subcommand — not part of the adapter contract)
# Equivalent direct form: command = %q
# ENV: CODEX_API_KEY|OPENAI_API_KEY, CODEX_PATH, CODEX_CONFIG, INITIAL_AGENT_MODE, APP_SERVER_LOGS
`, cmd, agentcli.DefaultCodexACPCommand, agentcli.DefaultCodexACPCommand)
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
		out := make([]string, len(known))
		copy(out, known)
		return out, nil
	}
	for _, k := range known {
		if n == k {
			return []string{n}, nil
		}
	}
	return nil, fmt.Errorf("agentinstall: unknown agent %q (want %s, or all)",
		name, strings.Join(known, ", "))
}

func launcherScript(name, execLine string) string {
	// Derive exec targets from shipped constants so launchers cannot drift.
	// codex uses DefaultCodexACPCommand; claude/grok wrap the bare binary.
	switch name {
	case "codex":
		execLine = agentcli.DefaultCodexACPCommand
	case "claude":
		execLine = agentcli.CLIClaude
	case "grok":
		execLine = agentcli.CLIGrok
	}
	return fmt.Sprintf("#!/bin/sh\n%s %s — do not edit\nexec %s \"$@\"\n",
		MarkerPrefix, name, execLine)
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
