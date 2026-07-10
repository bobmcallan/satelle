package config

import (
	"fmt"
	"os"
	"path/filepath"
)

// GrokConfigName is Grok's user-level config filename under ~/.grok.
// [compat.*] lives only here (not project-scoped) — see Grok docs on harness
// compatibility.
const GrokConfigName = "config.toml"

// GrokConfigPath returns ~/.grok/config.toml. os.UserHomeDir honors HOME, so
// tests can t.Setenv("HOME", tmp) without a satelle-specific override.
func GrokConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("config: resolve home for Grok config: %w", err)
	}
	return filepath.Join(home, ".grok", GrokConfigName), nil
}
