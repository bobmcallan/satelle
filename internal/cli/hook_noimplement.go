package cli

import (
	"path"
	"strings"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/spf13/cobra"
)

// evaluateNoImplement is the single model-rule decision used by both `hook gate`
// and `hook explain`. Empty no_implement_models means the rule is off.
func evaluateNoImplement(caller callerID, absTarget, repoRoot string, cfg config.Config) (decision, matchedGlob, exemptSource, reason string) {
	if len(cfg.Gate.NoImplementModels) == 0 {
		return "skip", "", "", "model check skipped: no_implement_models not set"
	}
	if absTarget != "" {
		if editExempt(cfg.ResolveNoImplementExemptPaths(repoRoot), repoRoot, absTarget) {
			return "skip", "", "no_implement_exempt_paths", "model check skipped: target matches no_implement_exempt_paths"
		}
		if editExemptPattern(cfg.ResolveNoImplementExemptGlobs(), repoRoot, absTarget) {
			return "skip", "", "no_implement_exempt_globs", "model check skipped: target matches no_implement_exempt_globs"
		}
	}
	if strings.TrimSpace(caller.Model) == "" {
		why := caller.Reason
		if why == "" {
			why = "no caller model resolvable from this payload"
		}
		return "skip", "", "", "model check skipped: " + why
	}
	for _, g := range cfg.Gate.NoImplementModels {
		g = strings.TrimSpace(g)
		if g == "" {
			continue
		}
		ok, err := path.Match(g, caller.Model)
		if err != nil || !ok {
			continue
		}
		msg := cfg.Gate.NoImplementMessage
		if msg == "" {
			msg = "satelle: caller model matches [gate] no_implement_models glob " + g
		}
		return "deny", g, "", withHeuristicMark(caller, msg)
	}
	return "allow", "", "", withHeuristicMark(caller, "caller model does not match no_implement_models")
}

func withHeuristicMark(caller callerID, msg string) string {
	if caller.Key != "mtime heuristic" {
		return msg
	}
	if strings.Contains(msg, "mtime heuristic") {
		return msg
	}
	return msg + " (mtime heuristic)"
}

func denyIfNoImplement(cmd *cobra.Command, raw []byte, target string) error {
	cfg, cfgPath, err := config.Load("")
	if err != nil {
		return nil
	}
	if len(cfg.Gate.NoImplementModels) == 0 {
		return nil
	}
	root := config.RepoRootFromConfigPath(cfgPath)
	abs := target
	if target != "" {
		abs = resolveAbsTarget(root, target)
	}
	caller := resolveCaller(raw, osCallerFS{})
	d, _, _, reason := evaluateNoImplement(caller, abs, root, cfg)
	if d == "deny" {
		return denyPreToolUse(cmd, raw, reason)
	}
	return nil
}

func formatHookExplain(caller callerID, decision, matched, exempt, reason string) string {
	or := func(s, fallback string) string {
		if strings.TrimSpace(s) == "" {
			return fallback
		}
		return s
	}
	key := or(caller.Key, "(unresolved)")
	return "transcript: " + or(caller.Transcript, "(none)") + "\n" +
		"key:        " + key + "\n" +
		"model:      " + or(caller.Model, "(none)") + "\n" +
		"matched:    " + or(matched, "(none)") + "\n" +
		"exempt:     " + or(exempt, "(none)") + "\n" +
		"decision:   " + or(decision, "skip") + "\n" +
		"reason:     " + or(reason, "") + "\n"
}
