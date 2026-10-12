// The rules a resident document is condensed by when its context has a size
// budget (sty_df21b2db). They are configuration: an embedded default
// (substrate/config/condense.toml) a repo overrides, key by key, in
// <data dir>/config/condense.toml. internal/condense parses and applies them;
// this file only finds the two layers, so the condenser holds no word of its own.
package config

import (
	"os"
	"path/filepath"
)

const condenseFile = "substrate/config/condense.toml"

// CondenseOverrideRel is where a repo's condense overrides live, relative to its
// data dir.
const CondenseOverrideRel = "config/condense.toml"

// EmbeddedCondense returns the embedded default condense rules (TOML). Empty
// when the embed is unreadable, which the parser reports as a missing ladder.
func EmbeddedCondense() []byte {
	raw, err := substrateFS.ReadFile(condenseFile)
	if err != nil {
		return nil
	}
	return raw
}

// CondenseOverride returns the repo's condense overrides (TOML), or nil when the
// repo sets none or the file cannot be read.
func CondenseOverride(dataDir string) []byte {
	if dataDir == "" {
		return nil
	}
	raw, err := os.ReadFile(filepath.Join(dataDir, filepath.FromSlash(CondenseOverrideRel)))
	if err != nil {
		return nil
	}
	return raw
}
