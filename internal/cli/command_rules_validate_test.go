package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// satelle validate reports a stale allow name, and a malformed rule, as
// failures that name what to fix.
func TestValidateAllCommandRuleAllowName(t *testing.T) {
	repo := tempRepo(t)
	t.Chdir(repo)
	writeRuleConfig(t, repo, `
[[gate.command_rule]]
pattern = "orbctl apply"
action = "deny"
allow = ["nosuchstate"]
message = "m"
`)
	out, err := runRootIn(t, "", "validate")
	if err == nil {
		t.Fatalf("validate passed with an allow entry that names no state:\n%s", out)
	}
	if !strings.Contains(out, "nosuchstate") || !strings.Contains(out, "# command rules") {
		t.Errorf("validate must name the stale entry:\n%s", out)
	}
}

func TestValidateAllCommandRulesPass(t *testing.T) {
	repo := tempRepo(t)
	t.Chdir(repo)
	writeRuleConfig(t, repo, exampleRules(t))
	out, err := runRootIn(t, "", "validate")
	if err != nil {
		t.Fatalf("a valid rule set must validate: %v\n%s", err, out)
	}
	if !strings.Contains(out, "PASS  command rules") {
		t.Errorf("validate must report the command rules check:\n%s", out)
	}
}

func TestValidateAllMalformedRule(t *testing.T) {
	repo := tempRepo(t)
	t.Chdir(repo)
	writeRuleConfig(t, repo, "\n[[gate.command_rule]]\npattern = \"orbctl apply\"\naction = \"block\"\nmessage = \"m\"\n")
	out, err := runRootIn(t, "", "validate")
	if err == nil {
		t.Fatalf("validate passed with a malformed rule:\n%s", out)
	}
	got := err.Error() + out
	for _, want := range []string{filepath.Join(repo, ".satelle", "satelle.toml"), "rule 0", "field action"} {
		if !strings.Contains(got, want) {
			t.Errorf("validate output lacks %q:\n%s", want, got)
		}
	}
	if _, serr := os.Stat(filepath.Join(repo, ".satelle", "satelle.toml")); serr != nil {
		t.Fatal(serr)
	}
}
