package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// Global config lives under ~/.satelle/ — the one machine-wide touchpoint the
// spec reserves (the future workspace registry lives here too). It is kept in a
// file named config.toml, deliberately NOT satelle.toml, so the per-repo
// walk-up (which looks for .satelle/satelle.toml) can never mistake the global
// home for a repo root.
const (
	// GlobalConfigName is the global config filename under the global dir.
	GlobalConfigName = "config.toml"
	// DefaultServiceAddr is the bind address for the background service. Unlike
	// the transient `serve` default (127.0.0.1), the service defaults to all
	// interfaces so it is reachable across the WSL↔Windows boundary in any
	// networking mode. Restrict it to 127.0.0.1 in config to keep it off the LAN.
	DefaultServiceAddr = "0.0.0.0"
)

// GlobalConfig is the machine-wide config at ~/.satelle/config.toml.
type GlobalConfig struct {
	Service   ServiceConfig   `toml:"service"`
	Agent     AgentConfig     `toml:"agent"`
	Workspace WorkspaceConfig `toml:"workspace"`
	UI        UIConfig        `toml:"ui"`
}

// UIConfig holds user-level UI preferences shared across every repo, so the
// light/dark choice follows the operator rather than a single browser origin.
type UIConfig struct {
	Theme string `toml:"theme"` // "dark" | "light" (empty = light default)
}

// WorkspaceConfig is the connected-repo registry the workspace view aggregates.
// Per-repo databases stay the source of truth; this is just the list of paths.
type WorkspaceConfig struct {
	Repos []string `toml:"repos"`
}

// AddRepo adds an absolute repo path to the registry, de-duplicated. Reports
// whether it was newly added.
func (w *WorkspaceConfig) AddRepo(path string) bool {
	for _, r := range w.Repos {
		if r == path {
			return false
		}
	}
	w.Repos = append(w.Repos, path)
	return true
}

// RemoveRepo drops a repo path from the registry. Reports whether it was present.
func (w *WorkspaceConfig) RemoveRepo(path string) bool {
	out := w.Repos[:0]
	found := false
	for _, r := range w.Repos {
		if r == path {
			found = true
			continue
		}
		out = append(out, r)
	}
	w.Repos = out
	return found
}

// DefaultAgentCLI is the agent CLI the reviewer/summariser shell out to when
// none is selected — claude, whose flag surface satelle's runner mirrors.
const DefaultAgentCLI = "claude"

// AgentConfig selects the headless agent CLI the quality-management spine uses
// for isolated reviews/summaries. Set once at install (`satelle agent`).
type AgentConfig struct {
	// CLI is the agent CLI identifier (claude | codex). Empty resolves to
	// DefaultAgentCLI.
	CLI string `toml:"cli"`
}

// ResolveCLI returns the selected agent CLI, defaulting when unset.
func (a AgentConfig) ResolveCLI() string {
	if c := strings.TrimSpace(a.CLI); c != "" {
		return c
	}
	return DefaultAgentCLI
}

// ServiceConfig configures the background web service (`satelle service`).
type ServiceConfig struct {
	// Port the service listens on; zero means DefaultWebPort.
	Port int `toml:"port"`
	// Addr the service binds; empty means DefaultServiceAddr (0.0.0.0).
	Addr string `toml:"addr"`
	// Repo is the repository the service serves (its working directory). Empty
	// until set by `satelle service install`, which defaults it to the CWD.
	Repo string `toml:"repo"`
}

// ResolvePort returns the service port, defaulting when unset.
func (s ServiceConfig) ResolvePort() int {
	if s.Port > 0 {
		return s.Port
	}
	return DefaultWebPort
}

// ResolveAddr returns the service bind address, defaulting when unset.
func (s ServiceConfig) ResolveAddr() string {
	if a := strings.TrimSpace(s.Addr); a != "" {
		return a
	}
	return DefaultServiceAddr
}

// GlobalDir returns the machine-wide satelle home (~/.satelle), honoring the
// SATELLE_HOME override (used by tests). Falls back to ".satelle-global" in CWD
// only if the home directory cannot be resolved.
func GlobalDir() string {
	if v := strings.TrimSpace(os.Getenv("SATELLE_HOME")); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".satelle-global"
	}
	return filepath.Join(home, ".satelle")
}

// GlobalConfigPath returns the path to the global config file.
func GlobalConfigPath() string {
	return filepath.Join(GlobalDir(), GlobalConfigName)
}

// LoadGlobal reads the global config, returning a zero-value GlobalConfig (which
// resolves to defaults) when the file is absent. A present-but-malformed file is
// an error.
func LoadGlobal() (GlobalConfig, error) {
	path := GlobalConfigPath()
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return GlobalConfig{}, nil
	}
	if err != nil {
		return GlobalConfig{}, fmt.Errorf("config: read %s: %w", path, err)
	}
	var gc GlobalConfig
	if _, err := toml.Decode(string(b), &gc); err != nil {
		return GlobalConfig{}, fmt.Errorf("config: parse %s: %w", path, err)
	}
	return gc, nil
}

// SaveGlobal writes the global config to ~/.satelle/config.toml, creating the
// dir as needed. It renders a documented template (a fixed, satelle-managed
// shape) from the resolved values rather than re-encoding, so the file stays
// readable and self-explanatory.
func SaveGlobal(gc GlobalConfig) error {
	dir := GlobalDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("config: mkdir %s: %w", dir, err)
	}
	repos := "[]"
	if len(gc.Workspace.Repos) > 0 {
		quoted := make([]string, len(gc.Workspace.Repos))
		for i, r := range gc.Workspace.Repos {
			quoted[i] = fmt.Sprintf("%q", r)
		}
		repos = "[" + strings.Join(quoted, ", ") + "]"
	}
	body := fmt.Sprintf(globalTemplate, gc.Service.ResolvePort(), gc.Service.ResolveAddr(), gc.Service.Repo, gc.Agent.ResolveCLI(), repos, gc.UI.Theme)
	path := GlobalConfigPath()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return fmt.Errorf("config: write %s: %w", path, err)
	}
	return nil
}

// globalTemplate is the documented global config shape. Order/format are fixed
// so SaveGlobal produces a stable, human-readable file.
const globalTemplate = `# satelle global config (machine-wide, ~/.satelle/config.toml).
# Managed by ` + "`satelle service`" + `; safe to hand-edit, then re-run
# ` + "`satelle service install`" + ` to apply changes to the running service.

[service]
# port the background web service listens on.
port = %d
# addr it binds. 0.0.0.0 is reachable from Windows when satelle runs in WSL;
# set to "127.0.0.1" to keep the service off the local network.
addr = %q
# repo the service serves (its working directory). Set by 'service install'.
repo = %q

[agent]
# the headless agent CLI the reviewer/summariser shell out to (claude | codex).
# Set by 'satelle agent set <cli>' / 'satelle agent detect'.
cli = %q

[workspace]
# connected repo paths the /workspace view aggregates (per-repo DBs stay the
# source of truth). Manage with 'satelle workspace add|remove|list'.
repos = %s

[ui]
# light/dark theme shared across every repo's web UI ("dark" | "" = light).
# Set by the theme toggle in the web header; follows the operator across repos.
theme = %q
`
