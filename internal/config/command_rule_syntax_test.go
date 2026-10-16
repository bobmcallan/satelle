package config

import (
	"errors"
	"strings"
	"testing"
)

// A malformed pattern in the wildcard syntax is refused naming the rule and the
// field, never the value (sty_f54540d1).
func TestLoadCommandRulesMalformedWildcardPattern(t *testing.T) {
	for _, pat := range []string{
		"**/", "**/a/b", "**/a*", "a **/b", "** **/b", "a*", "*a", "a*b", "***", "**x", "*a*b*", "**/*",
	} {
		t.Run(pat, func(t *testing.T) {
			root := t.TempDir()
			p := writeSatelleToml(t, root, ConfigName, "[[gate.command_rule]]\npattern = \"ok\"\naction = \"deny\"\nmessage = \"m\"\n\n[[gate.command_rule]]\npattern = \""+pat+"\"\naction = \"deny\"\nmessage = \"m\"\n")
			_, _, err := Load(p)
			var cre *CommandRuleError
			if !errors.As(err, &cre) || cre.Field != "pattern" || cre.Index != 1 {
				t.Fatalf("err = %v, want rule 1 field pattern", err)
			}
		})
	}
}

// Whatever is wrong with a pattern, its text is never in the error.
func TestLoadCommandRulesMalformedWildcardNeverEchoed(t *testing.T) {
	for _, pat := range []string{"SECRETVAL*", "**/SECRETVAL/x", "SECRETVAL**", "x **/SECRETVAL", "**/SECRETVAL*", "*SECRETVAL*x*", "SECRETVAL ;"} {
		root := t.TempDir()
		p := writeSatelleToml(t, root, ConfigName, "[[gate.command_rule]]\npattern = \""+pat+"\"\naction = \"deny\"\nmessage = \"m\"\n")
		_, _, err := Load(p)
		var cre *CommandRuleError
		if !errors.As(err, &cre) || cre.Field != "pattern" || cre.Index != 0 {
			t.Errorf("%q: err = %v, want rule 0 field pattern", pat, err)
			continue
		}
		if strings.Contains(err.Error(), "SECRETVAL") {
			t.Errorf("error echoes the pattern: %v", err)
		}
	}
}

func TestLoadCommandRulesWildcardPatternsAccepted(t *testing.T) {
	for _, pat := range []string{"**/orbctl", "**/orbctl ** *api.example.test*", "orbctl * --force", "** orbctl", "orbctl **", "*host*"} {
		root := t.TempDir()
		p := writeSatelleToml(t, root, ConfigName, "[[gate.command_rule]]\npattern = \""+pat+"\"\naction = \"deny\"\nmessage = \"m\"\n")
		cfg, _, err := Load(p)
		if err != nil {
			t.Errorf("%q refused: %v", pat, err)
			continue
		}
		if len(cfg.Gate.CommandRules[0].Tokens) == 0 {
			t.Errorf("%q not classified at load", pat)
		}
	}
}
