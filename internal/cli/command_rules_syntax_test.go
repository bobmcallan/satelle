package cli

import (
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
)

// Pattern syntax beyond the exact argv prefix (sty_f54540d1). Every name here is
// fictional and authored by the test: satelle ships none.

func syntaxRules(pattern string) string {
	return "\n[[gate.command_rule]]\npattern = \"" + pattern + "\"\naction = \"deny\"\nmessage = \"refused by rule\"\n"
}

// AC1: a program matches by name however it is invoked.
func TestCommandRuleProgramAnyInvocation(t *testing.T) {
	ruleRepo(t, engagedNowhere, syntaxRules("**/orbctl"))
	denied := []string{
		"orbctl status",
		"/opt/x/bin/orbctl status",
		"./bin/orbctl status",
		"../orbctl status",
		"~/.orb/bin/orbctl status",
		"$HOME/.orb/bin/orbctl status",
		"${HOME}/.orb/bin/orbctl status",
		`"$HOME/.orb/bin/orbctl" status`,
		"A=1 $ORB_HOME/orbctl status",
		"cd /tmp && /opt/orbctl",
	}
	for _, c := range denied {
		out, err := commitgateOn(t, "claude", c)
		if err == nil {
			t.Errorf("%q must be denied", c)
			continue
		}
		if r := ruleDenyReason(t, out); !strings.Contains(r, "refused by rule") {
			t.Errorf("%q: reason = %s", c, r)
		}
	}
	for _, c := range []string{"orbctlx status", "/opt/orbctl/bin/other", "echo orbctl", "xorbctl", "ls /opt/orbctl"} {
		if out, err := commitgateOn(t, "claude", c); err != nil || out != "" {
			t.Errorf("%q must pass: %v %q", c, err, out)
		}
	}
}

// AC2: text inside an argument, at any position after the program.
func TestCommandRuleArgumentAnywhere(t *testing.T) {
	ruleRepo(t, engagedNowhere, syntaxRules("**/fetchy ** *api.example.test*"))
	denied := []string{
		"fetchy https://api.example.test/v1",
		"fetchy -s https://api.example.test/v1 -o out",
		`fetchy -s -H "X-Token: t" https://api.example.test/graphql`,
		`fetchy -s -H 'X-Token: t' -d '{"q":1}' https://api.example.test/graphql`,
		"fetchy '--url=https://api.example.test/x'",
		"fetchy --url=https://api.example.test/x",
		`/usr/bin/fetchy -X POST "https://api.example.test"`,
		"$HOME/bin/fetchy api.example.test",
	}
	for _, c := range denied {
		if _, err := commitgateOn(t, "claude", c); err == nil {
			t.Errorf("%q must be denied", c)
		}
	}
	for _, c := range []string{
		"fetchy https://other.example.test/v1",
		"fetchy -s -H 'X: y' https://cdn.example.org",
		"fetchy",
		"echo api.example.test",
		"other https://api.example.test",
	} {
		if out, err := commitgateOn(t, "claude", c); err != nil || out != "" {
			t.Errorf("%q must pass: %v %q", c, err, out)
		}
	}
}

func TestCommandRulesNewSyntaxMatching(t *testing.T) {
	cases := []struct {
		pattern, cmd string
		want         bool
	}{
		{"fetchy **", "fetchy", true},
		{"fetchy ** done", "fetchy done", true},
		{"fetchy ** done", "fetchy a b c done more", true},
		{"fetchy ** done", "fetchy a b c", false},
		{"** fetchy", "sudo fetchy x", true},
		{"** fetchy", "fetchy x", true},
		{"fetchy ** a ** b", "fetchy x a y b", true},
		{"fetchy ** a ** b", "fetchy x b y a", false},
		{"fetchy ** a b", "fetchy a a b", true},
		{"fetchy *host* *", "fetchy ahostb x", true},
		{"fetchy *host* *", "fetchy ahostb", false},
		{"fetchy *host*", "fetchy hos", false},
		{"fetchy *host*", "fetchy host", true},
		{"**/fetchy", "FETCHY", false},
		{"**/fetchy *", "fetchy", false},
		{"fetchy *x*", "fetchy 'a b x'", true},
		{"fetchy *x*", "fetchy <<EOF\nx\nEOF", false},
	}
	for _, tc := range cases {
		rules := []config.CommandRule{{Pattern: tc.pattern, Action: "deny", Message: "m"}}
		if _, got := matchCommandRule(rules, tc.cmd); got != tc.want {
			t.Errorf("pattern %q on %q = %v, want %v", tc.pattern, tc.cmd, got, tc.want)
		}
	}
}

