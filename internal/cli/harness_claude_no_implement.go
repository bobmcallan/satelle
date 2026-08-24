package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/bobmcallan/satelle/internal/config"
)

func printNoImplementHandover(out io.Writer, repoRoot string) {
	cfg, _, err := config.Load(filepath.Join(repoRoot, config.DefaultDataDir, config.ConfigName))
	if err != nil {
		return
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	raw, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if err != nil {
		return
	}
	if n := noImplementHandoverNotice(cfg.Gate, raw); n != "" {
		fmt.Fprintln(out, "  i "+n)
	}
}

// noImplementHandoverNotice is the one-line init notice that the model-role
// rule now lives in satelle once [gate] no_implement_models is set. Empty when
// the keys are unset or ~/.claude/settings.json has no foreign PreToolUse
// Edit/Write command. The found command is interpolated — no script name is
// compiled in.
func noImplementHandoverNotice(cfg config.GateConfig, userSettings []byte) string {
	if len(cfg.NoImplementModels) == 0 {
		return ""
	}
	cmd := foreignEditPreToolUseCommand(userSettings)
	if cmd == "" {
		return ""
	}
	return "the no-implement rule is satelle-owned once [gate] no_implement_models is set — you can retire " + cmd + " from " + claudeUserSettingsRel
}

func foreignEditPreToolUseCommand(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	var doc struct {
		Hooks struct {
			PreToolUse []struct {
				Matcher string `json:"matcher"`
				Hooks   []struct {
					Command string `json:"command"`
				} `json:"hooks"`
			} `json:"PreToolUse"`
		} `json:"hooks"`
	}
	if json.Unmarshal(raw, &doc) != nil {
		return ""
	}
	for _, block := range doc.Hooks.PreToolUse {
		m := strings.ToLower(block.Matcher)
		if m != "" && !strings.Contains(m, "edit") && !strings.Contains(m, "write") && !strings.Contains(m, "notebook") {
			continue
		}
		for _, h := range block.Hooks {
			cmd := strings.TrimSpace(h.Command)
			if cmd == "" {
				continue
			}
			if strings.Contains(cmd, "satelle-hook.sh") || strings.Contains(cmd, "satelle hook") {
				continue
			}
			return cmd
		}
	}
	return ""
}
