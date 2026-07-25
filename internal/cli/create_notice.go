// create_notice.go — surface the ungated-create state on the story/task create path.
//
// Repos initialised before the gate_create seed (sty_83782ffb) still carry a
// commented [review] block; GateCreate falls back to false and nothing tells
// the operator. Detection reuses scaffoldConfigDefaults (init_analysis.go) so
// the enable-block text has one owner; configKeyDefined covers committed
// satelle.toml and the satelle.local.toml overlay so "defined" means the same
// thing on create as on init/validate.
package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bobmcallan/satelle/internal/config"
)

// configKeyDefined reports whether section.key is present (uncommented) in
// committed satelle.toml and/or the satelle.local.toml overlay under dataDir.
// Overlay-only definitions count — Load merges them per-key. Unreadable /
// absent files do not count as defined.
func configKeyDefined(dataDir, section, key string) bool {
	for _, name := range []string{config.ConfigName, config.LocalConfigName} {
		raw, err := os.ReadFile(filepath.Join(dataDir, name))
		if err != nil {
			continue
		}
		if config.HasKey(string(raw), section, key) {
			return true
		}
	}
	return false
}

// scaffoldDefault looks up a scaffoldConfigDefaults entry by section+key.
func scaffoldDefault(section, key string) (scaffoldConfigDefault, bool) {
	for _, d := range scaffoldConfigDefaults {
		if d.Section == section && d.Key == key {
			return d, true
		}
	}
	return scaffoldConfigDefault{}, false
}

// createGateNotice returns an advisory when stories/tasks are being created
// without the create gate, or "" when the notice must stay silent.
//
// Silent when:
//   - gateOn is true (cfg.Review.GateCreate) — correctly gated repo
//   - [review] gate_create is defined anywhere (committed or overlay) — deliberate
//     opt-out (false) or enable (true); only "absent" is the pre-seed trap
//
// The enable block is taken from scaffoldConfigDefaults so Block/Why have one owner.
func createGateNotice(gateOn bool, dataDir string) string {
	if gateOn {
		return ""
	}
	if configKeyDefined(dataDir, "review", "gate_create") {
		return ""
	}
	d, ok := scaffoldDefault("review", "gate_create")
	if !ok {
		// Table missing the entry would be a programming error; stay silent
		// rather than invent a second source of truth for the block text.
		return ""
	}
	// Indent the multi-line Block for the notice body.
	block := d.Block
	indented := indentBlock(block, "    ")
	return fmt.Sprintf(
		"notice: created WITHOUT create review — %s/%s defines no [review] gate_create, so the workflow's create_review skill never ran.\n"+
			"  enable (add to %s/%s):\n%s\n"+
			"  or set gate_create = false to opt out explicitly (silences this notice).\n",
		config.DefaultDataDir, config.ConfigName,
		config.DefaultDataDir, config.ConfigName,
		indented,
	)
}

// indentBlock prefixes each non-empty line of s with prefix.
func indentBlock(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if line != "" {
			lines[i] = prefix + line
		}
	}
	return strings.Join(lines, "\n")
}

// categoryNotice returns the warn-mode advisory for an unknown category, or "".
// Reject-mode is enforced in verb (hard error); off is silent. The list and
// wording come from config.CategoryWarning so allowed values have one owner.
func categoryNotice(cfg config.Config, category string) string {
	return cfg.CategoryWarning(category)
}