// legacyPrefixMatch is the 0.0.677 algorithm: an exact argv prefix, where a
// token of * matches one token.
func legacyPrefixMatch(pattern, argv []string) bool {
	if len(pattern) == 0 || len(argv) < len(pattern) {
		return false
	}
	for i, p := range pattern {
		if p != "*" && argv[i] != p {
			return false
		}
	}
	return true
}

// AC3: a pattern using none of the new syntax matches exactly as before.
func TestCommandRulesLegacyPatternUnchanged(t *testing.T) {
	patterns := []string{"orbctl scale * --force", "orbctl apply", "orbctl", "* apply", "orbctl * *", "*"}
	argvs := [][]string{
		{"orbctl"},
		{"orbctl", "apply"},
		{"orbctl", "apply", "-f", "x"},
		{"orbctl", "applyx"},
		{"orbctl", "scale", "web", "--force"},
		{"orbctl", "scale", "--force"},
		{"orbctl", "scale", "web", "--force", "extra"},
		{"orbctl", "scale", "web"},
		{"other", "apply"},
		{unquotedPlaceholder, "apply"},
		{"orbctl", unquotedPlaceholder},
		{"/opt/orbctl", "apply"},
		{},
	}
	for _, p := range patterns {
		parsed, err := config.ParseCommandRulePattern(p)
		if err != nil {
			t.Fatalf("%q: %v", p, err)
		}
		for _, a := range argvs {
			if got, want := patternMatchesArgv(parsed, a), legacyPrefixMatch(strings.Fields(p), a); got != want {
				t.Errorf("pattern %q argv %q: new %v, 0.0.677 %v", p, a, got, want)
			}
		}
	}
}

// AC4: a malformed wildcard pattern names the rule and the field, never the value.
func TestValidateAllMalformedWildcardPattern(t *testing.T) {
	repo := tempRepo(t)
	t.Chdir(repo)
	writeRuleConfig(t, repo, "\n[[gate.command_rule]]\npattern = \"orbctl apply\"\naction = \"deny\"\nmessage = \"m\"\n\n[[gate.command_rule]]\npattern = \"**/orbctl*\"\naction = \"deny\"\nmessage = \"m\"\n")
	out, err := runRootIn(t, "", "validate")
	if err == nil {
		t.Fatalf("validate passed with a malformed pattern:\n%s", out)
	}
	got := err.Error() + out
	for _, want := range []string{"rule 1", "field pattern"} {
		if !strings.Contains(got, want) {
			t.Errorf("validate output lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "orbctl*") {
		t.Errorf("validate echoes the pattern:\n%s", got)
	}
}

func newSyntaxBenchRules() []config.CommandRule {
	var rules []config.CommandRule
	for i := 0; i < 100; i++ {
		name := "tool" + string(rune('a'+i%26))
		if i%2 == 0 {
			rules = append(rules, config.CommandRule{Pattern: "**/" + name + " ** *host" + string(rune('a'+i%26)) + "*", Action: "deny", Message: "m"})
		} else {
			rules = append(rules, config.CommandRule{Pattern: name + " sub * --flag", Action: "deny", Message: "m"})
		}
	}
	return rules
}

func BenchmarkCommandRulesNewSyntax(b *testing.B) {
	rules := newSyntaxBenchRules()
	const command = "cd /srv/app && git status --short | grep -v x; ls -la internal/cli"
	resolve := func() (engagement, bool, error) { return engagement{}, false, nil }
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		evalCommandRules(rules, command, resolve)
	}
}

// AC5: the new syntax stays within the same relative bound as exact prefixes.
func TestCommandRulesRelativeCostNewSyntax(t *testing.T) {
	rules := newSyntaxBenchRules()
	const command = "cd /srv/app && git status --short | grep -v x; ls -la internal/cli"
	resolve := func() (engagement, bool, error) { return engagement{}, false, nil }
	checkRelativeCost(t,
		func() { evalCommandRules(rules, command, resolve) },
		func() { gitSubcommands(command) })
}
